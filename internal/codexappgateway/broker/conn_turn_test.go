package broker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// replayHandshake reads initialize + initialized frames and replies to
// initialize so Dial completes. Returns once both are seen.
func replayHandshake(t *testing.T, ctx context.Context, c *websocket.Conn) {
	t.Helper()
	init := readFrame(t, ctx, c)
	writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{}})
	got := readFrame(t, ctx, c)
	if got["method"] != "initialized" {
		t.Fatalf("expected initialized, got %v", got)
	}
}

func TestConnTurnSuccessful(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)

		// Expect turn/start call.
		ts := readFrame(t, ctx, c)
		if ts["method"] != "turn/start" {
			t.Fatalf("want turn/start, got %v", ts["method"])
		}
		params := ts["params"].(map[string]any)
		if params["threadId"] != "thr-abc" {
			t.Errorf("threadId=%v", params["threadId"])
		}
		// Reply with turn/start response (turn id).
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"id":      ts["id"],
			"result":  map[string]any{"turn": map[string]any{"id": "trn-001"}},
		})

		// Stream notifications then turn/completed.
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/started",
			"params":  map[string]any{"threadId": "thr-abc", "turn": map[string]any{"id": "trn-001"}},
		})
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params": map[string]any{
				"threadId": "thr-abc",
				"turn": map[string]any{
					"id":          "trn-001",
					"status":      "completed",
					"itemsView":   "full",
					"items":       []any{map[string]any{"type": "agentMessage", "id": "msg1", "text": "hello"}},
					"error":       nil,
					"startedAt":   1,
					"completedAt": 2,
					"durationMs":  1000,
				},
			},
		})
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	rawTurn, err := conn.Turn(ctx, "thr-abc", json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`), 5*time.Second)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(rawTurn, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "completed" {
		t.Errorf("status=%v", got["status"])
	}
	items := got["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["text"] != "hello" {
		t.Errorf("items=%v", items)
	}
}
