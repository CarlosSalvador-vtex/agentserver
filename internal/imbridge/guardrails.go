package imbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultInboundRedirectMessage is sent to the user when an inbound message is
// blocked as out-of-scope (WhatsApp direct-send path).
const DefaultInboundRedirectMessage = "I can only help with topics related to this assistant's scope. Please ask something within that scope."

// DefaultOutboundBlockMessage is sent when an outbound reply is blocked (PII or out-of-scope).
const DefaultOutboundBlockMessage = "I can't send that reply because it may contain sensitive information or content outside my scope."

// GuardrailsBlockedError is returned from Provider.Send when outbound guardrails block the message.
type GuardrailsBlockedError struct {
	Message string
	Reason  string // "pii", "out_of_scope"
}

func (e *GuardrailsBlockedError) Error() string { return e.Message }

var (
	runtimeLLMProxyURL      string
	runtimeTokenProvider    WorkspaceTokenProvider
)

// SetGuardrailsRuntime configures package-level llmproxy URL and workspace token minting (imbridge service startup).
func SetGuardrailsRuntime(llmProxyURL string, tokenProvider WorkspaceTokenProvider) {
	runtimeLLMProxyURL = strings.TrimRight(strings.TrimSpace(llmProxyURL), "/")
	runtimeTokenProvider = tokenProvider
}

// GuardrailDecision is the result of a guardrails check.
type GuardrailDecision struct {
	Allowed bool
	Reason  string // "in_scope", "out_of_scope", "pii", "infra_allow"
}

// GuardrailsChecker validates inbound and outbound IM text when a channel has a scope.
type GuardrailsChecker interface {
	CheckInbound(ctx context.Context, text string) GuardrailDecision
	CheckOutbound(ctx context.Context, text string) GuardrailDecision
}

// NoopGuardrails allows all messages (default for channels without scope_description).
type NoopGuardrails struct{}

func (NoopGuardrails) CheckInbound(context.Context, string) GuardrailDecision {
	return GuardrailDecision{Allowed: true, Reason: "noop"}
}

func (NoopGuardrails) CheckOutbound(context.Context, string) GuardrailDecision {
	return GuardrailDecision{Allowed: true, Reason: "noop"}
}

// WorkspaceTokenProvider mints workspace-scoped LLM proxy API keys.
type WorkspaceTokenProvider interface {
	WorkspaceProxyToken(ctx context.Context, workspaceID string) (string, error)
}

// DBWorkspaceTokenProvider implements WorkspaceTokenProvider using a DB token lookup.
type DBWorkspaceTokenProvider struct {
	GetToken func(ctx context.Context, workspaceID string) (string, error)
}

func (d DBWorkspaceTokenProvider) WorkspaceProxyToken(ctx context.Context, workspaceID string) (string, error) {
	return d.GetToken(ctx, workspaceID)
}

// ScopeGuardrails enforces scope_description via llmproxy classification and CPF regex on outbound.
type ScopeGuardrails struct {
	ScopeDescription string
	WorkspaceID      string
	LLMProxyURL      string
	TokenProvider    WorkspaceTokenProvider
	Model            string

	classifyCache sync.Map // map[string]cachedClassify
}

type cachedClassify struct {
	inScope   bool
	expiresAt time.Time
}

var brazilCPFRegex = regexp.MustCompile(`\b\d{3}\.?\d{3}\.?\d{3}-?\d{2}\b`)

type guardrailsContextKey struct{}

// ContextWithGuardrailsChecker attaches a checker for Provider.Send and similar paths.
func ContextWithGuardrailsChecker(ctx context.Context, c GuardrailsChecker) context.Context {
	if c == nil {
		c = NoopGuardrails{}
	}
	return context.WithValue(ctx, guardrailsContextKey{}, c)
}

// GuardrailsCheckerFromContext returns the checker from context, or NoopGuardrails.
func GuardrailsCheckerFromContext(ctx context.Context) GuardrailsChecker {
	if c, ok := ctx.Value(guardrailsContextKey{}).(GuardrailsChecker); ok && c != nil {
		return c
	}
	return NoopGuardrails{}
}

// CheckerForCredentials builds a guardrails checker from channel scope fields.
func CheckerForCredentials(creds *Credentials, llmProxyURL string, tokenProvider WorkspaceTokenProvider) GuardrailsChecker {
	if creds == nil {
		return NoopGuardrails{}
	}
	return GuardrailsForChannel(&ChannelScopeInfo{
		WorkspaceID:      creds.WorkspaceID,
		ScopeDescription: creds.ScopeDescription,
	}, llmProxyURL, tokenProvider)
}

// EffectiveOutboundGuardrails uses context checker when set (non-noop), else credentials + runtime config.
func EffectiveOutboundGuardrails(ctx context.Context, creds *Credentials, llmProxyURL string, tokenProvider WorkspaceTokenProvider) GuardrailsChecker {
	if c := GuardrailsCheckerFromContext(ctx); c != nil {
		if _, noop := c.(NoopGuardrails); !noop {
			return c
		}
	}
	if llmProxyURL == "" {
		llmProxyURL = runtimeLLMProxyURL
	}
	if tokenProvider == nil {
		tokenProvider = runtimeTokenProvider
	}
	return CheckerForCredentials(creds, llmProxyURL, tokenProvider)
}

