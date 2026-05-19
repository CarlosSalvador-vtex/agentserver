package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// captureSender records what handler POSTed to /api/internal/imbridge/send.
type capturedSend struct {
	channelID string
	toUser    string
	text      string
}

func newCapturingImbridge(t *testing.T) (url string, sends *atomic.Value /* []*capturedSend */, stop func()) {
	t.Helper()
	var stored atomic.Value
	stored.Store([]*capturedSend{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChannelID string `json:"channel_id"`
			ToUserID  string `json:"to_user_id"`
			Text      string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		cur := stored.Load().([]*capturedSend)
		stored.Store(append(cur, &capturedSend{channelID: body.ChannelID, toUser: body.ToUserID, text: body.Text}))
		w.WriteHeader(200)
	}))
	return srv.URL, &stored, srv.Close
}

// fakeSessionStore implements the sessionStore interface (defined in
// codex_im_inbound.go) by routing through caller-supplied closures.
type fakeSessionStore struct {
	get func(ctx context.Context, workspaceID, externalID string) (sessionView, error)
	set func(ctx context.Context, sessionID string, threadID *string) error
}

func (f *fakeSessionStore) GetSessionByExternalID(ctx context.Context, workspaceID, externalID string) (sessionView, error) {
	return f.get(ctx, workspaceID, externalID)
}

func (f *fakeSessionStore) SetSessionCodexThreadID(ctx context.Context, sessionID string, threadID *string) error {
	return f.set(ctx, sessionID, threadID)
}

// fakeCodexClient lets us inject CXG responses.
type fakeCodexClient struct {
	resp *CodexTurnResponse
	err  error
}

func (f *fakeCodexClient) RunTurn(_ context.Context, _ CodexTurnRequest) (*CodexTurnResponse, error) {
	return f.resp, f.err
}

func TestCodexInboundHappyPath(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-new",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"completed","items":[{"type":"agentMessage","id":"m1","text":"hello"}],"itemsView":"full","error":null}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: nil}, nil
			},
			set: func(_ context.Context, sessionID string, tid *string) error {
				if sessionID != "sess-1" || tid == nil || *tid != "thr-new" {
					t.Errorf("set called with sessionID=%s tid=%v", sessionID, tid)
				}
				return nil
			},
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()

	body := map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_a",
		"text":           "hi",
	}
	r := newCodexInboundRequest(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", w.Code)
	}
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	captured := sends.Load().([]*capturedSend)[0]
	if captured.text != "hello" {
		t.Errorf("send text=%q want hello", captured.text)
	}
	if captured.toUser != "wxid_a" {
		t.Errorf("send to=%q", captured.toUser)
	}
}

func TestCodexInboundFailedWithUsageLimit(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-x",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"failed","items":[],"itemsView":"full","error":{"message":"quota","codexErrorInfo":"usageLimitExceeded","additionalDetails":null}}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: strPtr("thr-x")}, nil
			},
			set: func(context.Context, string, *string) error { return nil },
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "配额") {
		t.Errorf("text=%q want quota message", got.text)
	}
}

func TestCodexInboundContextWindowClearsThread(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	var cleared int32
	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-old",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"failed","items":[],"itemsView":"full","error":{"message":"too long","codexErrorInfo":"contextWindowExceeded","additionalDetails":null}}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: strPtr("thr-old")}, nil
			},
			set: func(_ context.Context, _ string, tid *string) error {
				if tid != nil {
					t.Errorf("want clear (nil), got %v", *tid)
				}
				atomic.AddInt32(&cleared, 1)
				return nil
			},
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return atomic.LoadInt32(&cleared) > 0 && len(sends.Load().([]*capturedSend)) == 1 })
	if !strings.Contains(sends.Load().([]*capturedSend)[0].text, "上下文") {
		t.Errorf("want context-window message")
	}
}

func TestCodexInboundTransportError(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID:  "thr-x",
				Transport: &CodexTransportError{Code: "brokerTimeout", Message: "..."},
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1"}, nil
			},
			set: func(context.Context, string, *string) error { return nil },
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	if !strings.Contains(sends.Load().([]*capturedSend)[0].text, "超时") {
		t.Errorf("want timeout message")
	}
}

// helpers

func newCodexInboundRequest(body map[string]any) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/internal/imbridge/codex/turn", bytes.NewReader(b))
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never satisfied")
}

func strPtr(s string) *string { return &s }
