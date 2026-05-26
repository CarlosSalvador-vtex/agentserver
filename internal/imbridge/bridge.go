package imbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	bridgeRetryDelay       = 2 * time.Second
	bridgeBackoffDelay     = 30 * time.Second
	maxConsecutiveFailures = 3
	forwardTimeout         = 10 * time.Second
)

// BridgeDB is the DB interface needed by the bridge.
type BridgeDB interface {
	UpdateIMChannelCursor(channelID, cursor string) error
	UpsertChannelMeta(channelID, userID, key, value string) error
	GetChannelMeta(channelID, userID, key string) (string, error)
	GetAllChannelMeta(channelID, userID string) (map[string]string, error)
	GetSandboxForChannel(channelID string) (sandboxID, podIP, bridgeSecret, assistantName string, err error)
	// DispatchInboundChannel looks up the channel by ID and returns the
	// fields needed to construct a BridgeBinding for push-based providers
	// (e.g. WhatsApp webhooks).
	DispatchInboundChannel(channelID string) (workspaceID, provider, botID, botToken, baseURL, routingMode string, err error)
}

// SandboxResolver looks up the current state of a sandbox.
type SandboxResolver interface {
	GetPodIP(sandboxID string) string
}

// ExecCommander can execute a command inside a sandbox pod.
type ExecCommander interface {
	ExecSimple(ctx context.Context, sandboxID string, command []string) (string, error)
}

// BridgeBinding holds the info needed to run a poller for one IM channel.
// The sandbox to forward messages to is resolved dynamically from the channel ID.
type BridgeBinding struct {
	Provider    Provider
	Credentials Credentials
	ChannelID   string // workspace_im_channels.id
	Cursor      string
	WorkspaceID string // workspace that owns this channel
	RoutingMode string // "codex" (default) or legacy "nanoclaw"
}

// pollerEntry tracks an active poller so the bridge can both cancel it and
// invoke its provider's lifecycle hooks (e.g. notifyStop) on shutdown.
type pollerEntry struct {
	cancel  context.CancelFunc
	binding BridgeBinding
}

// Bridge manages per-binding poll goroutines for all IM providers.
type Bridge struct {
	db               BridgeDB
	resolver         SandboxResolver
	exec             ExecCommander
	agentserverURL   string
	providers        map[string]Provider
	pollers          map[string]pollerEntry // key: channelID
	channelMention   map[string]bool        // key: channelID → require_mention setting
	channelRouting   map[string]string      // key: channelID → routing_mode (runtime override of binding)
	typingSessions   map[string]func()      // key: "channelID:userID" → cancel func
	mu               sync.Mutex
}

// NewBridge creates a new Bridge instance with the given providers.
func NewBridge(db BridgeDB, resolver SandboxResolver, exec ExecCommander, providers []Provider) *Bridge {
	pm := make(map[string]Provider, len(providers))
	for _, p := range providers {
		pm[p.Name()] = p
	}
	agentserverURL := os.Getenv("AGENTSERVER_URL")
	if agentserverURL == "" {
		agentserverURL = "http://localhost:8080"
	}
	return &Bridge{
		db:               db,
		resolver:         resolver,
		exec:             exec,
		agentserverURL:   agentserverURL,
		providers:        pm,
		pollers:          make(map[string]pollerEntry),
		channelMention:   make(map[string]bool),
		channelRouting:   make(map[string]string),
		typingSessions:   make(map[string]func()),
	}
}

// Providers returns all registered providers.
func (b *Bridge) Providers() []Provider {
	out := make([]Provider, 0, len(b.providers))
	for _, p := range b.providers {
		out = append(out, p)
	}
	return out
}

// GetProvider returns the provider with the given name, or nil if not found.
func (b *Bridge) GetProvider(name string) Provider {
	return b.providers[name]
}

// StartPoller starts a long-poll goroutine for a channel.
// If a poller already exists for this channel, it is stopped first.
//
// If the provider implements LifecycleProvider, OnPollerStart is invoked
// best-effort in a separate goroutine; failures are logged but do not
// block poller startup.
func (b *Bridge) StartPoller(binding BridgeBinding) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := binding.ChannelID
	prevEntry, replacing := b.pollers[key]
	if replacing {
		prevEntry.cancel()
	}

	// Seed the in-memory routing map so getChannelRoutingMode returns
	// the value that forwardMessage would use by default. Later calls
	// to SetChannelRoutingMode override this seeded value.
	b.channelRouting[key] = binding.RoutingMode

	ctx, cancel := context.WithCancel(context.Background())
	b.pollers[key] = pollerEntry{cancel: cancel, binding: binding}

	if replacing {
		// Same channelID often means same bot account (re-login). Serialize
		// notifyStop(old) → notifyStart(new) so the upstream server's per-account
		// online state is not corrupted by reordering. Detached from the bridge
		// lock; the new pollLoop starts in parallel since it doesn't depend on
		// notify lifecycle.
		go func() {
			invokeOnPollerStop(prevEntry.binding)
			invokeOnPollerStart(binding)
		}()
	} else {
		go invokeOnPollerStart(binding)
	}
	go b.pollLoop(ctx, binding)
}