// OutboundBlockMessage returns the user-visible text for a blocked outbound send.
func OutboundBlockMessage(reason string) string {
	_ = reason
	return DefaultOutboundBlockMessage
}

// GuardrailsForChannel returns NoopGuardrails when scope is empty or llmproxy URL unset.
func GuardrailsForChannel(ch *ChannelScopeInfo, llmProxyURL string, tokenProvider WorkspaceTokenProvider) GuardrailsChecker {
	if ch == nil || strings.TrimSpace(ch.ScopeDescription) == "" || strings.TrimSpace(llmProxyURL) == "" {
		return NoopGuardrails{}
	}
	model := os.Getenv("IM_GUARDRAILS_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &ScopeGuardrails{
		ScopeDescription: strings.TrimSpace(ch.ScopeDescription),
		WorkspaceID:      ch.WorkspaceID,
		LLMProxyURL:      strings.TrimRight(strings.TrimSpace(llmProxyURL), "/"),
		TokenProvider:    tokenProvider,
		Model:            model,
	}
}

// ChannelScopeInfo carries fields needed to build a guardrails checker for a channel.
type ChannelScopeInfo struct {
	WorkspaceID       string
	ScopeDescription  string
}

func (s *ScopeGuardrails) CheckInbound(ctx context.Context, text string) GuardrailDecision {
	if strings.TrimSpace(text) == "" {
		return GuardrailDecision{Allowed: true, Reason: "empty"}
	}
	inScope, err := s.classify(ctx, text)
	if err != nil {
		return GuardrailDecision{Allowed: true, Reason: "infra_allow"}
	}
	if !inScope {
		return GuardrailDecision{Allowed: false, Reason: "out_of_scope"}
	}
	return GuardrailDecision{Allowed: true, Reason: "in_scope"}
}

func (s *ScopeGuardrails) CheckOutbound(ctx context.Context, text string) GuardrailDecision {
	if strings.TrimSpace(text) == "" {
		return GuardrailDecision{Allowed: true, Reason: "empty"}
	}
	if brazilCPFRegex.MatchString(text) {
		return GuardrailDecision{Allowed: false, Reason: "pii"}
	}
	inScope, err := s.classify(ctx, text)
	if err != nil {
		return GuardrailDecision{Allowed: true, Reason: "infra_allow"}
	}
	if !inScope {
		return GuardrailDecision{Allowed: false, Reason: "out_of_scope"}
	}
	return GuardrailDecision{Allowed: true, Reason: "in_scope"}
}

func (s *ScopeGuardrails) classify(ctx context.Context, text string) (bool, error) {
	if s.TokenProvider == nil || s.WorkspaceID == "" || s.LLMProxyURL == "" {
		return true, nil
	}
	key := classifyCacheKey(s.WorkspaceID, s.ScopeDescription, text)
	if v, ok := s.classifyCache.Load(key); ok {
		c := v.(cachedClassify)
		if time.Now().Before(c.expiresAt) {
			return c.inScope, nil
		}
		s.classifyCache.Delete(key)
	}
	inScope, err := classifyScopeLLM(ctx, s.LLMProxyURL, s.TokenProvider, s.WorkspaceID, s.ScopeDescription, text, s.Model)
	if err != nil {
		return true, err
	}
	s.classifyCache.Store(key, cachedClassify{inScope: inScope, expiresAt: time.Now().Add(60 * time.Second)})
	return inScope, nil
}

func classifyCacheKey(workspaceID, scope, text string) string {
	norm := strings.ToLower(strings.TrimSpace(text))
	h := sha256.Sum256([]byte(norm))
	prefix := hex.EncodeToString(h[:4])
	scopeHash := sha256.Sum256([]byte(scope))
	return workspaceID + ":" + hex.EncodeToString(scopeHash[:8]) + ":" + prefix
}

func classifyScopeLLM(ctx context.Context, baseURL string, tokens WorkspaceTokenProvider, workspaceID, scopeDesc, userText, model string) (bool, error) {
	token, err := tokens.WorkspaceProxyToken(ctx, workspaceID)
	if err != nil || token == "" {
		return true, fmt.Errorf("workspace token: %w", err)
	}

	system := fmt.Sprintf(`You classify whether a user message is within the assistant's scope.

Scope description:
%s

Reply with JSON only: {"in_scope": true} or {"in_scope": false}
Consider the message in-scope if it reasonably relates to the scope, including greetings and clarifying questions about the assistant's role.`, scopeDesc)

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 32,
		"system":     system,
		"messages": []map[string]string{
			{"role": "user", "content": userText},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return true, err
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return true, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", token)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return true, err
	}
	if resp.StatusCode >= 400 {
		return true, fmt.Errorf("llmproxy status %d: %s", resp.StatusCode, string(respBody))
	}

	var envelope struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return true, err
	}
	var combined strings.Builder
	for _, block := range envelope.Content {
		combined.WriteString(block.Text)
	}
	parsed := parseInScopeJSON(combined.String())
	if parsed == nil {
		// Fail open if model returned non-JSON
		return true, nil
	}
	return *parsed, nil
}

func parseInScopeJSON(s string) *bool {
	s = strings.TrimSpace(s)
	// Extract first JSON object if wrapped in prose
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	var v struct {
		InScope bool `json:"in_scope"`
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return &v.InScope
}
