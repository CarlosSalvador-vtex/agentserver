package handlers

import (
	"context"
	"encoding/json"
	"net/http"
)

// InternalConnectedStore is the subset of storage required by Connected.
// It uses the local ConnectedExecutor type (defined in workspace_binding.go)
// to avoid an import cycle with the parent codexexecgateway package.
type InternalConnectedStore interface {
	ConnectedExecutorsForWorkspace(ctx context.Context, workspaceID string, connectedIDs []string) ([]ConnectedExecutor, error)
}

// Registry is satisfied by *codexexecgateway.ConnRegistry.
type Registry interface {
	ConnectedIDs() []string
}

// Connected returns the intersection of (workspace's bound executors) ∩
// (currently-connected exe_ids). Used by codex-app-gateway when composing
// the per-turn manifest.
func Connected(store InternalConnectedStore, reg Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wid := r.URL.Query().Get("workspace_id")
		if wid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id required"})
			return
		}
		ids := reg.ConnectedIDs()
		rows, err := store.ConnectedExecutorsForWorkspace(r.Context(), wid, ids)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list"})
			return
		}
		if rows == nil {
			rows = []ConnectedExecutor{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows) //nolint:errcheck
	}
}
