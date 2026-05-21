package broker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestConnAutoApprovesRequestUserInput(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		replayThreadResume(t, ctx, c)

		// turn/start → reply
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-1"}}})

		// Server sends an approval request mid-turn.
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"id":      999,
			"method":  "item/tool/requestUserInput",
			"params":  map[string]any{"toolName": "read_file"},
		})
		// Expect the broker to reply with {"answers":{}} (the schema-correct payload).
		approval := readFrame(t, ctx, c)
		if approval["id"] != float64(999) {
			t.Errorf("approval reply id=%v want 999", approval["id"])
		}
		result, ok := approval["result"].(map[string]any)
		if !ok {
			t.Fatalf("approval reply missing result object: %v", approval)
		}
		answers, ok := result["answers"].(map[string]any)
		if !ok {
			t.Errorf("expected result.answers map, got %v", result)
		}
		if len(answers) != 0 {
			t.Errorf("expected empty answers map, got %v", answers)
		}

		// Finish the turn so Turn() returns.
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params":  map[string]any{"threadId": "thr-1", "turn": map[string]any{"id": "trn-1", "status": "completed", "items": []any{}, "itemsView": "full", "error": nil}},
		})
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Turn(ctx, "thr-1", json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`), 30*time.Second); err != nil {
		t.Fatalf("Turn: %v", err)
	}
}

func TestConnAutoApprovesPermissionsWithEmptyProfile(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		replayThreadResume(t, ctx, c)
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-2"}}})

		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": 555,
			"method": "item/permissions/requestApproval",
			"params": map[string]any{},
		})
		reply := readFrame(t, ctx, c)
		result, ok := reply["result"].(map[string]any)
		if !ok {
			t.Fatalf("perms reply missing result object: %v", reply)
		}
		perms, ok := result["permissions"].(map[string]any)
		if !ok {
			t.Errorf("expected result.permissions object, got %v", result)
		}
		if len(perms) != 0 {
			t.Errorf("expected empty permissions object, got %v", perms)
		}

		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params":  map[string]any{"threadId": "thr-2", "turn": map[string]any{"id": "trn-2", "status": "completed", "items": []any{}, "itemsView": "full", "error": nil}},
		})
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Turn(ctx, "thr-2", json.RawMessage(`{"input":[]}`), 30*time.Second); err != nil {
		t.Fatalf("Turn: %v", err)
	}
}

// TestConnRepliesMethodNotFoundForUnknownServerRequest pins the
// defensive behaviour that unblocks the prod "broker stuck for whole
// workspace" wedge: any id-bearing server request whose method we
// don't recognise gets a JSON-RPC method-not-found reply, so codex
// doesn't sit waiting for a response that would never arrive.
func TestConnRepliesMethodNotFoundForUnknownServerRequest(t *testing.T) {
	gotReply := make(chan map[string]any, 1)
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		replayThreadResume(t, ctx, c)
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-u"}}})
		// Send a made-up server request mid-turn.
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": 4242,
			"method": "experimental/futureThing",
			"params": map[string]any{"foo": "bar"},
		})
		// Expect the broker to reply with -32601, then complete the turn.
		reply := readFrame(t, ctx, c)
		gotReply <- reply
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0",
			"method":  "turn/completed",
			"params":  map[string]any{"threadId": "thr-u", "turn": map[string]any{"id": "trn-u", "status": "completed", "items": []any{}, "itemsView": "full", "error": nil}},
		})
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Turn(ctx, "thr-u", json.RawMessage(`{"input":[]}`), 30*time.Second); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	select {
	case reply := <-gotReply:
		if reply["id"] != float64(4242) {
			t.Errorf("reply id=%v want 4242", reply["id"])
		}
		e, ok := reply["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected error in reply, got %v", reply)
		}
		if e["code"] != float64(-32601) {
			t.Errorf("error.code=%v want -32601", e["code"])
		}
		if msg, _ := e["message"].(string); msg == "" {
			t.Errorf("expected non-empty error message, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not observe method-not-found reply")
	}
}
