package envmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// listEnvironmentsSchema: empty object, no args.
var listEnvironmentsSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// ListEnvironmentsTool calls codex-app-gateway's loopback
// /internal/connected endpoint and returns the JSON list of
// currently-connected executors bound to this workspace.
type ListEnvironmentsTool struct {
	url           string // e.g. http://127.0.0.1:8080/internal/connected
	loopbackToken string
	httpClient    *http.Client
	logger        *slog.Logger
}

func NewListEnvironmentsTool(url, loopbackToken string, logger *slog.Logger) *ListEnvironmentsTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &ListEnvironmentsTool{
		url:           url,
		loopbackToken: loopbackToken,
		httpClient:    &http.Client{Timeout: 3 * time.Second},
		logger:        logger,
	}
}

func (t *ListEnvironmentsTool) Name() string { return "list_environments" }

func (t *ListEnvironmentsTool) Description() string {
	return "Return the list of environments (executors) currently connected to this workspace. " +
		"Each entry has env_id, description, is_default, and last_seen. Call this before any " +
		"shell/apply_patch/read_file/unified_exec tool to pick a target env_id."
}

func (t *ListEnvironmentsTool) InputSchema() json.RawMessage { return listEnvironmentsSchema }

func (t *ListEnvironmentsTool) Call(ctx context.Context, _ json.RawMessage) (MCPCallToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return errResult(fmt.Sprintf("list_environments: build request: %v", err)), nil
	}
	req.Header.Set("X-Loopback-Token", t.loopbackToken)
	req.Header.Set("Accept", "application/json")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return errResult(fmt.Sprintf("list_environments: %v (executor list temporarily unavailable; retry)", err)), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return errResult(fmt.Sprintf("list_environments: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errResult(fmt.Sprintf("list_environments: read body: %v", err)), nil
	}
	return MCPCallToolResult{
		Content: []MCPToolContent{{Type: "text", Text: string(body)}},
	}, nil
}
