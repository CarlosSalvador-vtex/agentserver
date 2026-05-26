package server

import (
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
// PATCH /api/playground/skills/{id}/visibility — admin-only
// PATCH /api/playground/souls/{id}/visibility  — admin-only

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
	writeJSON(w, http.StatusCreated, summarizeSkill(fork))
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
	writeJSON(w, http.StatusCreated, summarizeSoul(fork))
}

// Admin-only visibility toggle. Route is wrapped in requireAdmin middleware.

func (s *Server) handleSetSkillVisibility(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
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
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetSoulVisibility(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
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
	w.WriteHeader(http.StatusNoContent)
}
