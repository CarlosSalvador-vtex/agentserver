package codexappgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBlockLocalIORPC_BlocksAndReturnsRPCError pins the contract that
// a blocked RPC (here: thread/shellCommand) is rejected with a
// well-formed JSON-RPC error response that preserves the request id.
// Without this gate, codex's `!` keystroke would spawn a shell in the
// shared codex-app-gateway pod.
func TestBlockLocalIORPC_BlocksAndReturnsRPCError(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":42,"method":"thread/shellCommand","params":{"threadId":"t","command":"whoami"}}`)
	resp, blocked := tryBlockLocalIORPC(in)
	if !blocked {
		t.Fatal("expected blocked=true for thread/shellCommand")
	}
	if resp == nil {
		t.Fatal("expected non-nil response (request had id, must be answered)")
	}
	var got struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response not valid JSON: %v\n%s", err, resp)
	}
	if got.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", got.JSONRPC)
	}
	if got.ID != 42 {
		t.Errorf("id round-trip failed: got %d, want 42", got.ID)
	}
	if got.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, "disabled") {
		t.Errorf("error.message should explain why it's disabled, got %q", got.Error.Message)
	}
}

// TestBlockLocalIORPC_PreservesStringId guards against id-type
// mangling. JSON-RPC ids can be strings or numbers; codex TUI sends
// integers, but the protocol allows strings — we round-trip whichever
// shape came in.
func TestBlockLocalIORPC_PreservesStringId(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":"abc-123","method":"thread/shellCommand","params":{}}`)
	resp, blocked := tryBlockLocalIORPC(in)
	if !blocked {
		t.Fatal("expected blocked=true")
	}
	if !strings.Contains(string(resp), `"id":"abc-123"`) {
		t.Errorf("expected id verbatim in response, got %s", resp)
	}
}

// TestBlockLocalIORPC_NotificationDroppedSilently covers the edge
// case where a blocked method arrives as a notification (no id). We
// still drop it (don't let codex act on it) but can't send an error
// response without an id to address it to.
func TestBlockLocalIORPC_NotificationDroppedSilently(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","method":"thread/shellCommand","params":{}}`)
	resp, blocked := tryBlockLocalIORPC(in)
	if !blocked {
		t.Fatal("expected blocked=true so the frame is dropped")
	}
	if resp != nil {
		t.Errorf("expected nil response (no id to address), got %s", resp)
	}
}

// TestBlockLocalIORPC_AllBlockedMethods enumerates every method in the
// blacklist and asserts it gets rejected. A typo in the wire-format
// string would silently let an RPC through; this pins them.
func TestBlockLocalIORPC_AllBlockedMethods(t *testing.T) {
	// Wire names cross-referenced against
	// codex-rs/app-server-protocol/src/protocol/common.rs. If codex
	// renames any of these, this test will pass (the rename means the
	// old method is gone) but our blacklist will silently miss the new
	// one — periodic re-audit needed.
	wantBlocked := []string{
		"thread/shellCommand",
		"command/exec",
		"command/exec/write",
		"command/exec/terminate",
		"command/exec/resize",
		"fs/readFile",
		"fs/writeFile",
		"fs/createDirectory",
		"fs/remove",
		"fs/copy",
		"fs/readDirectory",
		"fs/getMetadata",
		"fs/watch",
		"fs/unwatch",
	}
	for i, method := range wantBlocked {
		frame := []byte(`{"jsonrpc":"2.0","id":` + itoa(i+1) + `,"method":"` + method + `","params":{}}`)
		if _, blocked := tryBlockLocalIORPC(frame); !blocked {
			t.Errorf("method %q not blocked — blacklist regression", method)
		}
	}
	// Also sanity-check the set has exactly these (catches accidental
	// additions / removals).
	if len(blockedClientRPCMethods) != len(wantBlocked) {
		t.Errorf("blockedClientRPCMethods size = %d, want %d — list drift",
			len(blockedClientRPCMethods), len(wantBlocked))
	}
}

// TestBlockLocalIORPC_PassesThroughOtherRPCs ensures every other
// JSON-RPC method (turn/start, thread/resume, initialize, …) is
// forwarded unchanged. A false-positive here would break every codex
// session.
func TestBlockLocalIORPC_PassesThroughOtherRPCs(t *testing.T) {
	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"turn/start","params":{"threadId":"t"}}`),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"thread/resume","params":{"threadId":"t"}}`),
		[]byte(`{"jsonrpc":"2.0","id":4,"method":"thread/start","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":5,"method":"thread/list","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":6,"method":"skills/list","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":7,"result":{}}`), // response, not a request
		[]byte(`not even json`),
	} {
		if _, blocked := tryBlockLocalIORPC(frame); blocked {
			t.Errorf("false positive: %s", frame)
		}
	}
}

// itoa avoids depending on strconv just for a 1-2 digit number in tests.
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
