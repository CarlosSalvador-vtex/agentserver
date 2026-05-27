package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/notif"
	"github.com/agentserver/agentserver/internal/secrets"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// inviteTTL is how long a freshly-issued invite remains acceptable.
// Short enough to limit replay window; long enough to survive a weekend.
const inviteTTL = 7 * 24 * time.Hour

// hashInviteToken returns sha256(token) in lowercase hex.
// Same scheme used in db.workspace_invites.token_hash.
func hashInviteToken(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// handleCreateInvite — POST /api/workspaces/{id}/invites
//
//	@Summary     Create a workspace invite
//	@Description Issues a single-use invite. The invite_url is returned ONCE here
//	@Description (also sent by email if a real mailer is configured); the
//	@Description plaintext token is never persisted nor re-readable.
//	@Tags        Workspaces
//	@Accept      json
//	@Produce     json
//	@Param       id   path  string                true  "Workspace ID"
//	@Param       body body  InviteCreateRequest   true  "Invite payload"
//	@Success     201  {object}  InviteResponse
//	@Failure     400  {string}  string  "bad request"
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Failure     409  {string}  string  "invite already pending for this email"
//	@Router      /api/workspaces/{id}/invites [post]
func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}

	var req InviteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Email == "" {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "developer"
	}

	workspace, err := s.DB.GetWorkspace(wsID)
	if err != nil || workspace == nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	callerID := auth.UserIDFromContext(r.Context())

	plainToken, err := secrets.RandomHex(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	inviteID := uuid.NewString()
	expiresAt := time.Now().Add(inviteTTL).UTC()

	inv, err := s.DB.CreateInvite(
		inviteID, wsID, req.Email, req.Role,
		hashInviteToken(plainToken), callerID, expiresAt,
	)
	if err != nil {
		if errors.Is(err, db.ErrInviteAlreadyPending) {
			http.Error(w, "invite already pending for this email", http.StatusConflict)
			return
		}
		log.Printf("create invite: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	baseDomain := s.primaryBaseDomain()
	inviteURL := notif.BuildInviteURL(workspace.Slug, baseDomain, plainToken)

	if s.Mailer != nil {
		if err := s.Mailer.SendInvite(r.Context(), notif.InviteMessage{
			To:            req.Email,
			WorkspaceName: workspace.Name,
			WorkspaceSlug: workspace.Slug,
			Role:          req.Role,
			InviteURL:     inviteURL,
			ExpiresAt:     inv.ExpiresAt.UTC().Format(time.RFC3339),
		}); err != nil {
			// non-fatal: admin still has the URL to copy/paste
			log.Printf("send invite email to %s: %v", req.Email, err)
		}
	}

	resp := InviteResponse{
		ID:            inv.ID,
		Email:         inv.Email,
		Role:          inv.Role,
		ExpiresAt:     inv.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt:     inv.CreatedAt.UTC().Format(time.RFC3339),
		InviteURL:     inviteURL,
		WorkspaceSlug: workspace.Slug,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleListInvites — GET /api/workspaces/{id}/invites (owner/maintainer)
//
//	@Summary     List workspace invites
//	@Tags        Workspaces
//	@Produce     json
//	@Param       id  path  string  true  "Workspace ID"
//	@Success     200  {array}   InviteResponse
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Router      /api/workspaces/{id}/invites [get]
func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	invites, err := s.DB.ListInvitesByWorkspace(wsID)
	if err != nil {
		log.Printf("list invites: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := make([]InviteResponse, 0, len(invites))
	for _, inv := range invites {
		var acceptedAt *string
		if inv.AcceptedAt.Valid {
			s := inv.AcceptedAt.Time.UTC().Format(time.RFC3339)
			acceptedAt = &s
		}
		resp = append(resp, InviteResponse{
			ID:         inv.ID,
			Email:      inv.Email,
			Role:       inv.Role,
			ExpiresAt:  inv.ExpiresAt.UTC().Format(time.RFC3339),
			AcceptedAt: acceptedAt,
			CreatedAt:  inv.CreatedAt.UTC().Format(time.RFC3339),
			// InviteURL deliberately omitted — plaintext token is gone.
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleRevokeInvite — DELETE /api/workspaces/{id}/invites/{inviteId}
//
//	@Summary     Revoke a pending invite
//	@Tags        Workspaces
//	@Param       id        path  string  true  "Workspace ID"
//	@Param       inviteId  path  string  true  "Invite ID"
//	@Success     204
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Failure     404  {string}  string  "not found"
//	@Router      /api/workspaces/{id}/invites/{inviteId} [delete]
func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	inviteID := chi.URLParam(r, "inviteId")
	if err := s.DB.DeleteInvite(inviteID); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetInviteInfo — GET /api/auth/invite/{token} (no auth)
//
// Returns metadata so the accept-invite UI can render workspace + role
// before the user commits a password. Token enumeration is mitigated by
// returning the same 404 for "not found", "expired", and "accepted".
//
//	@Summary     Read invite metadata
//	@Tags        Auth
//	@Param       token  path  string  true  "Invite token"
//	@Produce     json
//	@Success     200  {object}  InviteInfoResponse
//	@Failure     404  {string}  string  "invalid or expired invite"
//	@Router      /api/auth/invite/{token} [get]
func (s *Server) handleGetInviteInfo(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}
	inv, err := s.DB.GetPendingInviteByTokenHash(hashInviteToken(token))
	if err != nil {
		log.Printf("lookup invite: %v", err)
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}
	if inv == nil {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}
	ws, err := s.DB.GetWorkspace(inv.WorkspaceID)
	if err != nil || ws == nil {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}
	resp := InviteInfoResponse{
		WorkspaceName: ws.Name,
		WorkspaceSlug: ws.Slug,
		Email:         inv.Email,
		Role:          inv.Role,
		ExpiresAt:     inv.ExpiresAt.UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleAcceptInvite — POST /api/auth/invite/{token}/accept (no auth)
//
// Idempotent rules:
//   - If user does not exist, create with the provided password and add as
//     member.
//   - If user exists, the provided password MUST match (else 401 — never
//     leak that the email is already registered).
//   - On success the session token is stamped with active_workspace_id and
//     set as a host-only cookie scoped to the workspace's subdomain.
//
//	@Summary     Accept a workspace invite
//	@Tags        Auth
//	@Accept      json
//	@Produce     json
//	@Param       token  path  string                true  "Invite token"
//	@Param       body   body  InviteAcceptRequest   true  "Password"
//	@Success     200    {object}  AuthStatusResponse
//	@Failure     400    {string}  string  "bad request"
//	@Failure     401    {string}  string  "invalid credentials"
//	@Failure     404    {string}  string  "invalid or expired invite"
//	@Router      /api/auth/invite/{token}/accept [post]
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}

	var req InviteAcceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	inv, err := s.DB.GetPendingInviteByTokenHash(hashInviteToken(token))
	if err != nil || inv == nil {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}

	ws, err := s.DB.GetWorkspace(inv.WorkspaceID)
	if err != nil || ws == nil {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}

	user, err := s.Auth.GetUserByEmail(inv.Email)
	if err != nil {
		log.Printf("lookup user during invite accept: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var userID string
	if user == nil {
		// New user: create with the supplied password.
		userID = uuid.NewString()
		if err := s.Auth.Register(userID, inv.Email, req.Password); err != nil {
			log.Printf("register user during invite accept: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else {
		// Existing user: require correct password (no implicit join via token).
		userID = user.ID
		if _, _, ok := s.Auth.Login(inv.Email, req.Password); !ok {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
	}

	// Add as member if not already (idempotent re-accept guard).
	if memberOK, _ := s.DB.IsWorkspaceMember(inv.WorkspaceID, userID); !memberOK {
		if err := s.DB.AddWorkspaceMember(inv.WorkspaceID, userID, inv.Role); err != nil {
			log.Printf("add member during invite accept: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if err := s.DB.MarkInviteAccepted(inv.ID); err != nil &&
		!errors.Is(err, db.ErrInviteAlreadyAccepted) {
		log.Printf("mark invite accepted: %v", err)
		// Don't fail the flow — membership already granted.
	}

	// Issue a workspace-bound session token via the existing slug-aware
	// login path. We just verified the password above, so calling
	// LoginWithWorkspace with the same creds is safe and atomic with the
	// active_workspace_id stamp.
	sessionToken, _, ok := s.Auth.LoginWithWorkspace(inv.Email, req.Password, ws.Slug)
	if !ok {
		log.Printf("issue session after invite accept failed unexpectedly for %s", inv.Email)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Host-only cookie — invite acceptance always lands on the workspace
	// subdomain, so isolate the session per tenant.
	auth.SetTokenCookieHostOnly(w, sessionToken, true)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuthStatusResponse{Status: "ok"})
}

// primaryBaseDomain returns the first configured base domain, used as the
// suffix for invite URLs. Falls back to "agentserver.local" if unset (only
// happens in unit tests that don't set BASE_DOMAIN).
func (s *Server) primaryBaseDomain() string {
	if len(s.BaseDomains) > 0 {
		return s.BaseDomains[0]
	}
	return "agentserver.local"
}

// Ensure unused import safety if fmt becomes unused after edits.
var _ = fmt.Sprintf
