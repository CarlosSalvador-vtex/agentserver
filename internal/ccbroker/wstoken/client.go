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

// CacheTTL bounds how long a fetched token is reused before re-validating
// against agentserver. Short enough that a token rotation / workspace
// deletion is picked up within a few minutes; long enough that steady-
// state turn dispatch never hits the network for token lookup.
const CacheTTL = 5 * time.Minute

// Client acquires and caches workspace proxy tokens from
// agentserver:/internal/workspace-token.
type Client struct {
	agentserverURL string
	internalSecret string
	httpClient     *http.Client

	mu    sync.RWMutex
	cache map[string]cacheEntry // workspaceID → token + freshness
}

type cacheEntry struct {
	token   string
	fetched time.Time
}

func New(agentserverURL, internalSecret string) *Client {
	return &Client{
		agentserverURL: agentserverURL,
		internalSecret: internalSecret,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		cache:          make(map[string]cacheEntry),
	}
}

// GetOrCreate returns the workspace's proxy token, fetching it from
// agentserver on first request (or after CacheTTL has elapsed) and caching
// the result. The agentserver endpoint is itself idempotent so concurrent
// first-time fetches converge.
func (c *Client) GetOrCreate(ctx context.Context, workspaceID string) (string, error) {
	c.mu.RLock()
	if e, ok := c.cache[workspaceID]; ok && time.Since(e.fetched) < CacheTTL {
		c.mu.RUnlock()
		return e.token, nil
	}
	c.mu.RUnlock()

	tok, err := c.fetch(ctx, workspaceID)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cache[workspaceID] = cacheEntry{token: tok, fetched: time.Now()}
	c.mu.Unlock()
	return tok, nil
}

// Invalidate drops the cached token for a workspace, forcing the next
// GetOrCreate to fetch fresh. Call this when an upstream returned 401 for
// the cached value (e.g., the workspace was deleted and its token row went
// with it). Safe to call for absent workspaces.
func (c *Client) Invalidate(workspaceID string) {
	c.mu.Lock()
	delete(c.cache, workspaceID)
	c.mu.Unlock()
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
