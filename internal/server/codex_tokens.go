package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"

	"github.com/go-chi/chi/v5"
)

// handleMintCodexToken issues a new long-lived bearer token for codex CLI access.
//
//	@Summary   Mint a Codex access token
//	@Tags      Codex Tokens
//	@Accept    json
//	@Produce   json
//	@Param     body  body  CodexTokenMintRequest  true  "Token parameters"
//	@Success   201  {object}  CodexTokenMintResponse
//	@Failure   400  {string}  string  "invalid JSON"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "not a member of this workspace"
//	@Failure   422  {string}  string  "workspace_id and name are required / expires_at invalid"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/codex/tokens [post]
func (s *Server) handleMintCodexToken(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req CodexTokenMintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.WorkspaceID == "" || req.Name == "" {
		http.Error(w, "workspace_id and name are required", http.StatusUnprocessableEntity)
		return
	}
	exp, err := resolveExpiresAt(req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	role, err := s.DB.GetWorkspaceMemberRole(req.WorkspaceID, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if role == "" || role == "guest" {
		http.Error(w, "not a member of this workspace", http.StatusForbidden)
		return
	}

	tok, err := secrets.Mint(secrets.AgentserverTokenSpec)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.DB.CreateCodexToken(r.Context(), db.CodexToken{
		ID: tok.ID, UserID: userID, WorkspaceID: req.WorkspaceID, Name: req.Name,
		TokenHash: tok.Hash, ExpiresAt: exp,
	}); err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(CodexTokenMintResponse{
		ID: tok.ID, Token: tok.Full, Name: req.Name, WorkspaceID: req.WorkspaceID,
		ExpiresAt: exp, CreatedAt: time.Now().UTC(),
	})
}

// handleListCodexTokens returns all codex tokens for a workspace.
//
//	@Summary   List Codex tokens for a workspace
//	@Tags      Codex Tokens
//	@Produce   json
//	@Param     workspace_id     query  string  true   "Workspace id"
//	@Param     include_revoked  query  bool    false  "Include revoked tokens (default false)"
//	@Success   200  {array}   CodexTokenListItem
//	@Failure   400  {string}  string  "workspace_id required"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "not a member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/codex/tokens [get]
func (s *Server) handleListCodexTokens(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := r.URL.Query().Get("workspace_id")
	if wid == "" {
		http.Error(w, "workspace_id required", http.StatusBadRequest)
		return
	}
	role, err := s.DB.GetWorkspaceMemberRole(wid, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if role == "" {
		http.Error(w, "not a member", http.StatusForbidden)
		return
	}
	includeRevoked := r.URL.Query().Get("include_revoked") == "true"
	rows, err := s.DB.ListCodexTokensForWorkspace(r.Context(), wid, includeRevoked)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]CodexTokenListItem, 0, len(rows))
	for _, t := range rows {
		out = append(out, CodexTokenListItem{
			ID: t.ID, Name: t.Name, WorkspaceID: t.WorkspaceID,
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
			LastUsedAt: t.LastUsedAt, Revoked: t.RevokedAt != nil, RevokedAt: t.RevokedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleRevokeCodexToken revokes an existing codex token by id.
//
//	@Summary   Revoke a Codex token
//	@Tags      Codex Tokens
//	@Param     id  path  string  true  "Token id"
//	@Success   204
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "forbidden"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/codex/tokens/{id} [delete]
func (s *Server) handleRevokeCodexToken(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	row, err := s.DB.GetCodexToken(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if row == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	role, _ := s.DB.GetWorkspaceMemberRole(row.WorkspaceID, userID)
	isOwner := row.UserID == userID
	isAdmin := role == "owner" || role == "maintainer"
	if !isOwner && !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.DB.RevokeCodexToken(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routesForCodexTokens is a small chi sub-router used by tests so the
// `{id}` URL param resolves correctly when calling the handler outside the
// main Routes() wiring.
func (s *Server) routesForCodexTokens() http.Handler {
	r := chi.NewRouter()
	r.Post("/api/codex/tokens", s.handleMintCodexToken)
	r.Get("/api/codex/tokens", s.handleListCodexTokens)
	r.Delete("/api/codex/tokens/{id}", s.handleRevokeCodexToken)
	return r
}
