package codexappgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelserverClient fetches per-workspace ModelServer OAuth access tokens
// from agentserver's internal API. The endpoint
// (`GET /internal/workspaces/{id}/modelserver-token`) auto-refreshes
// expired tokens via the workspace's stored refresh token.
//
// Tokens are short-lived (~1h depending on the upstream OAuth provider).
// The gateway fetches one at spawn time and injects it as the spawned
// codex's CODEX_API_KEY (or whatever ModelProviderEnvKey resolves to);
// if the codex subprocess outlives the token, LLM calls start returning
// 401. Operators can `POST /admin/sessions/restart` to force a respawn
// with a fresh token. Auto-refresh inside the running subprocess is a
// follow-up.
type ModelserverClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewModelserverClient(baseURL string) *ModelserverClient {
	return &ModelserverClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// FetchToken returns the workspace's current ModelServer access token.
// Returns ("", nil) when the workspace has no ModelServer connection yet —
// callers should fall back to a static key (or fail-soft).
func (c *ModelserverClient) FetchToken(ctx context.Context, workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("modelserver client: workspaceID required")
	}
	u := c.baseURL + "/internal/workspaces/" + workspaceID + "/modelserver-token"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("modelserver token fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Workspace doesn't have a ModelServer connection — return empty,
		// let caller decide how to degrade.
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("modelserver token fetch: status=%d body=%q", resp.StatusCode, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode modelserver token: %w", err)
	}
	return out.AccessToken, nil
}
