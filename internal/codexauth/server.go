package codexauth

import (
	"crypto/rsa"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SessionResolver returns the user_id for the request's session cookie,
// or empty string if unauthenticated. agentserver supplies its existing
// session middleware here.
type SessionResolver func(r *http.Request) string

// Server is the user-facing codex-auth HTTP surface (PKCE / device flow /
// JWKS / agent-identity), all mounted under a single chi subrouter.
type Server struct {
	Store            *Store
	IssuerURL        string          // e.g. "https://agent.cs.ac.cn/codex-auth"
	SigningKey       *rsa.PrivateKey // active RSA key for id_token + Agent Identity JWT
	SigningKid       string
	SessionResolve   SessionResolver
	LoginRedirectURL string // where /oauth/authorize sends unauth users
}

// Mount registers all codex-auth routes onto r. Routes are mounted
// without a prefix; the caller decides where this subtree lives
// (typically /codex-auth/* on agentserver's outermost router).
func (s *Server) Mount(r chi.Router) {
	r.Get("/oauth/authorize", s.handleAuthorize)
	r.Post("/oauth/token", s.handleToken)

	r.Get("/agent-identities/jwks", s.handleJWKS)
	r.Post("/v1/agent/{rid}/task/register", s.handleTaskRegister)

	r.Post("/api/accounts/deviceauth/usercode", s.handleDeviceUserCode)
	r.Post("/api/accounts/deviceauth/token", s.handleDeviceToken)
	r.Get("/codex/device", s.handleDeviceVerifyPage)
	r.Post("/codex/device", s.handleDeviceVerifySubmit)
}

// All route handlers are implemented in dedicated files:
// pkce.go, device.go, agent_identity.go, jwks.go.
