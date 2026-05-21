package broker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestConnTurn_SkipsResumeOnSubsequentTurnSameThread locks in the
// per-conn attached-set optimization: after the first Turn() on a
// thread has thread/resume'd it (or StartThread auto-registered it),
// subsequent Turn() calls on the same thread on the same Conn MUST
// NOT re-issue thread/resume. The listener is already wired for the
// connection lifetime; re-issuing is redundant and would 500 the
// first message of fresh threads against codex (rollout not flushed
// yet, see thread_processor.rs:3589).
func TestConnTurn_SkipsResumeOnSubsequentTurnSameThread(t *testing.T) {
	resumeCount := 0
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		for {
			f, err := readNoFatal(ctx, c)
			if err != nil {
				return
			}
			switch f["method"] {
			case "thread/resume":
				resumeCount++
				writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": f["id"], "result": map[string]any{}})
			case "turn/start":
				writeJSON(t, ctx, c, map[string]any{
					"jsonrpc": "2.0", "id": f["id"],
					"result": map[string]any{"turn": map[string]any{"id": "trn-skip"}},
				})
				writeJSON(t, ctx, c, map[string]any{
					"jsonrpc": "2.0", "method": "turn/completed",
					"params": map[string]any{"threadId": "thr-skip", "turn": map[string]any{
						"id": "trn-skip", "status": "completed",
						"items": []any{}, "itemsView": "full", "error": nil,
					}},
				})
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

	for i := 0; i < 3; i++ {
		if _, err := conn.Turn(ctx, "thr-skip",
			json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`),
			30*time.Second); err != nil {
			t.Fatalf("Turn iter %d: %v", i, err)
		}
	}
	// First Turn attaches; iter 1 and 2 must not re-attach.
	if resumeCount != 1 {
		t.Errorf("thread/resume sent %d times, want exactly 1 across 3 turns", resumeCount)
	}
}

// TestStartThread_RegistersListenerAttachment locks in that the
// thread id returned by StartThread is treated as already-attached by
// subsequent Turn() calls. codex's thread_created broadcast wired the
// listener for this connection at thread/start time, so an explicit
// thread/resume is both unnecessary AND broken (fires "no rollout
// found" before the rollout has flushed).
func TestStartThread_RegistersListenerAttachment(t *testing.T) {
	sawResume := false
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		// thread/start reply.
		ts := readFrame(t, ctx, c)
		if ts["method"] != "thread/start" {
			t.Fatalf("first frame = %v, want thread/start", ts["method"])
		}
		writeJSON(t, ctx, c, map[string]any{
			"jsonrpc": "2.0", "id": ts["id"],
			"result": map[string]any{
				"thread":         map[string]any{"id": "thr-fresh", "sessionId": "s", "createdAt": 0, "updatedAt": 0},
				"model":          "gpt-x",
				"modelProvider":  "openai",
				"serviceTier":    nil,
				"cwd":            "/tmp/codex",
				"approvalPolicy": "onRequest",
			},
		})
		// Subsequent turn — must be turn/start directly, not thread/resume.
		for {
			f, err := readNoFatal(ctx, c)
			if err != nil {
				return
			}
			if f["method"] == "thread/resume" {
				sawResume = true
				writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": f["id"], "result": map[string]any{}})
				continue
			}
			if f["method"] == "turn/start" {
				writeJSON(t, ctx, c, map[string]any{
					"jsonrpc": "2.0", "id": f["id"],
					"result": map[string]any{"turn": map[string]any{"id": "trn-fresh"}},
				})
				writeJSON(t, ctx, c, map[string]any{
					"jsonrpc": "2.0", "method": "turn/completed",
					"params": map[string]any{"threadId": "thr-fresh", "turn": map[string]any{
						"id": "trn-fresh", "status": "completed",
						"items": []any{}, "itemsView": "full", "error": nil,
					}},
				})
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

	tid, err := conn.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	if _, err := conn.Turn(ctx, tid,
		json.RawMessage(`{"input":[{"type":"text","text":"hi"}]}`),
		30*time.Second); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if sawResume {
		t.Error("Turn issued thread/resume after StartThread — broadcast-attached listener was not recorded")
	}
}

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
