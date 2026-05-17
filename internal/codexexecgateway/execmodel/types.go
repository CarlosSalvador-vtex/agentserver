// Package execmodel holds shared DTOs that cross the
// codexexecgateway↔handlers package boundary. Both packages import it
// to avoid an import cycle and to eliminate field-by-field adapter
// translation that silently drops new fields.
package execmodel

import "time"

// Executor is the persistent identity of a codex-exec node.
type Executor struct {
	ExeID        string     `json:"exe_id"`
	UserID       string     `json:"user_id"`
	DisplayName  string     `json:"display_name,omitempty"`
	Description  string     `json:"description,omitempty"`
	DefaultCwd   string     `json:"default_cwd,omitempty"`
	RegisteredAt time.Time  `json:"registered_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

// WorkspaceExecutor is a row in workspace_executors. Name is the
// workspace-unique human-readable label LLM-facing tools surface
// (per v0.54.0); Description is the per-binding free-text note the
// user can attach when registering.
type WorkspaceExecutor struct {
	WorkspaceID string    `json:"workspace_id"`
	ExeID       string    `json:"exe_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

// ConnectedExecutor is the join shape returned by workspace listing
// and /api/exec-gateway/connected endpoints. ExeID is still present
// (env-mcp uses it to dial /bridge and our internal routing keys by
// it) but LLM-facing payloads omit it.
type ConnectedExecutor struct {
	ExeID       string     `json:"exe_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsDefault   bool       `json:"is_default"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}
