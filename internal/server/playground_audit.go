package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
)

// Sprint 3 PR-3 (improvements.md #14). Read-side of the audit log.
// GET /api/playground/{skills|souls}/{id}/audit returns the most recent
// events for the draft, newest first. Author-only (matches the rest of
// the per-draft endpoints).

type auditEventOut struct {
	ID          int64                  `json:"id"`
	DraftKind   string                 `json:"draft_kind"`
	DraftID     string                 `json:"draft_id"`
	ActorUserID string                 `json:"actor_user_id,omitempty"`
	Action      string                 `json:"action"`
	PayloadDiff map[string]interface{} `json:"payload_diff,omitempty"`
	CreatedAt   string                 `json:"created_at"`
}

func (s *Server) handleListSkillDraftAudit(w http.ResponseWriter, r *http.Request) {
	s.handleListDraftAudit(w, r, "skill")
}

func (s *Server) handleListSoulDraftAudit(w http.ResponseWriter, r *http.Request) {
	s.handleListDraftAudit(w, r, "soul")
}

func (s *Server) handleListDraftAudit(w http.ResponseWriter, r *http.Request, kind string) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	// Ownership check (delegate to the per-kind fetchers we already have).
	switch kind {
	case "skill":
		draft, err := s.DB.GetSkillDraft(id)
		if err != nil || draft == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
			http.Error(w, "not your draft", http.StatusForbidden)
			return
		}
	case "soul":
		draft, err := s.DB.GetSoulDraft(id)
		if err != nil || draft == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
			http.Error(w, "not your draft", http.StatusForbidden)
			return
		}
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}

	events, err := s.DB.ListDraftAuditEvents(kind, id, limit)
	if err != nil {
		http.Error(w, "list audit events", http.StatusInternalServerError)
		return
	}

	out := make([]auditEventOut, 0, len(events))
	for _, e := range events {
		out = append(out, auditEventOut{
			ID:          e.ID,
			DraftKind:   e.DraftKind,
			DraftID:     e.DraftID,
			ActorUserID: e.ActorUserID.String,
			Action:      e.Action,
			PayloadDiff: e.PayloadDiff,
			CreatedAt:   e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": out})
}
