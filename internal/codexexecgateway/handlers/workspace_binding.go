package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// ConnectedExecutor is the join shape returned by workspace listing endpoints.
// It mirrors codexexecgateway.ConnectedExecutor without importing the parent package
// (which would create an import cycle).
type ConnectedExecutor struct {
	ExeID       string     `json:"exe_id"`
	Description string     `json:"description"`
	DefaultCwd  string     `json:"default_cwd"`
	IsDefault   bool       `json:"is_default"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

// BindingStore is the subset of storage required by the workspace binding handlers.
type BindingStore interface {
	BindWorkspaceExecutor(ctx context.Context, workspaceID, exeID string, isDefault bool) error
	UnbindWorkspaceExecutor(ctx context.Context, workspaceID, exeID string) error
	ListWorkspaceExecutors(ctx context.Context, workspaceID string) ([]ConnectedExecutor, error)
}

type bindRequest struct {
	ExeID     string `json:"exe_id"`
	IsDefault bool   `json:"is_default"`
}

// PostBinding returns an http.HandlerFunc that binds an executor to a workspace.
func PostBinding(store BindingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wid := chi.URLParam(r, "wid")
		var req bindRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if req.ExeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exe_id required"})
			return
		}
		if err := store.BindWorkspaceExecutor(r.Context(), wid, req.ExeID, req.IsDefault); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bind"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	}
}

// DeleteBinding returns an http.HandlerFunc that removes a workspace ↔ executor binding.
func DeleteBinding(store BindingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wid := chi.URLParam(r, "wid")
		exeID := chi.URLParam(r, "exe_id")
		if err := store.UnbindWorkspaceExecutor(r.Context(), wid, exeID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unbind"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListBinding returns an http.HandlerFunc that lists all executors bound to a workspace.
func ListBinding(store BindingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wid := chi.URLParam(r, "wid")
		rows, err := store.ListWorkspaceExecutors(r.Context(), wid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list"})
			return
		}
		if rows == nil {
			rows = []ConnectedExecutor{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}
