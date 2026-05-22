package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"
	"github.com/go-chi/chi/v5"
)

// handleListWorkspaceAPIKeys returns all keys for a workspace (any member).
//
//	@Summary    List workspace API keys
//	@Description Returns prefix-only metadata. Secrets are never included.
//	@Tags        Workspace API Keys
//	@Produce     json
//	@Param       wid  path  string  true  "Workspace id"
//	@Success     200  {array}   WorkspaceAPIKey
//	@Failure     403  {string}  string  "not a member"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{wid}/api-keys [get]
func (s *Server) handleListWorkspaceAPIKeys(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	rows, err := s.DB.ListWorkspaceAPIKeys(r.Context(), wid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]WorkspaceAPIKey, 0, len(rows))
	for _, k := range rows {
		scopes := k.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, WorkspaceAPIKey{
			ID:         k.ID,
			Name:       k.Name,
			Prefix:     k.Prefix,
			Scopes:     scopes,
			CreatedAt:  k.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:  k.ExpiresAt.UTC().Format(time.RFC3339),
			LastUsedAt: rfc3339Ptr(k.LastUsedAt),
			RevokedAt:  rfc3339Ptr(k.RevokedAt),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleMintWorkspaceAPIKey creates a new key. Returns the full secret
// ONCE in the response body — clients must persist it on their side
// because it never appears again. Validates scopes against the catalog
// (`internal/server/api_key_scopes.go`) — unknown or unavailable scopes
// are rejected with 400.
//
//	@Summary     Mint a workspace API key
//	@Description Returns the secret ONCE in the response body. At least one Available scope must be provided.
//	@Tags        Workspace API Keys
//	@Accept      json
//	@Produce     json
//	@Param       wid   path  string                      true  "Workspace id"
//	@Param       body  body  WorkspaceAPIKeyMintRequest  true  "Key metadata"
//	@Success     201   {object}  WorkspaceAPIKeyMintResponse
//	@Failure     400   {string}  string  "name required / scope not available / at least one scope required"
//	@Failure     422   {string}  string  "expires_at invalid (bad RFC3339 / in past / >365d in future)"
//	@Failure     403   {string}  string  "owner or maintainer required"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{wid}/api-keys [post]
func (s *Server) handleMintWorkspaceAPIKey(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if !s.requireWorkspaceRole(w, r, wid, "owner", "maintainer") {
		return
	}
	var req WorkspaceAPIKeyMintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := validateScopes(req.Scopes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	exp, err := resolveExpiresAt(req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Extract userID for the row — requireWorkspaceRole already validated membership.
	// Re-derive from context since the helper doesn't return it.
	userID := auth.UserIDFromContext(r.Context())
	row := db.WorkspaceAPIKey{
		ID:          tok.ID,
		WorkspaceID: wid,
		UserID:      userID,
		Name:        req.Name,
		Prefix:      tok.ID, // prefix == ID for ask_ tokens (both = "ask_<16chars>")
		SecretHash:  tok.Hash,
		Scopes:      req.Scopes,
		ExpiresAt:   exp,
	}
	if err := s.DB.CreateWorkspaceAPIKey(r.Context(), row); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(WorkspaceAPIKeyMintResponse{
		ID:        tok.ID,
		Name:      req.Name,
		Prefix:    tok.ID,
		Secret:    tok.Full, // full wire-format token returned once to the user
		Scopes:    req.Scopes,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: exp.Format(time.RFC3339),
	})
}

// handleListWorkspaceAPIKeyScopes exposes the in-code catalog so the SPA's
// mint modal renders the checkbox grid from the backend's source of truth.
//
//	@Summary    List available API key scopes
//	@Description Returns the catalog of scope names + descriptions. Available=false entries are placeholders shown greyed-out in the UI.
//	@Tags       Workspace API Keys
//	@Produce    json
//	@Param      wid  path  string  true  "Workspace id"
//	@Success    200  {array}   APIKeyScopeDescriptor
//	@Failure    403  {string}  string  "not a member"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{wid}/api-keys/scopes [get]
func (s *Server) handleListWorkspaceAPIKeyScopes(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	out := make([]APIKeyScopeDescriptor, 0, len(apiKeyScopeCatalog))
	for _, sc := range apiKeyScopeCatalog {
		out = append(out, APIKeyScopeDescriptor{
			Name:        sc.Name,
			Description: sc.Description,
			Available:   sc.Available,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleRevokeWorkspaceAPIKey soft-deletes a key. Idempotent.
//
//	@Summary    Revoke a workspace API key
//	@Tags       Workspace API Keys
//	@Param      wid  path  string  true  "Workspace id"
//	@Param      id   path  string  true  "Key id (= prefix)"
//	@Success    204
//	@Failure    403  {string}  string  "owner or maintainer required"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{wid}/api-keys/{id} [delete]
func (s *Server) handleRevokeWorkspaceAPIKey(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	keyID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wid, "owner", "maintainer") {
		return
	}
	if err := s.DB.RevokeWorkspaceAPIKey(r.Context(), wid, keyID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// errInvalidAPIKey is used by internal validate path (Task 1.4).
var errInvalidAPIKey = errors.New("invalid api key")
