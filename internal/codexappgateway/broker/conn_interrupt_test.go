package broker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestConnTurnInterruptOnTimeout(t *testing.T) {
	gotInterrupt := make(chan map[string]any, 1)
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-late"}}})
		// Never send turn/completed; wait for interrupt.
		for {
			f := readFrame(t, ctx, c)
			if f["method"] == "turn/interrupt" {
				gotInterrupt <- f
				return
			}
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	_, err = conn.Turn(ctx, "thr-late", json.RawMessage(`{"input":[]}`), 200*time.Millisecond)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v want *TimeoutError", err)
	}

	select {
	case f := <-gotInterrupt:
		p := f["params"].(map[string]any)
		if p["threadId"] != "thr-late" || p["turnId"] != "trn-late" {
			t.Errorf("interrupt params = %v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not observe turn/interrupt within 3s after timeout")
	}
}

func TestConnTurnFailsOnWSClose(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-x"}}})
		// Close ws mid-turn instead of sending turn/completed.
		c.Close(websocket.StatusInternalError, "simulated crash")
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_, err = conn.Turn(ctx, "thr-x", json.RawMessage(`{"input":[]}`), 5*time.Second)
	if err == nil {
		t.Fatal("expected error on ws close")
	}
	t.Logf("ws-close err = %v", err)
}

// TestConnTurnTimeoutClosesConn pins the self-heal behaviour: a Turn
// timeout marks the conn as closed (via Close), so the Pool will
// dial a fresh one on the next Get() rather than handing back the
// same broken conn. Without this, prod observed "first user message
// in workspace works → conn becomes stuck → every subsequent message
// in that workspace brokerTimeouts forever until CXG is restarted."
func TestConnTurnTimeoutClosesConn(t *testing.T) {
	url, stop := fakeCodexServer(t, func(t *testing.T, ctx context.Context, c *websocket.Conn) {
		replayHandshake(t, ctx, c)
		ts := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "trn-z"}}})
		// Never send turn/completed → caller will brokerTimeout.
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	_, err = conn.Turn(ctx, "thr-z", json.RawMessage(`{"input":[]}`), 200*time.Millisecond)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v want *TimeoutError", err)
	}

	// Give Close() a beat to propagate through readLoop's failAllPending.
	time.Sleep(50 * time.Millisecond)

	// closeErr should now be set; closeErrOr returns the underlying read
	// error (or our "connection closed" fallback). Use it as a proxy for
	// "conn is dead".
	got := conn.closeErrOr(nil)
	if got == nil {
		t.Error("conn.closeErr unset after Turn timeout — Pool would hand back this conn")
	}
}
