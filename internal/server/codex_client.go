package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CodexClient calls codex-app-gateway's POST /api/turns.
type CodexClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewCodexClient(baseURL, internalSecret string) *CodexClient {
	return &CodexClient{
		baseURL: baseURL,
		secret:  internalSecret,
		// Generous default — caller is the codex_im handler which has its
		// own per-turn timeout coming from the request body.
		http: &http.Client{Timeout: 6 * time.Minute},
	}
}

// CodexTurnRequest mirrors the spec'd /api/turns request body 1:1.
type CodexTurnRequest struct {
	WorkspaceID string          `json:"workspaceId"`
	ThreadID    *string         `json:"threadId,omitempty"`
	Params      json.RawMessage `json:"params"`
	TimeoutMs   int             `json:"timeoutMs,omitempty"`
}

// CodexTurnResponse mirrors the spec'd response.
type CodexTurnResponse struct {
	ThreadID  string               `json:"threadId"`
	Turn      json.RawMessage      `json:"turn,omitempty"`
	Transport *CodexTransportError `json:"transport,omitempty"`
}

type CodexTransportError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *CodexClient) RunTurn(ctx context.Context, req CodexTurnRequest) (*CodexTurnResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/turns", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		hreq.Header.Set("X-Internal-Secret", c.secret)
	}
	hresp, err := c.http.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("cxg: %w", err)
	}
	defer hresp.Body.Close()
	respBody, _ := io.ReadAll(hresp.Body)
	if hresp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cxg /api/turns status=%d body=%s", hresp.StatusCode, string(respBody))
	}
	var out CodexTurnResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode: %w body=%s", err, string(respBody))
	}
	return &out, nil
}
