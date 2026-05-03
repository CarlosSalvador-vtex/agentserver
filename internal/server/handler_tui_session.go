package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// authUserID extracts the authenticated user ID from the request context.
func authUserID(r *http.Request) string {
	return auth.UserIDFromContext(r.Context())
}

type createSessionReq struct {
	WorkspaceID         string `json:"workspace_id"`
	ExecutorID          string `json:"executor_id"`
	Title               string `json:"title,omitempty"`
	PermissionMode      string `json:"permission_mode,omitempty"`
	PreferredExecutorID string `json:"preferred_executor_id,omitempty"`
}

func (s *Server) handleCreateAgentSession(w http.ResponseWriter, r *http.Request) {
	userID := authUserID(r)
	if userID == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	var req createSessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "invalid body")
		return
	}
	if req.WorkspaceID == "" || req.ExecutorID == "" {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "workspace_id and executor_id required")
		return
	}
	sid := "cse_" + uuid.NewString()
	extID := fmt.Sprintf("tui:%s:%d", req.ExecutorID, time.Now().Unix())
	title := req.Title
	if title == "" {
		title = "TUI session"
	}
	if err := s.DB.CreateAgentSessionTUI(r.Context(), db.CreateTUISessionParams{
		ID:                  sid,
		WorkspaceID:         req.WorkspaceID,
		ExternalID:          extID,
		Title:               title,
		CreatorUserID:       userID,
		PermissionMode:      req.PermissionMode,
		PreferredExecutorID: req.PreferredExecutorID,
	}); err != nil {
		writeAPIErr(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":   sid,
		"external_id":  extID,
		"channel_type": "tui",
		"created_at":   time.Now().UTC(),
	})
}

type attachSessionReq struct {
	ExecutorID            string `json:"executor_id"`
	Mode                  string `json:"mode,omitempty"`             // "operator" | "observer"
	AsPermissionResponder bool   `json:"as_permission_responder,omitempty"`
	AlsoBecomePreferred   bool   `json:"also_become_preferred,omitempty"`
}

func (s *Server) handleAttachAgentSession(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	sid := chi.URLParam(r, "sid")
	var req attachSessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "invalid body")
		return
	}
	if req.ExecutorID == "" {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "executor_id required")
		return
	}
	sess, err := s.DB.GetAgentSession(sid)
	if err != nil || sess == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	// observer mode: read-only attach, no DB writes
	if req.Mode == "observer" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session_id":           sid,
			"permission_responder": sess.PermissionResponder,
			"previous_responder":   "",
			"previous_preferred":   "",
		})
		return
	}
	// operator mode (default): atomic responder + (optional) preferred swap
	prev, err := s.DB.AttachResponder(r.Context(), sid, req.ExecutorID, req.AlsoBecomePreferred)
	if err != nil {
		writeAPIErr(w, http.StatusInternalServerError, "internal", "attach failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":           sid,
		"permission_responder": req.ExecutorID,
		"previous_responder":   prev.PreviousResponder,
		"previous_preferred":   prev.PreviousPreferred,
	})
}

func (s *Server) handleListAgentSessions(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	q := r.URL.Query()
	wid := q.Get("workspace_id")
	chType := q.Get("channel_type")
	execID := q.Get("executor_id")
	if wid == "" || chType == "" {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "workspace_id and channel_type required")
		return
	}
	limit, _ := strconv.Atoi(q.Get("latest"))
	if limit <= 0 {
		limit = 20
	}
	list, err := s.DB.ListSessionsByChannel(r.Context(), wid, chType, execID, limit)
	if err != nil {
		writeAPIErr(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		out = append(out, map[string]any{
			"session_id":           it.ID,
			"external_id":          it.ExternalID,
			"title":                it.Title,
			"last_activity_at":     it.LastActivityAt,
			"permission_responder": it.PermissionResponder,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sessions": out})
}
