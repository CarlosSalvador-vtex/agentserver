package server

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
)

// --- Marketplace endpoints (improvements.md #18) --------------------------
//
// GET  /api/marketplace/skills          — list shared skill drafts (any auth)
// GET  /api/marketplace/souls           — list shared soul drafts  (any auth)
// POST /api/marketplace/skills/{id}/fork — copy to caller workspace
// POST /api/marketplace/souls/{id}/fork  — copy to caller workspace
// PATCH /api/playground/skills/{id}/visibility — workspace owner/maintainer
// PATCH /api/playground/souls/{id}/visibility  — workspace owner/maintainer
// PATCH /api/admin/playground/skills/{id}/visibility — admin override
// PATCH /api/admin/playground/souls/{id}/visibility  — admin override

func (s *Server) handleListMarketplaceSkills(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSharedSkillDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSkillSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, playgroundSkillSummary{
			ID:          d.ID,
			Name:        d.Name,
			Description: d.Description,
			Status:      d.Status,
			WorkspaceID: d.WorkspaceID.String,
			UpdatedAt:   d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"skills": out})
}

func (s *Server) handleListMarketplaceSouls(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSharedSoulDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSoulSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, playgroundSoulSummary{
			ID:          d.ID,
			Name:        d.Name,
			Description: d.Description,
			Status:      d.Status,
			WorkspaceID: d.WorkspaceID.String,
			UpdatedAt:   d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"souls": out})
}

func (s *Server) handleForkMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	fork, err := s.DB.ForkSkillDraft(id, userID, wsID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.summarizeSkillForUser(userID, fork))
}

func (s *Server) handleForkMarketplaceSoul(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	fork, err := s.DB.ForkSoulDraft(id, userID, wsID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.summarizeSoulForUser(userID, fork))
}

// userCanSetDraftVisibility allows workspace owner/maintainer to share drafts
// scoped to that workspace. System-wide drafts (NULL workspace) stay admin-only.
func (s *Server) userCanSetDraftVisibility(userID string, workspaceID sql.NullString) bool {
	if !workspaceID.Valid || workspaceID.String == "" {
		return false
	}
	role, err := s.DB.GetWorkspaceMemberRole(workspaceID.String, userID)
	if err != nil {
		return false
	}
	return role == "owner" || role == "maintainer"
}

func (s *Server) handleAuthorSetSkillVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSkillDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.userCanSetDraftVisibility(userID, draft.WorkspaceID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.patchSkillDraftVisibility(w, r, userID, id)
}

func (s *Server) handleAuthorSetSoulVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSoulDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.userCanSetDraftVisibility(userID, draft.WorkspaceID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.patchSoulDraftVisibility(w, r, userID, id)
}

// Admin-only visibility toggle. Route is wrapped in requireAdmin middleware.

func (s *Server) handleSetSkillVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	s.patchSkillDraftVisibility(w, r, userID, id)
}

func (s *Server) handleSetSoulVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	s.patchSoulDraftVisibility(w, r, userID, id)
}

func (s *Server) patchSkillDraftVisibility(w http.ResponseWriter, r *http.Request, userID, id string) {
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.DB.SetSkillDraftVisibility(id, req.Visibility); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AppendDraftAuditEvent("skill", id, userID, "visibility_changed", map[string]interface{}{
		"visibility": req.Visibility,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchSoulDraftVisibility(w http.ResponseWriter, r *http.Request, userID, id string) {
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.DB.SetSoulDraftVisibility(id, req.Visibility); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AppendDraftAuditEvent("soul", id, userID, "visibility_changed", map[string]interface{}{
		"visibility": req.Visibility,
	})
	w.WriteHeader(http.StatusNoContent)
}
