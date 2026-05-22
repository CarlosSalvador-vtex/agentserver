package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/clientmeta"
	"github.com/go-chi/chi/v5"
)

// CloudRegisterStore is the subset of *codexexecgateway.Store the
// upstream-compat /cloud/executor/{id}/register handler needs.
// Ownership lookup is asserted via the ownerStore type-assertion in
// assertExeOwnedByUser; the meta-update is optional and skipped if
// the store doesn't implement clientMetaStore.
type CloudRegisterStore interface{}

// clientMetaStore is optionally implemented by CloudRegisterStore values
// to receive codex client metadata captured at register time (UA, IP,
// version, OS). The ws upgrade carries no UA, so this is the only place
// to get it on the codex 0.132+ wire.
type clientMetaStore interface {
	UpdateClientMetaFromRegister(ctx context.Context, exeID, clientIP, clientUA, codexVersion, osStr string) error
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

// ErrValidatorUnavailable is returned by Validate when the validator
// itself is unreachable (network error, 5xx, or 408/429 transient
// status). Callers should treat this as retryable — surface as 503 to
// the codex client so it loops register-retry instead of bailing with
// an auth error.
var ErrValidatorUnavailable = fmt.Errorf("validator unavailable")

// Validate POSTs the request body to agentserver and returns the
// resolved user_id, or an error if validation fails. Returns
// ErrValidatorUnavailable (wrapped) for transient transport / 5xx
// failures so callers can distinguish "auth said no" (401) from
// "couldn't ask" (503 — retryable during rolling deploys).
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
		// Network error: connection refused, DNS failure, timeout, etc.
		// Treat as transient — the agentserver pod may be mid-restart.
		return "", fmt.Errorf("%w: %v", ErrValidatorUnavailable, err)
	}
	defer resp.Body.Close()
	var rb struct {
		UserID string `json:"user_id"`
		Error  string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&rb)
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: status=%d body=%q", ErrValidatorUnavailable, resp.StatusCode, rb.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("validate: %s (status %d)", rb.Error, resp.StatusCode)
	}
	return rb.UserID, nil
}

// CloudRegister handles POST /cloud/executor/{exe_id}/register.
//
// Auth: codex 0.132+ schemes only — Bearer (ChatGPT access_token) or
// AgentAssertion (Agent Identity), validated via agentserver. The
// pre-0.132 bcrypt registration_token bearer is gone (PR removing it).
//
// On success, mints a short-lived HMAC ws ticket and returns
// `wss://.../codex-exec/{exe_id}?token=<ticket>`. The inbound ws
// handler verifies the ticket signature locally — no DB hop, no JWT
// verify, no validator round-trip.
//
// publicWSBaseURL is the externally-visible wss:// origin (e.g.
// "wss://codex-exec.agent.cs.ac.cn:443"). When empty, the response URL
// is synthesised from r.Host with wss scheme — best-effort fallback for
// dev / direct in-cluster use.
func CloudRegister(store CloudRegisterStore, publicWSBaseURL string, validator AgentserverValidator, wsTicketSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// codex 0.132 path: /cloud/executor/{exe_id}/register
		// codex 0.133+ path: /cloud/environment/{env_id}/register
		// Same handler, same id semantics — just two URL shapes.
		exeID := chi.URLParam(r, "exe_id")
		if exeID == "" {
			exeID = chi.URLParam(r, "env_id")
		}
		if exeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exe_id required"})
			return
		}

		authHeader := r.Header.Get("Authorization")
		userID, vErr := classifyAndValidate(r.Context(), validator,
			authHeader, r.Header.Get("ChatGPT-Account-ID"))
		if vErr != nil {
			// 503 for transient validator unavailability so codex keeps
			// the register-retry loop alive across agentserver rolling
			// restarts. 401/403 would make codex exit the loop with an
			// auth error and drop the long-lived ws — every client
			// connector would disconnect on every deploy.
			if errors.Is(vErr, ErrValidatorUnavailable) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "validator unavailable, retry"})
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if err := assertExeOwnedByUser(r.Context(), store, exeID, userID); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		if meta, ok := store.(clientMetaStore); ok {
			ua := r.Header.Get("User-Agent")
			version, osStr := clientmeta.ParseCodexUA(ua)
			ip := clientmeta.ClientIP(r)
			// Failure here is non-fatal — the row already exists and
			// missing metadata just keeps the UI columns at "—".
			_ = meta.UpdateClientMetaFromRegister(r.Context(), exeID, ip, ua, version, osStr)
		}
		ticket, err := MintWSTicket(exeID, wsTicketSecret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint ws ticket: " + err.Error()})
			return
		}
		respondWithWSURL(w, r, exeID, ticket, publicWSBaseURL)
	}
}

// classifyAndValidate inspects the auth header and calls agentserver
// with the appropriate scheme. Returns (userID, nil) on success,
// (_, ErrValidatorUnavailable) when every retry to the validator was
// unreachable, and (_, non-nil) for other auth failures (treat as 401).
// An empty/unrecognized authHeader returns ("", nil) — caller treats
// that as 401 too.
//
// Codex `register_environment(...).await?` exits run_remote_environment
// on ANY non-2xx, so a 5xx during agentserver rolling restart would
// disconnect every codex client. To hide the brief restart window we
// retry transient validator failures here in-process; total wait stays
// under ~3s so the codex client side doesn't time out.
func classifyAndValidate(ctx context.Context, v AgentserverValidator, authHeader, accountID string) (string, error) {
	var scheme, credKey, cred string
	switch {
	case strings.HasPrefix(authHeader, "Bearer "):
		scheme = "bearer"
		credKey = "token"
		cred = strings.TrimPrefix(authHeader, "Bearer ")
	case strings.HasPrefix(authHeader, "AgentAssertion "):
		scheme = "agent_assertion"
		credKey = "assertion"
		cred = strings.TrimPrefix(authHeader, "AgentAssertion ")
	default:
		return "", nil
	}
	body := map[string]string{
		"scheme": scheme, credKey: cred, "account_id": accountID,
	}
	// 5 attempts: 0ms, 300ms, 600ms, 1200ms, 2400ms — total ~4.5s, well
	// over the kubelet rolling-restart RTT for an agentserver pod (1-2s).
	var lastErr error
	backoff := 300 * time.Millisecond
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		uid, err := v.Validate(ctx, body)
		if err == nil {
			return uid, nil
		}
		lastErr = err
		if !errors.Is(err, ErrValidatorUnavailable) {
			// Hard auth failure — no point retrying.
			return "", err
		}
	}
	return "", lastErr
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

func respondWithWSURL(w http.ResponseWriter, r *http.Request, exeID, ticket, publicWSBaseURL string) {
	base := publicWSBaseURL
	if base == "" {
		base = synthBaseURL(r)
	}
	wsURL := base + "/codex-exec/" + url.PathEscape(exeID) + "?token=" + url.QueryEscape(ticket)
	writeJSON(w, http.StatusOK, cloudRegisterResponse{
		ID: exeID, ExecutorID: exeID, URL: wsURL,
	})
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
