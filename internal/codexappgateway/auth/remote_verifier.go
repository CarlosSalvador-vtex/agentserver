// Package auth implements inbound bearer-token verification.
//
// Phase 2 default is RemoteVerifier: each ws connect POSTs the supplied
// bearer to agentserver's /api/internal/codex/tokens/verify, which owns
// the codex_remote_tokens table and applies bcrypt + expiry + revocation
// policy. This couples the gateway to agentserver's lifecycle but keeps
// the gateway stateless.
//
// HMACAuthenticator stays in the package as a break-glass / local-test
// implementation but is no longer used in chart-deployed pods.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized is returned by Verify when agentserver responds 401.
// Distinguishable so handlers can map directly to HTTP 401 without leaking
// other error reasons (network failure → 500, etc.).
var ErrUnauthorized = errors.New("auth: unauthorized")

// RemoteVerifier delegates token verification to agentserver's internal API.
type RemoteVerifier struct {
	baseURL    string
	bearer     string
	httpClient *http.Client
}

// NewRemoteVerifier constructs a verifier targeting agentserver's internal
// HTTP API. baseURL is the http base (e.g.
// "http://release-agentserver.namespace.svc:8080"); bearer is the value of
// INTERNAL_API_SECRET used as the X-Internal-Secret header.
func NewRemoteVerifier(baseURL, bearer string) *RemoteVerifier {
	return &RemoteVerifier{
		baseURL:    strings.TrimRight(baseURL, "/"),
		bearer:     bearer,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Verify implements Authenticator.
func (v *RemoteVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return Identity{}, fmt.Errorf("marshal verify body: %w", err)
	}
	url := v.baseURL + "/api/internal/codex/tokens/verify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Identity{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", v.bearer)
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("verify call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return Identity{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Identity{}, fmt.Errorf("verify call: status=%d body=%q", resp.StatusCode, b)
	}

	var out struct {
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Identity{}, fmt.Errorf("decode verify response: %w", err)
	}
	return Identity{UserID: out.UserID, WorkspaceID: out.WorkspaceID}, nil
}
