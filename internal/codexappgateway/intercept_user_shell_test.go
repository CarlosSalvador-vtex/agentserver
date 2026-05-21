package codexappgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBlockUserShellCommand_BlocksAndReturnsRPCError pins the contract
// that any incoming `thread/shellCommand` request is rejected with a
// well-formed JSON-RPC error response that preserves the request id.
// Without this gate the user's `!` keystroke would spawn a shell in
// the shared codex-app-gateway pod.
func TestBlockUserShellCommand_BlocksAndReturnsRPCError(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":42,"method":"thread/shellCommand","params":{"threadId":"t","command":"whoami"}}`)
	resp, blocked := tryBlockUserShellCommand(in)
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

// TestBlockUserShellCommand_PreservesStringId guards against id-type
// mangling. JSON-RPC ids can be strings or numbers; codex TUI sends
// integers, but the protocol allows strings — we round-trip whichever
// shape came in.
func TestBlockUserShellCommand_PreservesStringId(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","id":"abc-123","method":"thread/shellCommand","params":{}}`)
	resp, blocked := tryBlockUserShellCommand(in)
	if !blocked {
		t.Fatal("expected blocked=true")
	}
	if !strings.Contains(string(resp), `"id":"abc-123"`) {
		t.Errorf("expected id verbatim in response, got %s", resp)
	}
}

// TestBlockUserShellCommand_NotificationDroppedSilently covers the
// edge case where a `thread/shellCommand` notification (no id) somehow
// arrives. We still drop it (don't let codex spawn the shell) but
// can't send an error response without an id to address it to.
func TestBlockUserShellCommand_NotificationDroppedSilently(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","method":"thread/shellCommand","params":{}}`)
	resp, blocked := tryBlockUserShellCommand(in)
	if !blocked {
		t.Fatal("expected blocked=true so the frame is dropped")
	}
	if resp != nil {
		t.Errorf("expected nil response (no id to address), got %s", resp)
	}
}

// TestBlockUserShellCommand_PassesThroughOtherRPCs ensures every other
// JSON-RPC method (turn/start, thread/resume, initialize, …) is
// forwarded unchanged. A false-positive here would break every codex
// session.
func TestBlockUserShellCommand_PassesThroughOtherRPCs(t *testing.T) {
	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"turn/start","params":{"threadId":"t"}}`),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"thread/resume","params":{"threadId":"t"}}`),
		[]byte(`{"jsonrpc":"2.0","id":4,"method":"thread/start","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
		[]byte(`{"jsonrpc":"2.0","id":5,"result":{}}`), // response, not a request
		[]byte(`not even json`),
	} {
		if _, blocked := tryBlockUserShellCommand(frame); blocked {
			t.Errorf("false positive: %s", frame)
		}
	}
}
