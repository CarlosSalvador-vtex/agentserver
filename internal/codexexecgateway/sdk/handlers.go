package sdk

import (
	"net/http"
)

// toolDesc is the per-tool entry in envs/list responses. The SDK uses
// these to populate Env.tools. No JSON schema — server validates tool
// args; SDK trusts.
type toolDesc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// coreTools returns the fixed list of tools the SDK knows about. Kept
// hardcoded so envs/list doesn't depend on the tool registry being
// populated (which only matters for tool/call).
func coreTools() []toolDesc {
	return []toolDesc{
		{Name: "shell", Kind: "core", Description: "Run a command synchronously."},
		{Name: "read_file", Kind: "core", Description: "Read a file by path."},
		{Name: "write_file", Kind: "core", Description: "Write a file by path."},
		{Name: "apply_patch", Kind: "core", Description: "Apply a unified-diff patch."},
		{Name: "copy_path", Kind: "core", Description: "Upload or download a file."},
		{Name: "exec_command", Kind: "core", Description: "Start a long-running process (returns session_id)."},
	}
}

type envEntry struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	IsDefault bool       `json:"is_default"`
	Tools     []toolDesc `json:"tools"`
	LastSeen  string     `json:"last_seen,omitempty"`
}

func (s *Server) handleEnvsList(w http.ResponseWriter, r *http.Request) {
	wsID := workspaceFromCtx(r.Context())
	connected, err := s.Registry.Connected(r.Context(), wsID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	envs := make([]envEntry, 0, len(connected))
	for _, c := range connected {
		envs = append(envs, envEntry{
			Name:      c.Name,
			Type:      "executor",
			IsDefault: c.IsDefault,
			Tools:     coreTools(),
			LastSeen:  c.LastSeenAt,
		})
	}
	writeJSON(w, map[string]any{"envs": envs})
}
