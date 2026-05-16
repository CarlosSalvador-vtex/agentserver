package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// CloudRegisterStore is the subset of *codexexecgateway.Store the
// upstream-compat /cloud/executor/{id}/register handler needs.
type CloudRegisterStore interface {
	GetRegistrationTokenHash(ctx context.Context, exeID string) (string, error)
}

// cloudRegisterResponse mirrors the upstream codex exec-server registry
// response shape. Codex v0.130 expects {id, executor_id, url}; main has
// dropped `id`. We include all three so both shapes deserialize cleanly.
// The `id` field is only used by upstream for log messages — we reuse
// executor_id since we don't track per-attempt registration IDs.
type cloudRegisterResponse struct {
	ID         string `json:"id"`
	ExecutorID string `json:"executor_id"`
	URL        string `json:"url"`
}

// CloudRegister is the upstream-compatibility shim for
// `codex exec-server --remote <base_url> --executor-id <id>`. Upstream
// codex first POSTs to `<base_url>/cloud/executor/{id}/register` with
// `Authorization: Bearer <token>` (token from
// CODEX_EXEC_SERVER_REMOTE_BEARER_TOKEN env), then connects to the
// returned URL with no further auth (the URL itself is the credential).
//
// Our existing inbound handler at `/codex-exec/{exe_id}?token=...` is
// the actual ws endpoint; this handler verifies the bearer once and
// returns that URL with the token plumbed through.
//
// publicWSBaseURL is the externally-visible wss:// origin (e.g.
// "wss://codex-exec.agent.cs.ac.cn:443"). When empty, the response URL
// is synthesised from r.Host with wss scheme — best-effort fallback for
// dev / direct in-cluster use.
func CloudRegister(store CloudRegisterStore, publicWSBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		exeID := chi.URLParam(r, "exe_id")
		if exeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exe_id required"})
			return
		}
		bearer, ok := extractBearer(r)
		if !ok || bearer == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer"})
			return
		}

		hash, err := store.GetRegistrationTokenHash(r.Context(), exeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		if hash == "" {
			// Don't distinguish unknown-id from bad-token to upstream.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(bearer)); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		base := publicWSBaseURL
		if base == "" {
			base = synthBaseURL(r)
		}
		wsURL := base + "/codex-exec/" + url.PathEscape(exeID) + "?token=" + url.QueryEscape(bearer)

		writeJSON(w, http.StatusOK, cloudRegisterResponse{
			ID:         exeID,
			ExecutorID: exeID,
			URL:        wsURL,
		})
	}
}

// extractBearer returns the Authorization: Bearer <token> value.
func extractBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimPrefix(h, prefix), true
}

// synthBaseURL composes a wss:// base from the incoming request's Host.
// Falls back to ws:// for plain-HTTP requests (TLS-less dev).
func synthBaseURL(r *http.Request) string {
	scheme := "wss"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "ws"
	}
	return scheme + "://" + r.Host
}

