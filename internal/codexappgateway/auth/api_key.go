// Package auth — workspace API-key validator that forwards Bearer secrets
// to agentserver's internal validate endpoint.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// APIKeyValidator calls agentserver's /internal/workspace-api-keys/validate
// to verify a Bearer secret and return the workspace_id + scopes it authorizes.
type APIKeyValidator struct {
	BaseURL        string // e.g. "http://agentserver.agentserver.svc:8080"
	InternalSecret string
	HTTPClient     *http.Client // optional; nil → default with 5s timeout
}

// ValidatedKey is the result of a successful Validate call.
type ValidatedKey struct {
	WorkspaceID string
	KeyID       string
	Scopes      []string
}

// NewAPIKeyValidator constructs a validator with sensible HTTP defaults.
func NewAPIKeyValidator(baseURL, internalSecret string) *APIKeyValidator {
	return &APIKeyValidator{
		BaseURL:        baseURL,
		InternalSecret: internalSecret,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Validate sends the secret to agentserver's internal validate RPC.
// Returns the workspace and scopes authorized by the key, or an error
// on any non-200 response (invalid key, revoked, wrong hash, etc.).
func (v *APIKeyValidator) Validate(ctx context.Context, secret string) (*ValidatedKey, error) {
	if v.BaseURL == "" {
		return nil, fmt.Errorf("api key validator not configured (no BaseURL)")
	}
	body, _ := json.Marshal(map[string]string{"secret": secret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.BaseURL+"/internal/workspace-api-keys/validate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("api key validate: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", v.InternalSecret)

	client := v.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api key validate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api key validate: status %d", resp.StatusCode)
	}

	var out struct {
		WorkspaceID string   `json:"workspace_id"`
		KeyID       string   `json:"key_id"`
		Scopes      []string `json:"scopes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("api key validate: decode response: %w", err)
	}
	if out.WorkspaceID == "" {
		return nil, fmt.Errorf("api key validate: empty workspace_id in response")
	}
	return &ValidatedKey{
		WorkspaceID: out.WorkspaceID,
		KeyID:       out.KeyID,
		Scopes:      out.Scopes,
	}, nil
}
