package imbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/sandbox"
)

// WhatsAppProvider integrates the WhatsApp Cloud API (Meta/Facebook).
//
// Unlike Telegram/WeChat/Matrix, this provider is **push-based** — Meta
// delivers inbound messages via webhook to the agentserver, not via
// long-poll. Poll() therefore returns no messages and a short backoff
// so the bridge loop stays idle. The webhook handler at
// /webhook/whatsapp parses Meta's payload and feeds messages into the
// dispatch pipeline via Bridge.DispatchInbound.
//
// Credentials mapping:
//
//	BotID    = WhatsApp Business phone_number_id (from Meta dashboard)
//	BotToken = long-lived access token (Bearer)
//	BaseURL  = Graph API root, defaults to https://graph.facebook.com/v18.0
type WhatsAppProvider struct{}

const (
	whatsappDefaultBaseURL = "https://graph.facebook.com/v18.0"
	whatsappJIDSuffix      = "@wa"
)

func (p *WhatsAppProvider) Name() string { return sandbox.ProviderWhatsApp.String() }
func (p *WhatsAppProvider) JIDSuffix() string { return whatsappJIDSuffix }

// Poll is a no-op for WhatsApp Cloud — messages arrive via webhook.
// Returns a long backoff so the bridge poller goroutine stays parked
// without burning CPU.
func (p *WhatsAppProvider) Poll(ctx context.Context, creds *Credentials, cursor string) (*PollResult, error) {
	return &PollResult{
		Messages:      nil,
		NewCursor:     cursor,
		ShouldBackoff: 5 * time.Minute,
	}, nil
}

// Send posts a text message to a WhatsApp recipient via the Graph API.
//
// toUserID is the recipient's wa_id (E.164 without "+") optionally
// suffixed with "@wa". The function strips the suffix before building
// the request payload.
func (p *WhatsAppProvider) Send(ctx context.Context, creds *Credentials, toUserID, text string, meta map[string]string) error {
	if creds == nil || creds.BotID == "" {
		return fmt.Errorf("whatsapp: missing phone_number_id (creds.BotID)")
	}
	if creds.BotToken == "" {
		return fmt.Errorf("whatsapp: missing access token")
	}
	if text == "" {
		return fmt.Errorf("whatsapp: empty text")
	}

	checker := EffectiveOutboundGuardrails(ctx, creds, runtimeLLMProxyURL, runtimeTokenProvider)
	if dec := checker.CheckOutbound(ctx, text); !dec.Allowed {
		return &GuardrailsBlockedError{Reason: dec.Reason, Message: OutboundBlockMessage(dec.Reason)}
	}

	to := strings.TrimSuffix(toUserID, whatsappJIDSuffix)
	to = strings.TrimPrefix(to, "+")
	if to == "" {
		return fmt.Errorf("whatsapp: empty recipient")
	}

	baseURL := creds.BaseURL
	if baseURL == "" {
		baseURL = whatsappDefaultBaseURL
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", strings.TrimRight(baseURL, "/"), creds.BotID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("whatsapp: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.BotToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp: send returned %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

// ValidateCredentials is a no-op for WhatsApp because the configure
// handler accepts phone_number_id explicitly (it cannot be derived from
// the access token alone — a single Business Account may own multiple
// numbers). The handler passes the user-supplied phone_number_id as
// the botID directly.
//
// Implementing ConfigurableProvider here keeps the configure path
// symmetric with Telegram/Matrix even though we skip the API call.
func (p *WhatsAppProvider) ValidateCredentials(ctx context.Context, baseURL, token string) (string, error) {
	// Configure handler supplies the bot_id; nothing to validate here.
	return "", nil
}
