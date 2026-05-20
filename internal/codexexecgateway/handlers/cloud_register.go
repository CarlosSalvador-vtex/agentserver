package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// AgentserverValidator calls agentserver's /internal/codex-auth/validate
// to verify codex 0.132 Bearer / AgentAssertion auth on cloud register.
type AgentserverValidator struct {
	BaseURL        string // e.g. "http://agentserver.agentserver.svc:8080"
	InternalSecret string
	HTTPClient     *http.Client // optional; nil → default with 5s timeout
}

// Validate POSTs the request body to agentserver and returns the
// resolved user_id, or an error if validation fails.
func (v *AgentserverValidator) Validate(ctx context.Context, req map[string]string) (userID string, err error) {
	if v.BaseURL == "" {
		return "", fmt.Errorf("validator not configured")
	}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		v.BaseURL+"/internal/codex-auth/validate", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Internal-Secret", v.InternalSecret)
	client := v.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("validate: %w", err)
	}
	defer resp.Body.Close()
	var rb struct {
		UserID string `json:"user_id"`
		Error  string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&rb)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("validate: %s (status %d)", rb.Error, resp.StatusCode)
	}
	return rb.UserID, nil
}

// CloudRegister handles POST /cloud/executor/{exe_id}/register.
//
// Auth: prefers codex 0.132+ schemes (Bearer access_token or
// AgentAssertion) validated via agentserver. Falls back to legacy
// bcrypt bearer token (codex < 0.132) for backward compat.
//
// Our existing inbound handler at `/codex-exec/{exe_id}?token=...` is
// the actual ws endpoint; this handler verifies the bearer once and
// returns that URL with the token plumbed through.
//
// publicWSBaseURL is the externally-visible wss:// origin (e.g.
// "wss://codex-exec.agent.cs.ac.cn:443"). When empty, the response URL
// is synthesised from r.Host with wss scheme — best-effort fallback for
// dev / direct in-cluster use.
func CloudRegister(store CloudRegisterStore, publicWSBaseURL string, validator AgentserverValidator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		exeID := chi.URLParam(r, "exe_id")
		if exeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exe_id required"})
			return
		}

		// 1. New codex 0.132+ path: try Bearer (ChatGPT) or
		//    AgentAssertion (Agent Identity) via agentserver.
		authHeader := r.Header.Get("Authorization")
		if userID, ok := classifyAndValidate(r.Context(), validator,
			authHeader, r.Header.Get("ChatGPT-Account-ID")); ok {
			if err := assertExeOwnedByUser(r.Context(), store, exeID, userID); err != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
				return
			}
			respondWithWSURL(w, r, exeID, authHeader, publicWSBaseURL)
			return
		}

		// 2. Legacy bcrypt bearer path for codex < 0.132 — kept until
		//    we drop support, then this block goes away.
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
		if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(bearer)) != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		respondWithWSURL(w, r, exeID, "Bearer "+bearer, publicWSBaseURL)
	}
}

// classifyAndValidate inspects the auth header and calls agentserver
// with the appropriate scheme. Returns userID + ok=true on match.
// Returns ok=false on any failure (legacy bcrypt path will then be tried).
func classifyAndValidate(ctx context.Context, v AgentserverValidator, authHeader, accountID string) (string, bool) {
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		// ChatGPT-mode bearer requests always come with the
		// ChatGPT-Account-ID header (codex's BearerAuthProvider always
		// adds it). Forward it so agentserver can cross-check the
		// header against the token's owner.
		// Legacy bcrypt tokens have similar shape; we always try
		// delegating first and fall back to the bcrypt path on 401.
		uid, err := v.Validate(ctx, map[string]string{
			"scheme": "bearer", "token": token, "account_id": accountID,
		})
		if err == nil && uid != "" {
			return uid, true
		}
		return "", false
	}
	if strings.HasPrefix(authHeader, "AgentAssertion ") {
		assertion := strings.TrimPrefix(authHeader, "AgentAssertion ")
		uid, err := v.Validate(ctx, map[string]string{
			"scheme": "agent_assertion", "assertion": assertion, "account_id": accountID,
		})
		if err == nil && uid != "" {
			return uid, true
		}
		return "", false
	}
	return "", false
}

func assertExeOwnedByUser(ctx context.Context, store CloudRegisterStore, exeID, userID string) error {
	type ownerStore interface {
		UserIDForExecutor(ctx context.Context, exeID string) (string, error)
	}
	os, ok := store.(ownerStore)
	if !ok {
		return fmt.Errorf("store does not implement UserIDForExecutor")
	}
	owner, err := os.UserIDForExecutor(ctx, exeID)
	if err != nil {
		return fmt.Errorf("lookup owner: %w", err)
	}
	if owner == "" {
		return fmt.Errorf("executor %s not registered", exeID)
	}
	if owner != userID {
		return fmt.Errorf("executor %s not owned by user %s", exeID, userID)
	}
	return nil
}

func respondWithWSURL(w http.ResponseWriter, r *http.Request, exeID, authHeader, publicWSBaseURL string) {
	base := publicWSBaseURL
	if base == "" {
		base = synthBaseURL(r)
	}
	// The WS URL has its own bearer cap-token in the query string; the
	// auth header above only authorized the register call itself.
	tokenForWS := strings.TrimPrefix(strings.TrimPrefix(authHeader, "Bearer "), "AgentAssertion ")
	wsURL := base + "/codex-exec/" + url.PathEscape(exeID) + "?token=" +
		url.QueryEscape(tokenForWS)
	writeJSON(w, http.StatusOK, cloudRegisterResponse{
		ID: exeID, ExecutorID: exeID, URL: wsURL,
	})
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
