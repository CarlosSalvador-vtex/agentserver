// Package wstoken acquires per-workspace proxy tokens from agentserver and
// caches them in-process so cc-broker can stamp them onto every spawned
// Claude CLI's ANTHROPIC_AUTH_TOKEN env without hitting the network on
// every turn.
package wstoken

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client acquires and caches workspace proxy tokens from
// agentserver:/internal/workspace-token.
type Client struct {
	agentserverURL string
	internalSecret string
	httpClient     *http.Client

	mu    sync.RWMutex
	cache map[string]string // workspaceID → token
}

func New(agentserverURL, internalSecret string) *Client {
	return &Client{
		agentserverURL: agentserverURL,
		internalSecret: internalSecret,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		cache:          make(map[string]string),
	}
}

// GetOrCreate returns the workspace's proxy token, fetching it from
// agentserver on first request and caching the result. The agentserver
// endpoint is itself idempotent so concurrent first-time fetches converge.
func (c *Client) GetOrCreate(ctx context.Context, workspaceID string) (string, error) {
	c.mu.RLock()
	if tok, ok := c.cache[workspaceID]; ok {
		c.mu.RUnlock()
		return tok, nil
	}
	c.mu.RUnlock()

	tok, err := c.fetch(ctx, workspaceID)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cache[workspaceID] = tok
	c.mu.Unlock()
	return tok, nil
}

func (c *Client) fetch(ctx context.Context, workspaceID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"workspace_id": workspaceID})
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.agentserverURL+"/internal/workspace-token", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalSecret != "" {
		req.Header.Set("X-Internal-Secret", c.internalSecret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call agentserver: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workspace-token returned %d: %s", resp.StatusCode, respBody)
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("empty token in response")
	}
	return out.Token, nil
}
