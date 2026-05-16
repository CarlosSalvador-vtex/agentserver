package handlers

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireAgentserverSecret rejects requests whose X-Internal-Secret
// header does not constant-time-match `secret`. When `secret` is empty,
// this middleware is a no-op (dev mode).
//
// This is separate from RequireSharedSecret because the two represent
// different trust scopes:
//   - RequireSharedSecret       → cap-token admin API
//                                 (called by codex-app-gateway via
//                                 CXG_INTERNAL_SHARED_SECRET)
//   - RequireAgentserverSecret  → user-management API
//                                 (called by agentserver on behalf of
//                                 session-authenticated humans, via
//                                 CXG_AGENTSERVER_INTERNAL_SECRET)
func RequireAgentserverSecret(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if secret == "" {
				next.ServeHTTP(w, r)
				return
			}
			got := r.Header.Get("X-Internal-Secret")
			if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireSharedSecret rejects requests whose Authorization: Bearer header
// does not constant-time-match `secret`.
func RequireSharedSecret(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			got := h[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