// StopPoller stops the polling goroutine for a specific channel and, if the
// provider implements LifecycleProvider, fires OnPollerStop in a separate
// goroutine (best-effort, won't block return).
func (b *Bridge) StopPoller(channelID string) {
	b.mu.Lock()
	entry, ok := b.pollers[channelID]
	if ok {
		entry.cancel()
		delete(b.pollers, channelID)
	}
	b.mu.Unlock()
	if ok {
		go invokeOnPollerStop(entry.binding)
	}
}

// StopAllPollers stops all polling goroutines and typing sessions.
// LifecycleProvider hooks fire best-effort in goroutines.
func (b *Bridge) StopAllPollers() {
	b.mu.Lock()
	entries := make([]pollerEntry, 0, len(b.pollers))
	for key, entry := range b.pollers {
		entry.cancel()
		entries = append(entries, entry)
		delete(b.pollers, key)
	}
	for key, cancel := range b.typingSessions {
		cancel()
		delete(b.typingSessions, key)
	}
	b.mu.Unlock()
	for _, e := range entries {
		go invokeOnPollerStop(e.binding)
	}
}

// invokeOnPollerStart fires the optional LifecycleProvider.OnPollerStart hook
// with a short timeout. Failures are logged and ignored.
func invokeOnPollerStart(binding BridgeBinding) {
	lp, ok := binding.Provider.(LifecycleProvider)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lp.OnPollerStart(ctx, &binding.Credentials); err != nil {
		log.Printf("imbridge: %s OnPollerStart failed (ignored) channel=%s: %v",
			binding.Provider.Name(), binding.ChannelID, err)
	}
}

// invokeOnPollerStop fires the optional LifecycleProvider.OnPollerStop hook
// with a short timeout. Failures are logged and ignored.
func invokeOnPollerStop(binding BridgeBinding) {
	lp, ok := binding.Provider.(LifecycleProvider)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lp.OnPollerStop(ctx, &binding.Credentials); err != nil {
		log.Printf("imbridge: %s OnPollerStop failed (ignored) channel=%s: %v",
			binding.Provider.Name(), binding.ChannelID, err)
	}
}

// FindProviderByJID matches a JID suffix to a provider.
// Returns nil if no provider matches.
func (b *Bridge) FindProviderByJID(jid string) Provider {
	for _, p := range b.providers {
		if strings.HasSuffix(jid, p.JIDSuffix()) {
			return p
		}
	}
	return nil
}

func typingKey(channelID, userID string) string {
	return channelID + ":" + userID
}

// startTypingForUser starts a typing indicator session if the provider supports it.
func (b *Bridge) startTypingForUser(binding BridgeBinding, msg InboundMessage) {
	tp, ok := binding.Provider.(TypingProvider)
	if !ok {
		return
	}

	key := typingKey(binding.ChannelID, msg.FromUserID)

	sendError := func(text string) {
		if err := binding.Provider.Send(context.Background(), &binding.Credentials, msg.FromUserID, text, msg.Metadata); err != nil {
			log.Printf("imbridge: failed to send error notice to %s: %v", msg.FromUserID, err)
		}
	}

	// Create context with timeout and register cancel in map BEFORE starting
	// the typing goroutine, so StopTyping can find it even if a reply arrives
	// quickly. The 60-minute timeout ensures goroutines don't leak if NanoClaw
	// never replies, and triggers an error notice to the user.
	ctx, cancelFn := context.WithTimeout(context.Background(), 60*time.Minute)

	b.mu.Lock()
	if existingCancel, exists := b.typingSessions[key]; exists {
		existingCancel()
	}
	b.typingSessions[key] = cancelFn
	b.mu.Unlock()

	// Start typing asynchronously using the pre-registered context.
	tp.StartTyping(ctx, &binding.Credentials, msg.FromUserID, msg.Metadata, sendError)
}

