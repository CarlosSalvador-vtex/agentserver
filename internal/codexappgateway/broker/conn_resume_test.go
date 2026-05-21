package broker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestConnTurn_AbortsIfThreadResumeErrors locks in that an error reply to
// the thread/resume preamble surfaces from Turn without ever firing
// turn/start. Avoids leaking RPC IDs and turn state when the listener
// can't be established.
func TestConnTurn_AbortsIfThreadResumeErrors(t *testing.T) {
	sawTurnStart := make(chan struct{}, 1)
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)

		// thread/resume → reply with error.
		f := readFrame(t, ctx, c)
		if f["method"] != "thread/resume" {
			t.Fatalf("first frame method = %v, want thread/resume", f["method"])
		}
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": f["id"],
			"error": map[string]any{"code": -32602, "message": "thread closing"},
		})

		// Any further frame would be turn/start — flag it as a failure.
		for {
			next, err := readNoFatal(ctx, c)
			if err != nil {
				return
			}
			if next["method"] == "turn/start" {
				sawTurnStart <- struct{}{}
				return
			}
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	_, err = conn.Turn(ctx, "thr-err",
		json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`),
		30*time.Second)
	if err == nil {
		t.Fatal("expected error from Turn when thread/resume fails")
	}

	// Give the fake server a tick to see any erroneous turn/start that
	// might have slipped through despite the resume error.
	select {
	case <-sawTurnStart:
		t.Fatal("Turn sent turn/start despite thread/resume error — listener-attach guard failed")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestConnTurn_SendsThreadResumeBeforeTurnStart locks in the contract that
// Conn.Turn fires thread/resume before turn/start so codex auto-attaches
// the per-thread listener task on the current ws connection. Without this,
// turn/start acks but events have no consumer (see codex
// turn_processor.rs:turn_start_inner skipping ensure_conversation_listener)
// — empirically observed as 5-minute brokerTimeout with items=0.
func TestConnTurn_SendsThreadResumeBeforeTurnStart(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)

		// First post-handshake frame MUST be thread/resume.
		first := readFrame(t, ctx, c)
		if first["method"] != "thread/resume" {
			t.Fatalf("first frame method = %v, want thread/resume", first["method"])
		}
		// codex's ThreadResumeParams uses #[serde(rename_all = "camelCase")] —
		// the wire field is threadId. Asserting the exact key here is
		// load-bearing: a snake_case typo would slip past Go-on-Go tests
		// (fake server roundtrips whatever the client sends) yet fail
		// against real codex with "missing field threadId".
		params, _ := first["params"].(map[string]any)
		if params["threadId"] != "thr-listener" {
			t.Errorf("thread/resume threadId = %v, want thr-listener", params["threadId"])
		}
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": first["id"], "result": map[string]any{},
		})

		// Then turn/start.
		ts := readFrame(t, ctx, c)
		if ts["method"] != "turn/start" {
			t.Fatalf("second frame method = %v, want turn/start", ts["method"])
		}
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": ts["id"],
			"result": map[string]any{"turn": map[string]any{"id": "trn-listener"}},
		})
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "method": "turn/completed",
			"params": map[string]any{
				"threadId": "thr-listener",
				"turn": map[string]any{
					"id": "trn-listener", "status": "completed",
					"items": []any{}, "itemsView": "full", "error": nil,
				},
			},
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

	if _, err := conn.Turn(ctx, "thr-listener",
		json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`),
		30*time.Second); err != nil {
		t.Fatalf("Turn: %v", err)
	}
}
