package codexexecgateway

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

// WorkspaceExecutor is a row in workspace_executors.
type WorkspaceExecutor struct {
	WorkspaceID string    `json:"workspace_id"`
	ExeID       string    `json:"exe_id"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

// ConnectedExecutor is the join shape returned by /api/exec-gateway/connected.
type ConnectedExecutor struct {
	ExeID       string     `json:"exe_id"`
	Description string     `json:"description"`
	DefaultCwd  string     `json:"default_cwd"`
	IsDefault   bool       `json:"is_default"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}
