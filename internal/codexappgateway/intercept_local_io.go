package codexappgateway

import (
	"encoding/json"
)

// blockedClientRPCMethods is the set of JSON-RPC methods sent
// client→app-server that, if forwarded to the codex subprocess, would
// touch the codex-app-gateway pod's local filesystem or spawn local
// processes. In a cloud deployment that's the SHARED pod fs/proc:
// per-workspace codex subprocesses run side-by-side in one container,
// so any of these effectively trample on other workspaces.
//
// Each entry is the exact wire `method` name from
// codex-rs/app-server-protocol/src/protocol/common.rs.
//
// Why blocking is necessary instead of using a codex config flag:
//
//   - `[features] shell_tool = false` (already set via
//     codexhome/codexhome.go:120-123) only controls LLM tool exposure.
//     It does NOT gate these TUI→app-server RPCs.
//   - The fs/* protocol comment in common.rs explicitly says it "mirrors"
//     desktop's local-fs model — the design assumes client and app-server
//     are colocated on the user's machine, which is wrong for cloud.
//   - command/exec is documented as running "under the server's sandbox"
//     (i.e. the pod for cloud) and has no config gate.
//   - None of the methods carry an #[experimental] guard, so even
//     legacy clients can invoke them unconditionally.
//
// The LLM-driven path is unaffected: the LLM reaches shell / fs only via
// env-mcp tools, which route through codex-exec-gateway → the user's
// local exec-server. Blocking these direct RPCs at the gateway boundary
// closes the only remaining escape that would land in the pod.
var blockedClientRPCMethods = map[string]struct{}{
	// User-pressed `!` in TUI chat composer (chatwidget.rs:5382 →
	// Op::RunUserShellCommand → spawn_child_async with SandboxType::None).
	"thread/shellCommand": {},

	// Interactive command session (PTY-backed, stream stdin/stdout).
	// More powerful than thread/shellCommand — same blast radius.
	"command/exec":           {},
	"command/exec/write":     {},
	"command/exec/terminate": {},
	"command/exec/resize":    {},

	// Direct local fs access from the client. Each handler calls
	// `self.file_system.<op>(..., /*sandbox*/ None)` against the
	// app-server's local FS (fs_processor.rs).
	"fs/readFile":        {},
	"fs/writeFile":       {},
	"fs/createDirectory": {},
	"fs/remove":          {},
	"fs/copy":            {},
	"fs/readDirectory":   {},
	"fs/getMetadata":     {},
	"fs/watch":           {},
	"fs/unwatch":         {},
}

// blockedRPCMessage is what surfaces in the user's TUI as the JSON-RPC
// error message when a blocked method is rejected.
const blockedRPCMessage = "This RPC is disabled in the cloud deployment because it would access the shared codex-app-gateway pod, not your local machine. Use the LLM-driven shell / read_file tools (they route through your registered executor)."

// tryBlockLocalIORPC inspects an incoming client→server frame. If it's
// a JSON-RPC request whose method is in blockedClientRPCMethods,
// returns a synthesized JSON-RPC error response with `blocked=true`.
// The caller must write the returned bytes back to the user's ws AND
// drop the original frame so the codex subprocess never sees it.
//
// Returns `blocked=false` for anything else (forward unchanged).
//
// Tolerant on shape: anything that doesn't decode or doesn't look like
// a request returns blocked=false and the caller forwards normally.
func tryBlockLocalIORPC(frame []byte) (responseFrame []byte, blocked bool) {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(frame, &msg); err != nil {
		return nil, false
	}
	if _, ok := blockedClientRPCMethods[msg.Method]; !ok {
		return nil, false
	}
	// A notification (no id) wouldn't expect a response; in practice
	// these are all request methods, but defend anyway.
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return nil, true // drop without replying
	}
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      msg.ID,
		"error": map[string]any{
			"code":    -32601, // Method not found — JSON-RPC spec includes "is not available", which fits.
			"message": blockedRPCMessage,
		},
	})
	if err != nil {
		// Marshal of trivial map cannot fail in practice; if it
		// somehow does, drop without responding rather than forward.
		return nil, true
	}
	return resp, true
}