// SetChannelRequireMention updates the in-memory require_mention setting for a channel.
func (b *Bridge) SetChannelRequireMention(channelID string, requireMention bool) {
	b.mu.Lock()
	b.channelMention[channelID] = requireMention
	b.mu.Unlock()
}

// getChannelRequireMention reads the in-memory require_mention setting.
func (b *Bridge) getChannelRequireMention(channelID string) bool {
	b.mu.Lock()
	v := b.channelMention[channelID]
	b.mu.Unlock()
	return v
}

// SetChannelRoutingMode updates the in-memory routing_mode for a channel.
// The value takes precedence over the routing_mode captured in
// BridgeBinding at StartPoller time, so a configuration change applied
// via this setter is visible on the next inbound message.
func (b *Bridge) SetChannelRoutingMode(channelID, mode string) {
	b.mu.Lock()
	b.channelRouting[channelID] = mode
	b.mu.Unlock()
}

// getChannelRoutingMode reads the in-memory routing_mode. Returns ""
// if the channel has no override — callers fall back to
// BridgeBinding.RoutingMode in that case.
func (b *Bridge) getChannelRoutingMode(channelID string) string {
	b.mu.Lock()
	v := b.channelRouting[channelID]
	b.mu.Unlock()
	return v
}

// StopTyping stops the typing indicator for a user in a channel.
// It first tries an exact match on channelID:userID, then falls back
// to stopping all typing sessions for the channel (needed for group
// chats where the reply's to_user_id is the room, not the sender).
func (b *Bridge) StopTyping(channelID, userID string) {
	key := typingKey(channelID, userID)
	b.mu.Lock()
	cancel, ok := b.typingSessions[key]
	if ok {
		delete(b.typingSessions, key)
		b.mu.Unlock()
		cancel()
		return
	}
	// Fallback: stop all typing sessions for this channel.
	prefix := channelID + ":"
	var toCancel []func()
	for k, c := range b.typingSessions {
		if strings.HasPrefix(k, prefix) {
			toCancel = append(toCancel, c)
			delete(b.typingSessions, k)
		}
	}
	b.mu.Unlock()
	for _, c := range toCancel {
		c()
	}
}

// pollLoop is the long-poll goroutine for a single channel.
func (b *Bridge) pollLoop(ctx context.Context, binding BridgeBinding) {
	cursor := binding.Cursor
	consecutiveFailures := 0
	providerName := binding.Provider.Name()
	channelID := binding.ChannelID
	botID := binding.Credentials.BotID

	log.Printf("imbridge: starting poller for channel=%s provider=%s bot=%s", channelID, providerName, botID)

	for {
		if ctx.Err() != nil {
			log.Printf("imbridge: poller stopped for channel=%s provider=%s bot=%s", channelID, providerName, botID)
			return
		}

		result, err := binding.Provider.Poll(ctx, &binding.Credentials, cursor)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveFailures++
			log.Printf("imbridge: poll error channel=%s provider=%s bot=%s err=%v (%d/%d)",
				channelID, providerName, botID, err, consecutiveFailures, maxConsecutiveFailures)
			if consecutiveFailures >= maxConsecutiveFailures {
				consecutiveFailures = 0
				sleepCtx(ctx, bridgeBackoffDelay)
			} else {
				sleepCtx(ctx, bridgeRetryDelay)
			}
			continue
		}

		if result.ShouldBackoff > 0 {
			log.Printf("imbridge: provider requested backoff=%v channel=%s provider=%s", result.ShouldBackoff, channelID, providerName)
			sleepCtx(ctx, result.ShouldBackoff)
			continue
		}

		consecutiveFailures = 0

		// Forward messages BEFORE advancing cursor.
		allForwarded := true
		for _, msg := range result.Messages {
			// Persist provider-specific metadata.
			for k, v := range msg.Metadata {
				if err := b.db.UpsertChannelMeta(channelID, msg.FromUserID, k, v); err != nil {
					log.Printf("imbridge: failed to save metadata key=%s: %v", k, err)
				}
			}

			forwarded, err := b.forwardMessage(ctx, binding, msg)
			if err != nil {
				log.Printf("imbridge: forward failed channel=%s from=%s: %v (will retry next poll)",
					channelID, msg.FromUserID, err)
				allForwarded = false
				break
			}
			if forwarded {
				b.startTypingForUser(binding, msg)
			}
		}

		if allForwarded && result.NewCursor != "" {
			cursor = result.NewCursor
			if err := b.db.UpdateIMChannelCursor(channelID, cursor); err != nil {
				log.Printf("imbridge: failed to save cursor channel=%s: %v", channelID, err)
			}
		}

		if !allForwarded {
			sleepCtx(ctx, bridgeRetryDelay)
		}
	}
}

