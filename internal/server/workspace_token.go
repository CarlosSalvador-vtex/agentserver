package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleWorkspaceProxyToken returns the workspace's persistent proxy token,
// creating one on first request. Used by cc-broker to acquire a token it can
// inject into the spawned Claude CLI's ANTHROPIC_AUTH_TOKEN env so LLM
// traffic can be authenticated against llmproxy without per-sandbox identity.
//
// Auth: shared INTERNAL_API_SECRET (X-Internal-Secret header), same as the
// other /api/internal endpoints used by sibling services.
func (s *Server) handleWorkspaceProxyToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorkspaceID == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	token, err := s.DB.GetOrCreateWorkspaceToken(req.WorkspaceID)
	if err != nil {
		log.Printf("workspace-token: db error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}
