package envmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// connectedEntry mirrors the JSON shape codex-exec-gateway's
// /api/exec-gateway/connected returns. Note: per v0.54.0, exe_id is
// returned by the API but stripped from anything we send to the LLM.
type connectedEntry struct {
	ExeID       string `json:"exe_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	LastSeenAt  string `json:"last_seen_at,omitempty"`
}

// NameResolver maintains a workspace-scoped name → exe_id map by
// periodically refreshing from app-gateway's /internal/connected. Tools
// that take an env_id (semantically a name) call Resolve to get the
// underlying exe_id for BridgePool.Get.
//
// Cache strategy:
//   - First Resolve populates the cache.
//   - Subsequent Resolves use the cache if its age is under cacheTTL.
//   - A Resolve miss forces an immediate refresh before erroring.
type NameResolver struct {
	url        string // loopback /internal/connected
	token      string // X-Loopback-Token
	httpClient *http.Client
	logger     *slog.Logger
	cacheTTL   time.Duration

	mu        sync.Mutex
	cache     map[string]string // name → exe_id
	cachedAt  time.Time
}

const nameResolverCacheTTL = 10 * time.Second

func NewNameResolver(loopbackURL, loopbackToken string, logger *slog.Logger) *NameResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &NameResolver{
		url:        loopbackURL,
		token:      loopbackToken,
		httpClient: &http.Client{Timeout: 3 * time.Second},
		logger:     logger,
		cacheTTL:   nameResolverCacheTTL,
		cache:      map[string]string{},
	}
}

// fetch reads the current connected list from the loopback endpoint.
// Returns the raw entries (caller decides whether to update cache).
func (r *NameResolver) fetch(ctx context.Context) ([]connectedEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Loopback-Token", r.token)
	req.Header.Set("Accept", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var entries []connectedEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return entries, nil
}

// refresh fetches and overwrites the cache. Returns the entries so the
// caller (e.g. list_environments) can reuse the result.
func (r *NameResolver) refresh(ctx context.Context) ([]connectedEntry, error) {
	entries, err := r.fetch(ctx)
	if err != nil {
		return nil, err
	}
	fresh := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.Name != "" {
			fresh[e.Name] = e.ExeID
		}
	}
	r.mu.Lock()
	r.cache = fresh
	r.cachedAt = time.Now()
	r.mu.Unlock()
	return entries, nil
}

// Resolve returns the exe_id bound to name in the current workspace.
// If name isn't in the cache, refreshes once before reporting
// not-found.
func (r *NameResolver) Resolve(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("env name required")
	}
	r.mu.Lock()
	exeID, ok := r.cache[name]
	stale := time.Since(r.cachedAt) > r.cacheTTL
	r.mu.Unlock()
	if ok && !stale {
		return exeID, nil
	}
	// Either cache miss or stale — refresh.
	if _, err := r.refresh(ctx); err != nil {
		// On refresh failure, fall back to whatever was in the cache.
		if ok {
			return exeID, nil
		}
		return "", fmt.Errorf("refresh: %w", err)
	}
	r.mu.Lock()
	exeID, ok = r.cache[name]
	r.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no environment named %q (run list_environments to see what's available)", name)
	}
	return exeID, nil
}

// LLMView returns the entries reshaped for the LLM (omits exe_id).
// Always refreshes to keep the LLM's view fresh.
func (r *NameResolver) LLMView(ctx context.Context) ([]byte, error) {
	entries, err := r.refresh(ctx)
	if err != nil {
		return nil, err
	}
	type llmEntry struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		IsDefault   bool   `json:"is_default,omitempty"`
		LastSeenAt  string `json:"last_seen,omitempty"`
	}
	out := make([]llmEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, llmEntry{
			Name: e.Name, Description: e.Description,
			IsDefault: e.IsDefault, LastSeenAt: e.LastSeenAt,
		})
	}
	return json.Marshal(out)
}