// forwardMessage routes an inbound message based on the binding's RoutingMode.
// "codex" (and empty/default) forwards to agentserver's codex-app-gateway
// path. Legacy "nanoclaw" routing mode is no longer supported.
// DispatchInbound feeds a single inbound message into the same forward
// pipeline used by polling providers. Used by push-based providers
// (e.g. WhatsApp Cloud webhook handlers) that can't sit inside the
// poll loop. The binding is reconstructed from the channel row.
//
// Returns the same (handled, err) shape as forwardMessage:
//   - handled=true when the message was fully delivered to a sandbox.
//   - handled=false + err==nil when the channel is not yet bound to a
//     running sandbox (caller decides whether to drop or retry).
//   - err != nil for transport/marshal failures.
func (b *Bridge) DispatchInbound(ctx context.Context, channelID string, msg InboundMessage) (bool, error) {
	wsID, providerName, botID, botToken, baseURL, routingMode, err := b.db.DispatchInboundChannel(channelID)
	if err != nil {
		return false, fmt.Errorf("lookup channel %s: %w", channelID, err)
	}
	provider := b.GetProvider(providerName)
	if provider == nil {
		return false, fmt.Errorf("no provider registered for %q", providerName)
	}
	binding := BridgeBinding{
		Provider: provider,
		Credentials: Credentials{
			ChannelID: channelID,
			BotID:     botID,
			BotToken:  botToken,
			BaseURL:   baseURL,
		},
		ChannelID:   channelID,
		WorkspaceID: wsID,
		RoutingMode: routingMode,
	}
	return b.forwardMessage(ctx, binding, msg)
}

func (b *Bridge) forwardMessage(ctx context.Context, binding BridgeBinding, msg InboundMessage) (bool, error) {
	// In-memory routing mode (set via SetChannelRoutingMode) wins over
	// the routing_mode captured at StartPoller time. Empty map value
	// means no override — fall through to binding.RoutingMode.
	mode := b.getChannelRoutingMode(binding.ChannelID)
	if mode == "" {
		mode = binding.RoutingMode
	}
	if mode == "nanoclaw" {
		log.Printf("imbridge: routing_mode=nanoclaw is deprecated for channel=%s; using codex", binding.ChannelID)
	}
	return b.forwardToCodex(ctx, binding, msg)
}

// forwardToCodex POSTs the inbound message to agentserver's
// /api/internal/imbridge/codex/turn endpoint, which enqueues it into
// its per-user FIFO and asynchronously calls codex-app-gateway.
// Mirrors forwardToAgentserver's shape (HTTP-based, fire-and-forget
// for the reply — that comes back via /api/internal/imbridge/send).
func (b *Bridge) forwardToCodex(ctx context.Context, binding BridgeBinding, msg InboundMessage) (bool, error) {
	payload := map[string]any{
		"channel_id":         binding.ChannelID,
		"workspace_id":       binding.WorkspaceID,
		"wechat_user_id":     msg.FromUserID,
		"wechat_sender_name": msg.SenderName,
		"text":               msg.Text,
		"quoted_text":        msg.QuotedText,
		"quoted_sender":      msg.QuotedSender,
	}
	// Forward image bytes so codex turn input can carry them as a
	// `data:<mime>;base64,...` URL (UserInput::Image). Mirrors
	// forwardToNanoClaw's media payload shape, minus the filename —
	// codex has no File variant and the image case doesn't need the
	// filename. File attachments still come through imbridge's text
	// fallback ("[User sent a file: X]" via describeWeixinMedia); a
	// proper file-content path is deferred until designed.
	if len(msg.MediaData) > 0 {
		payload["media_data"] = base64.StdEncoding.EncodeToString(msg.MediaData)
		payload["media_type"] = msg.MediaType
	}
	if len(msg.QuotedMediaData) > 0 {
		payload["quoted_media_data"] = base64.StdEncoding.EncodeToString(msg.QuotedMediaData)
		payload["quoted_media_type"] = msg.QuotedMediaType
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal codex inbound: %w", err)
	}
	url := b.agentserverURL + "/api/internal/imbridge/codex/turn"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		req.Header.Set("X-Internal-Secret", secret)
	}
	hctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(hctx))
	if err != nil {
		return false, fmt.Errorf("forward codex: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return false, fmt.Errorf("codex inbound: status %d", resp.StatusCode)
	}
	return true, nil
}

// sleepCtx sleeps for the given duration or until the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
