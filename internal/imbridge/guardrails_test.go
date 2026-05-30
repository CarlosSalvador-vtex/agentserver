package imbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopGuardrailsAlwaysAllows(t *testing.T) {
	var n NoopGuardrails
	if !n.CheckInbound(context.Background(), "anything").Allowed {
		t.Fatal("inbound should allow")
	}
	if !n.CheckOutbound(context.Background(), "anything").Allowed {
		t.Fatal("outbound should allow")
	}
}

func TestGuardrailsForChannelEmptyScopeIsNoop(t *testing.T) {
	c := GuardrailsForChannel(&ChannelScopeInfo{WorkspaceID: "ws1", ScopeDescription: ""}, "http://127.0.0.1:9", nil)
	if _, ok := c.(NoopGuardrails); !ok {
		t.Fatalf("expected NoopGuardrails, got %T", c)
	}
}

func TestGuardrailsForChannelEmptyProxyURLIsNoop(t *testing.T) {
	c := GuardrailsForChannel(&ChannelScopeInfo{WorkspaceID: "ws1", ScopeDescription: "billing bot"}, "", nil)
	if _, ok := c.(NoopGuardrails); !ok {
		t.Fatalf("expected NoopGuardrails when llmproxy URL empty, got %T", c)
	}
}

func TestScopeGuardrailsOutboundBlocksCPF(t *testing.T) {
	s := &ScopeGuardrails{ScopeDescription: "billing", WorkspaceID: "ws1", LLMProxyURL: "http://unused"}
	dec := s.CheckOutbound(context.Background(), "seu CPF é 123.456.789-00")
	if dec.Allowed {
		t.Fatal("expected CPF to be blocked")
	}
	if dec.Reason != "pii" {
		t.Fatalf("reason=%q", dec.Reason)
	}
}

func TestScopeGuardrailsInboundFailOpenOnLLMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := &ScopeGuardrails{
		ScopeDescription: "billing support only",
		WorkspaceID:      "ws1",
		LLMProxyURL:      srv.URL,
		TokenProvider: DBWorkspaceTokenProvider{
			GetToken: func(context.Context, string) (string, error) { return "tok", nil },
		},
		Model: "claude-sonnet-4-6",
	}
	dec := s.CheckInbound(context.Background(), "qual é a capital da frança")
	if !dec.Allowed {
		t.Fatal("expected fail-open allow on LLM error")
	}
	if dec.Reason != "infra_allow" {
		t.Fatalf("reason=%q", dec.Reason)
	}
}

func TestScopeGuardrailsInboundBlocksOutOfScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"in_scope\": false}"}]}`))
	}))
	defer srv.Close()

	s := &ScopeGuardrails{
		ScopeDescription: "debt collection only",
		WorkspaceID:      "ws1",
		LLMProxyURL:      srv.URL,
		TokenProvider: DBWorkspaceTokenProvider{
			GetToken: func(context.Context, string) (string, error) { return "tok", nil },
		},
		Model: "claude-sonnet-4-6",
	}
	dec := s.CheckInbound(context.Background(), "previsão do tempo amanhã")
	if dec.Allowed {
		t.Fatal("expected out of scope block")
	}
	if dec.Reason != "out_of_scope" {
		t.Fatalf("reason=%q", dec.Reason)
	}
}

func TestParseInScopeJSON(t *testing.T) {
	trueVal := parseInScopeJSON(`{"in_scope":true}`)
	if trueVal == nil || !*trueVal {
		t.Fatal("expected true")
	}
	falseVal := parseInScopeJSON(`Here: {"in_scope": false}`)
	if falseVal == nil || *falseVal {
		t.Fatal("expected false")
	}
}
