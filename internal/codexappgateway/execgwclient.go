package codexappgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

// ExecGatewayClient calls codex-exec-gateway's internal HTTP API.
//
// Auth model: each request carries an `Authorization: Bearer <internal-shared-secret>`.
// Both gateway pods read the same secret out of the shared k8s Secret;
// see deploy/helm/agentserver/templates/codex-exec-gateway-secrets.yaml.
type ExecGatewayClient struct {
	baseURL    string
	bearer     string
	httpClient *http.Client
}

// NewExecGatewayClient constructs a client. baseURL is the http(s) base
// (e.g. "http://release-codex-exec-gateway:6060"); bearer is the
// shared-secret used for the `/api/exec-gateway` routes.
func NewExecGatewayClient(baseURL, bearer string) *ExecGatewayClient {
	return &ExecGatewayClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		bearer:     bearer,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Connected returns the intersection of (workspace's bound executors) ∩
// (currently-connected executors at the gateway). May be empty.
func (c *ExecGatewayClient) Connected(ctx context.Context, workspaceID string) ([]execmodel.ConnectedExecutor, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("execgwclient: workspaceID required")
	}
	q := url.Values{}
	q.Set("workspace_id", workspaceID)
	u := c.baseURL + "/api/exec-gateway/connected?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("execgwclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execgwclient: GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("execgwclient: GET %s: status=%d body=%q", u, resp.StatusCode, body)
	}
	var out []execmodel.ConnectedExecutor
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("execgwclient: decode response: %w", err)
	}
	return out, nil
}
