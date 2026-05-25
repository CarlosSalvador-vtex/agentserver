package sandboxproxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const (
	hermesCookieKey    = "hermes-token"
	hermesPort         = "9119"
	hermesCookieMaxTTL = 7 * 24 * time.Hour
)

// handleHermesSubdomainProxy handles all requests on
// hermes-{sandboxID}.{baseDomain}.
//
// Auth flow mirrors opencode/jupyter:
//  1. GET /auth?token=<main-site session>: validate, set per-subdomain
//     cookie (no Domain attr — scoped to this subdomain only), 302 /
//  2. All other requests: read cookie, validate, workspace membership,
//     reverse-proxy to the in-cluster Hermes Dashboard on the pod IP.
func (s *Server) handleHermesSubdomainProxy(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.URL.Path == "/auth" && r.Method == http.MethodGet {
		s.exchangeHermesToken(w, r, sandboxID)
		return
	}

	cookie, err := r.Cookie(hermesCookieKey)
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
	if !found || sbx.Type != "hermes" {
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

	s.throttledActivity(sandboxID)

	target := &url.URL{Scheme: "http", Host: sbx.PodIP + ":" + hermesPort}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // SSE + WebSocket streaming
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("hermes proxy error for sandbox %s: %v", sandboxID, err)
		http.Error(w, "proxy error", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) exchangeHermesToken(w http.ResponseWriter, r *http.Request, sandboxID string) {
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
	if !found || sbx.Type != "hermes" {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	isMember, err := s.DB.IsWorkspaceMember(sbx.WorkspaceID, userID)
	if err != nil || !isMember {
		writeErrorPage(w, errPageSandboxNotFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     hermesCookieKey,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(hermesCookieMaxTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
