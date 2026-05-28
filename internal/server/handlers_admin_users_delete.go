package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/go-chi/chi/v5"
)

// AdminDeleteUserConflictResponse is returned when the user is the sole owner of workspaces.
type AdminDeleteUserConflictResponse struct {
	Error      string                      `json:"error"`
	Workspaces []db.LastOwnerWorkspace     `json:"workspaces"`
}

//	@Summary		Anonymize (delete) a user (admin, LGPD)
//	@Description	Soft-deletes a user by anonymizing PII and revoking credentials, memberships, and sessions. Fails if the user is the sole owner of any workspace.
//	@Tags			Admin
//	@Param			id	path	string	true	"User ID"
//	@Success		204	"user anonymized"
//	@Failure		401	{string}	string	"unauthorized"
//	@Failure		403	{string}	string	"admin role required"
//	@Failure		404	{string}	string	"user not found"
//	@Failure		409	{object}	AdminDeleteUserConflictResponse	"sole workspace owner"
//	@Failure		410	{string}	string	"user already anonymized"
//	@Failure		500	{string}	string	"internal error"
//	@Security		CookieAuth
//	@Router			/api/admin/users/{id} [delete]
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")
	actorID := auth.UserIDFromContext(r.Context())

	u, err := s.DB.GetUserByID(targetID)
	if err != nil {
		log.Printf("admin delete user: lookup failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	anonymized, err := s.DB.IsUserAnonymized(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		log.Printf("admin delete user: anonymized check failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if anonymized {
		http.Error(w, "user already anonymized", http.StatusGone)
		return
	}

	lastOwner, err := s.DB.WorkspacesWhereUserIsLastOwner(r.Context(), targetID)
	if err != nil {
		log.Printf("admin delete user: last-owner check failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(lastOwner) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(AdminDeleteUserConflictResponse{
			Error:      "user is the sole owner of one or more workspaces",
			Workspaces: lastOwner,
		})
		return
	}

	if err := s.DB.AnonymizeUser(r.Context(), targetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, db.ErrUserAlreadyAnonymized) {
			http.Error(w, "user already anonymized", http.StatusGone)
			return
		}
		log.Printf("admin delete user: anonymize failed: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if s.Audit != nil {
		s.Audit.Log(r.Context(), "user.anonymized", map[string]any{
			"target_user_id": targetID,
			"actor_user_id":  actorID,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
