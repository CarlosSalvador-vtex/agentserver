package auth

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName = "agentserver-token"
	tokenTTL   = 7 * 24 * time.Hour
)

// cookieDomain returns the Domain attribute for the session cookie.
// Empty (the default) means a host-only cookie scoped to the exact
// host that set it. When set to e.g. ".agent.cs.ac.cn" it lets the
// cookie cross subdomains — necessary for the codex-auth subdomain to
// SSO with the main app session.
func cookieDomain() string {
	return os.Getenv("AGENTSERVER_COOKIE_DOMAIN")
}

type contextKey string

const (
	userIDKey            contextKey = "userID"
	activeWorkspaceIDKey contextKey = "activeWorkspaceID"
	sessionTokenKey      contextKey = "sessionToken"
)

type Auth struct {
	db *db.DB
}

func New(database *db.DB) *Auth {
	return &Auth{db: database}
}

// Register creates a new user with a bcrypt-hashed password.
func (a *Auth) Register(id, email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := a.db.CreateUser(id, email, string(hash)); err != nil {
		return err
	}
	return nil
}

// Login verifies credentials by email and returns a token.
func (a *Auth) Login(email, password string) (string, string, bool) {
	user, err := a.db.GetUserByEmail(email)
	if err != nil || user == nil {
		return "", "", false
	}
	hash, err := a.db.GetPasswordHash(user.ID)
	if err != nil || hash == nil {
		return "", "", false
	}
	if bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)) != nil {
		return "", "", false
	}
	token, err := a.IssueToken(user.ID)
	if err != nil {
		return "", "", false
	}
	return token, user.ID, true
}

// LoginWithWorkspace verifies credentials and, if workspaceSlug is non-empty,
// resolves the slug and stamps active_workspace_id on the new session token.
// Returns ok=false for bad credentials, unknown slug, or non-membership (same
// response as wrong password). On membership failure after token issue, the
// token is invalidated.
func (a *Auth) LoginWithWorkspace(email, password, workspaceSlug string) (string, string, bool) {
	token, userID, ok := a.Login(email, password)
	if !ok {
		return "", "", false
	}
	if workspaceSlug == "" {
		return token, userID, true
	}
	ws, err := a.db.GetWorkspaceBySlug(workspaceSlug)
	if err != nil || ws == nil {
		_ = a.InvalidateToken(token)
		return "", "", false
	}
	bound, err := a.SetActiveWorkspace(token, userID, ws.ID)
	if err != nil || !bound {
		_ = a.InvalidateToken(token)
		return "", "", false
	}
	return token, userID, true
}

// IssueToken generates a random token, stores it, and returns it.
func (a *Auth) IssueToken(userID string) (string, error) {
	token, err := secrets.RandomHex(32)
	if err != nil {
		return "", err
	}
	if err := a.db.CreateToken(token, userID, time.Now().Add(tokenTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateToken checks the token against the database and returns the user ID.
func (a *Auth) ValidateToken(token string) (string, bool) {
	userID, err := a.db.ValidateToken(token)
	if err != nil || userID == "" {
		return "", false
	}
	return userID, true
}

// ValidateTokenWithWorkspace returns (userID, activeWorkspaceID, ok).
// activeWorkspaceID is "" when the session has no workspace selected.
func (a *Auth) ValidateTokenWithWorkspace(token string) (string, string, bool) {
	userID, ws, err := a.db.ValidateTokenWithWorkspace(token)
	if err != nil || userID == "" {
		return "", "", false
	}
	return userID, ws, true
}

// SetActiveWorkspace validates the user is a member of the workspace then
// persists active_workspace_id on the session token. Pass empty workspaceID
// to clear. Returns false if not a member.
func (a *Auth) SetActiveWorkspace(token, userID, workspaceID string) (bool, error) {
	if workspaceID != "" {
		ok, err := a.db.IsWorkspaceMember(workspaceID, userID)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if err := a.db.SetTokenActiveWorkspace(token, workspaceID); err != nil {
		return false, err
	}
	return true, nil
}

// InvalidateToken removes the token row so the same cookie value cannot
// re-authenticate even if the browser fails to clear the cookie.
func (a *Auth) InvalidateToken(token string) error {
	return a.db.DeleteToken(token)
}

// Middleware authenticates web requests via session cookie. The TUI / agent
// CLI does NOT use this — it goes through BearerMiddleware on /api/agents/*.
// Injects userID, sessionToken, and activeWorkspaceID into the request context.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID, activeWS, ok := a.ValidateTokenWithWorkspace(cookie.Value)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		ctx = context.WithValue(ctx, sessionTokenKey, cookie.Value)
		ctx = context.WithValue(ctx, activeWorkspaceIDKey, activeWS)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// BearerMiddleware authenticates TUI / agent CLI requests via OAuth Bearer
// token, using Hydra introspection. The web app does NOT use this — it goes
// through Middleware (cookie auth). Token must be Active and have a non-empty
// Subject (= user ID), which is then injected into request context under the
// same key Middleware uses.
func BearerMiddleware(h *HydraClient) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(authz, "Bearer ")
			intro, err := h.IntrospectToken(token)
			if err != nil || !intro.Active || intro.Subject == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, intro.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ValidateRequest checks whether a request has a valid auth cookie and returns the user ID.
func (a *Auth) ValidateRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	return a.ValidateToken(cookie.Value)
}

// UserIDFromContext extracts the user ID set by Middleware.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

// ActiveWorkspaceFromContext returns the workspace currently bound to the
// session. Empty string when the user has not selected one (fresh login,
// or workspace was deleted).
func ActiveWorkspaceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(activeWorkspaceIDKey).(string)
	return v
}

// SessionTokenFromContext returns the raw cookie value for the current
// request. Used by handlers that need to mutate session state (e.g.
// switching active workspace).
func SessionTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionTokenKey).(string)
	return v
}

// ContextWithUserID returns a copy of ctx with userID injected under the same
// key that Middleware uses. Intended for use in tests that bypass the real
// auth middleware.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// ContextWithActiveWorkspace injects an active workspace ID for tests.
func ContextWithActiveWorkspace(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, activeWorkspaceIDKey, workspaceID)
}

// GetUserByID returns user info by ID.
func (a *Auth) GetUserByID(id string) (*db.User, error) {
	return a.db.GetUserByID(id)
}

// GetUserByEmail returns user info by email.
func (a *Auth) GetUserByEmail(email string) (*db.User, error) {
	return a.db.GetUserByEmail(email)
}

// DB returns the underlying database for use by other auth subsystems.
func (a *Auth) DB() *db.DB {
	return a.db
}

func SetTokenCookie(w http.ResponseWriter, token string) {
	SetTokenCookieHostOnly(w, token, false)
}

// SetTokenCookieHostOnly sets the session cookie. When hostOnly is true, Domain
// is omitted so the cookie is scoped to the current host (tenant subdomain).
func SetTokenCookieHostOnly(w http.ResponseWriter, token string, hostOnly bool) {
	domain := cookieDomain()
	if hostOnly {
		domain = ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Domain:   domain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(tokenTTL.Seconds()),
	})
}

// ClearTokenCookie removes the session cookie using the same Domain policy as issuance.
func ClearTokenCookie(w http.ResponseWriter, hostOnly bool) {
	domain := cookieDomain()
	if hostOnly {
		domain = ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Domain:   domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
