package broker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeCodexServer accepts one ws connection and runs `frame` against it.
// frame receives Read/Write helpers and must replay codex behavior.
func fakeCodexServer(t *testing.T, frame func(t *testing.T, ctx context.Context, c *websocket.Conn)) (wsURL string, stop func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		frame(t, r.Context(), c)
	}))
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	return url, srv.Close
}

func readFrame(t *testing.T, ctx context.Context, c *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func writeJSON(t *testing.T, ctx context.Context, c *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestConnInitializeHandshake(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		init := readFrame(t, ctx, c)
		if init["method"] != "initialize" {
			t.Errorf("first frame method=%v want initialize", init["method"])
		}
		// Reply with initialize result.
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"id":      init["id"],
			"result":  map[string]any{"protocolVersion": "2025-06-18"},
		})
		// Expect initialized notification.
		got := readFrame(t, ctx, c)
		if got["method"] != "initialized" {
			t.Errorf("second frame method=%v want initialized", got["method"])
		}
		if _, hasID := got["id"]; hasID {
			t.Errorf("initialized must be a notification (no id), got %v", got)
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialAndHandshake(ctx, url)
	if err != nil {
		t.Fatalf("dialAndHandshake: %v", err)
	}
	defer conn.Close()
}
