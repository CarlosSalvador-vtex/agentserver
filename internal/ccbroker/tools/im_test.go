package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendMessage_PostsToAgentserver(t *testing.T) {
	var captured map[string]any
	var gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("X-Internal-Secret")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"sent"}`))
	}))
	defer srv.Close()

	tctx := &Context{
		IMChannelID:       "ch1",
		IMUserID:          "u1",
		AgentserverURL:    srv.URL,
		InternalAPISecret: "topsecret",
		HTTP:              http.DefaultClient,
	}
	tool := byName(imTools(tctx), "send_message")
	r, _ := tool.Handler(context.Background(),
		json.RawMessage(`{"text":"hello","sender":"bot"}`))
	if r.IsError {
		t.Fatalf("IsError: %v", r.Content)
	}
	if captured["channel_id"] != "ch1" || captured["user_id"] != "u1" || captured["kind"] != "text" {
		t.Errorf("unexpected body: %v", captured)
	}
	if gotSecret != "topsecret" {
		t.Errorf("X-Internal-Secret=%q want topsecret", gotSecret)
	}
}

func TestSendMessage_NoIMContext(t *testing.T) {
	tctx := &Context{HTTP: http.DefaultClient} // no IMChannelID/IMUserID
	tool := byName(imTools(tctx), "send_message")
	r, _ := tool.Handler(context.Background(),
		json.RawMessage(`{"text":"hello"}`))
	if !r.IsError {
		t.Errorf("expected IsError when not invoked from an IM turn")
	}
}
