package codexappgateway

import (
	"encoding/json"
)

// userShellRPCMethod is the JSON-RPC method codex's TUI sends when the
// user presses `!` in the chat composer (see
// codex-rs/tui/src/chatwidget.rs submit_shell_command and
// codex-rs/app-server-protocol/src/protocol/common.rs ThreadShellCommand).
// The handler in the codex subprocess dispatches `Op::RunUserShellCommand`,
// which calls `tokio::process::Command::spawn` against the subprocess's
// own filesystem (codex-rs/core/src/tasks/user_shell.rs:execute_user_shell_command
// → execute_exec_request → spawn_child_async, with SandboxType::None +
// PermissionProfile::Disabled). In a cloud deployment that subprocess
// runs INSIDE the shared codex-app-gateway pod — every workspace's
// codex subprocess shares the pod's filesystem, so `!cat /etc/passwd`
// or `!rm -rf /tmp/*` runs against (and damages) shared state. The
// LLM-driven `shell` tool is unaffected: agentserver writes
// `[features] shell_tool = false` into codex_home/config.toml and routes
// the LLM's tool calls through env-mcp → exec-gateway → the user's
// LOCAL exec-server (see codexhome/codexhome.go:120-123). User-pressed
// `!` mode is the only path that bypasses that routing — block it.
const userShellRPCMethod = "thread/shellCommand"

// userShellBlockedMessage is what surfaces in the user's TUI as the
// JSON-RPC error message when their `!` command is rejected.
const userShellBlockedMessage = "User shell mode (!) is disabled in this cloud deployment because it would execute on the shared codex-app-gateway pod, not your local machine. Use the LLM via a normal turn (the `shell` tool routes to your registered executor)."

// tryBlockUserShellCommand inspects an incoming client→server frame.
// If it's a JSON-RPC request whose method is `thread/shellCommand`
// (codex's `!` user-shell entry point), returns a synthesized JSON-RPC
// error response with `blocked=true`. The caller must write the
// returned bytes back to the user's ws AND drop the original frame so
// the codex subprocess never sees it.
//
// Returns `blocked=false` for anything else (forward unchanged).
//
// Tolerant on shape: anything that doesn't decode or doesn't look like
// a request returns blocked=false and the caller forwards normally.
func tryBlockUserShellCommand(frame []byte) (responseFrame []byte, blocked bool) {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(frame, &msg); err != nil {
		return nil, false
	}
	if msg.Method != userShellRPCMethod {
		return nil, false
	}
	// A notification (no id) wouldn't expect a response; in practice
	// thread/shellCommand always has an id, but defend anyway.
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return nil, true // drop without replying
	}
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      msg.ID,
		"error": map[string]any{
			"code":    -32601, // Method not found — closest standard code.
			"message": userShellBlockedMessage,
		},
	})
	if err != nil {
		// Marshal of trivial map cannot fail in practice; if it
		// somehow does, drop without responding rather than forward.
		return nil, true
	}
	return resp, true
}
