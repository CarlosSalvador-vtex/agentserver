package sandboxproxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const (
	jupyterCookieKey    = "jupyter-token"
	jupyterPort         = "8888"
	jupyterCookieMaxTTL = 7 * 24 * time.Hour
)

// handleJupyterSubdomainProxy handles all requests on
// jupyter-{sandboxID}.{baseDomain}.
//
// Auth flow mirrors opencode/claudecode:
//  1. GET /auth?token=<main-site session>: validate, set per-subdomain
//     cookie (no Domain attr — scoped to this subdomain only), 302 /lab
//  2. All other requests: read cookie, validate, workspace membership,
//     reverse-proxy to the in-cluster Jupyter Server on the pod IP.
//
// Path is forwarded as-is. Jupyter runs with base_url=/ so absolute
// URLs in its HTML/JS work without any rewriting in this proxy.
func (s *Server) handleJupyterSubdomainProxy(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.URL.Path == "/auth" && r.Method == http.MethodGet {
		s.exchangeJupyterToken(w, r, sandboxID)
		return
	}

	cookie, err := r.Cookie(jupyterCookieKey)
	if err != nil {
		http.Redirect(w, r, "https://"+s.matchedBaseDomain(r)+"/", http.StatusFound)
		return
	}
	userID, ok := s.Auth.ValidateToken(cookie.Value)
	if !ok {
		http.Redirect(w, r, "https://"+s.matchedBaseDomain(r)+"/", http.StatusFound)
		return
	}

	sbx, found := s.Sandboxes.Resolve(sandboxID)
	if !found || sbx.Type != "jupyter" {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	isMember, err := s.DB.IsWorkspaceMember(sbx.WorkspaceID, userID)
	if err != nil || !isMember {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	if sbx.Status != "running" {
		writeErrorPage(w, errPageSandboxNotRunning)
		return
	}
	if sbx.PodIP == "" {
		writeErrorPage(w, errPagePodNotReady)
		return
	}

	// The notebook image runs with `AgentserverIdentityProvider`
	// (notebook/agentserver_jupyter_ext/identity_provider.py), which
	// authenticates the request from X-Forwarded-User and rejects
	// (302 /login) anything without it. The proxy IS the trust boundary
	// — by this point we've already validated the cookie and confirmed
	// workspace membership. Token/Basic Auth headers are ignored by
	// that IdP.
	r.Header.Set("X-Forwarded-User", userID)
	if sbx.ProxyToken != "" {
		// Belt-and-braces: also send Jupyter's native token format in
		// case a deployer swaps in the stock IdP.
		r.Header.Set("Authorization", "token "+sbx.ProxyToken)
	}

	s.throttledActivity(sandboxID)

	target := &url.URL{Scheme: "http", Host: sbx.PodIP + ":" + jupyterPort}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // SSE + WebSocket streaming
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("jupyter proxy error for sandbox %s: %v", sandboxID, err)
		http.Error(w, "proxy error", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) exchangeJupyterToken(w http.ResponseWriter, r *http.Request, sandboxID string) {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	userID, ok := s.Auth.ValidateToken(tok)
	if !ok {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	sbx, found := s.Sandboxes.Resolve(sandboxID)
	if !found || sbx.Type != "jupyter" {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	isMember, err := s.DB.IsWorkspaceMember(sbx.WorkspaceID, userID)
	if err != nil || !isMember {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     jupyterCookieKey,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(jupyterCookieMaxTTL.Seconds()),
	})
	http.Redirect(w, r, "/lab", http.StatusFound)
}
