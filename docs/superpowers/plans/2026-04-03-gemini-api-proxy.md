# Gemini API Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Gemini API proxy support to the LLM proxy service so that `/v1beta/*` requests are forwarded to Google's Gemini API with usage tracking.

**Architecture:** Raw HTTP reverse proxy (same pattern as the existing Anthropic handler). Gemini requests arrive at `/v1beta/*`, are authenticated via the existing proxy token flow, and forwarded to `generativelanguage.googleapis.com` with the real API key injected. Usage metadata is extracted from responses (both streaming SSE and non-streaming JSON) and stored in the existing `usage` table with `provider = "gemini"`.

**Tech Stack:** Go stdlib `net/http/httputil.ReverseProxy`, SSE parsing, existing llmproxy infrastructure (auth, trace, store, modelserver token cache).

**Spec:** `docs/superpowers/specs/2026-04-03-gemini-api-proxy-design.md`

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/llmproxy/config.go` | **Modify** — Add `GeminiBaseURL` and `GeminiAPIKey` config fields |
| `internal/llmproxy/trace.go` | **Modify** — Add Gemini trace/request ID generators with `gt-`/`gr-` prefixes |
| `internal/llmproxy/gemini_parser.go` | **Create** — Gemini response parsing (non-streaming JSON + streaming SSE events) |
| `internal/llmproxy/gemini_parser_test.go` | **Create** — Tests for Gemini response parsing |
| `internal/llmproxy/gemini_stream.go` | **Create** — `geminiStreamInterceptor` (SSE passthrough with usage extraction) |
| `internal/llmproxy/gemini_stream_test.go` | **Create** — Tests for Gemini stream interceptor |
| `internal/llmproxy/gemini.go` | **Create** — `handleGeminiProxy()` handler + `recordGeminiUsage()` |
| `internal/llmproxy/server.go` | **Modify** — Add `/v1beta/*` route |
| `cmd/llmproxy/main.go` | **Modify** — Relax startup validation to accept Gemini-only config |

---

### Task 1: Config + Trace ID Generators

**Files:**
- Modify: `internal/llmproxy/config.go`
- Modify: `internal/llmproxy/trace.go`

- [ ] **Step 1: Add Gemini fields to Config**

In `internal/llmproxy/config.go`, add two fields to the `Config` struct and load them in `LoadConfigFromEnv()`:

```go
// In Config struct, add after AnthropicAuthToken:
GeminiBaseURL string // upstream Gemini API URL
GeminiAPIKey  string // real Google API key for Gemini
```

```go
// In LoadConfigFromEnv(), add after the AnthropicAuthToken line:
GeminiBaseURL: envOr("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),
GeminiAPIKey:  os.Getenv("GEMINI_API_KEY"),
```

- [ ] **Step 2: Add Gemini trace/request ID generators**

In `internal/llmproxy/trace.go`, add new constants and functions:

```go
// Add to the const block:
geminiTraceIDPrefix   = "gt-"
geminiRequestIDPrefix = "gr-"
```

```go
// Add after GenerateRequestID():

// GenerateGeminiTraceID creates a new trace ID with the "gt-" prefix.
func GenerateGeminiTraceID() string {
	return geminiTraceIDPrefix + uuid.New().String()
}

// GenerateGeminiRequestID creates a new request ID with the "gr-" prefix.
func GenerateGeminiRequestID() string {
	return geminiRequestIDPrefix + uuid.New().String()
}
```

- [ ] **Step 3: Add ExtractGeminiTraceID method**

In `internal/llmproxy/trace.go`, add a Gemini-specific trace extraction that uses `gt-` prefix for auto-generated IDs:

```go
// ExtractGeminiTraceID extracts a trace ID from the request for Gemini.
// Same priority as ExtractTraceID but uses gt- prefix for auto-generated IDs.
func (s *Server) ExtractGeminiTraceID(r *http.Request, body []byte) (string, string) {
	// 1. Check custom trace header.
	if s.config.TraceHeader != "" {
		if hdr := r.Header.Get(s.config.TraceHeader); hdr != "" {
			return hdr, "header"
		}
	}

	// 2. Try OpenCode x-opencode-session header.
	if hdr := r.Header.Get("x-opencode-session"); hdr != "" {
		return geminiTraceIDPrefix + hdr, "opencode"
	}

	// 3. Auto-generate.
	return GenerateGeminiTraceID(), "auto"
}
```

- [ ] **Step 4: Verify it compiles**

Run: `cd /root/agentserver && go build ./internal/llmproxy/...`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/llmproxy/config.go internal/llmproxy/trace.go
git commit -m "feat(llmproxy): add Gemini config fields and trace ID generators"
```

---

### Task 2: Gemini Response Parser

**Files:**
- Create: `internal/llmproxy/gemini_parser.go`
- Create: `internal/llmproxy/gemini_parser_test.go`

- [ ] **Step 1: Write tests for non-streaming parsing**

Create `internal/llmproxy/gemini_parser_test.go`:

```go
package llmproxy

import (
	"testing"
)

func TestParseGeminiNonStreamingResponse(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantModel     string
		wantInput     int64
		wantOutput    int64
		wantCacheRead int64
		wantErr       bool
	}{
		{
			name: "basic response",
			body: `{
				"candidates": [{"content": {"parts": [{"text": "hello"}]}}],
				"usageMetadata": {
					"promptTokenCount": 100,
					"candidatesTokenCount": 50,
					"cachedContentTokenCount": 10,
					"totalTokenCount": 160
				},
				"modelVersion": "gemini-2.5-flash"
			}`,
			wantModel:     "gemini-2.5-flash",
			wantInput:     100,
			wantOutput:    50,
			wantCacheRead: 10,
		},
		{
			name: "no usage metadata",
			body: `{
				"candidates": [{"content": {"parts": [{"text": "hello"}]}}],
				"modelVersion": "gemini-2.5-flash"
			}`,
			wantModel: "gemini-2.5-flash",
		},
		{
			name:    "invalid json",
			body:    `not json`,
			wantErr: true,
		},
		{
			name: "empty model version",
			body: `{
				"candidates": [],
				"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 3}
			}`,
			wantModel:  "",
			wantInput:  5,
			wantOutput: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, usage, err := ParseGeminiNonStreamingResponse([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if usage.PromptTokenCount != tt.wantInput {
				t.Errorf("input = %d, want %d", usage.PromptTokenCount, tt.wantInput)
			}
			if usage.CandidatesTokenCount != tt.wantOutput {
				t.Errorf("output = %d, want %d", usage.CandidatesTokenCount, tt.wantOutput)
			}
			if usage.CachedContentTokenCount != tt.wantCacheRead {
				t.Errorf("cacheRead = %d, want %d", usage.CachedContentTokenCount, tt.wantCacheRead)
			}
		})
	}
}

func TestParseGeminiStreamChunk(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantModel string
		wantUsage bool
		wantParts bool
	}{
		{
			name: "chunk with usage and content",
			data: `{
				"candidates": [{"content": {"parts": [{"text": "hi"}], "role": "model"}}],
				"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5},
				"modelVersion": "gemini-2.5-flash"
			}`,
			wantModel: "gemini-2.5-flash",
			wantUsage: true,
			wantParts: true,
		},
		{
			name: "chunk with usage only, no content parts",
			data: `{
				"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 50},
				"modelVersion": "gemini-2.5-flash"
			}`,
			wantModel: "gemini-2.5-flash",
			wantUsage: true,
			wantParts: false,
		},
		{
			name:      "empty chunk",
			data:      `{}`,
			wantModel: "",
			wantUsage: false,
			wantParts: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, usage, hasUsage, hasParts := ParseGeminiStreamChunk([]byte(tt.data))
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if hasUsage != tt.wantUsage {
				t.Errorf("hasUsage = %v, want %v", hasUsage, tt.wantUsage)
			}
			if hasParts != tt.wantParts {
				t.Errorf("hasParts = %v, want %v", hasParts, tt.wantParts)
			}
			if hasUsage && usage.PromptTokenCount == 0 {
				t.Error("expected non-zero promptTokenCount when hasUsage is true")
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/llmproxy/ -run TestParseGemini -v`
Expected: compilation errors (functions not defined).

- [ ] **Step 3: Implement parser**

Create `internal/llmproxy/gemini_parser.go`:

```go
package llmproxy

import "encoding/json"

// GeminiUsageMetadata holds token counts from a Gemini API response.
type GeminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount,omitempty"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount,omitempty"`
	TotalTokenCount         int64 `json:"totalTokenCount,omitempty"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount,omitempty"`
}

// geminiResponse is a minimal structure for Gemini generateContent responses.
type geminiResponse struct {
	Candidates []struct {
		Content *struct {
			Parts []json.RawMessage `json:"parts,omitempty"`
		} `json:"content,omitempty"`
	} `json:"candidates,omitempty"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// ParseGeminiNonStreamingResponse parses a complete JSON response from Gemini generateContent.
func ParseGeminiNonStreamingResponse(body []byte) (model string, usage GeminiUsageMetadata, err error) {
	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", GeminiUsageMetadata{}, err
	}
	if resp.UsageMetadata != nil {
		usage = *resp.UsageMetadata
	}
	return resp.ModelVersion, usage, nil
}

// ParseGeminiStreamChunk parses a single SSE data payload from a Gemini streaming response.
// Returns model, usage, whether usage was present, and whether content parts were present.
func ParseGeminiStreamChunk(data []byte) (model string, usage GeminiUsageMetadata, hasUsage bool, hasParts bool) {
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", GeminiUsageMetadata{}, false, false
	}
	model = resp.ModelVersion
	if resp.UsageMetadata != nil {
		usage = *resp.UsageMetadata
		hasUsage = true
	}
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil && len(resp.Candidates[0].Content.Parts) > 0 {
		hasParts = true
	}
	return model, usage, hasUsage, hasParts
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/llmproxy/ -run TestParseGemini -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llmproxy/gemini_parser.go internal/llmproxy/gemini_parser_test.go
git commit -m "feat(llmproxy): add Gemini response parser with tests"
```

---

### Task 3: Gemini Stream Interceptor

**Files:**
- Create: `internal/llmproxy/gemini_stream.go`
- Create: `internal/llmproxy/gemini_stream_test.go`

- [ ] **Step 1: Write tests for the stream interceptor**

Create `internal/llmproxy/gemini_stream_test.go`:

```go
package llmproxy

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestGeminiStreamInterceptor(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1},"modelVersion":"gemini-2.5-flash"}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5},"modelVersion":"gemini-2.5-flash"}`,
		"",
		`data: {"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30},"modelVersion":"gemini-2.5-flash"}`,
		"",
	}, "\n")

	var gotModel string
	var gotUsage GeminiUsageMetadata
	var gotTTFT int64
	called := false

	inner := io.NopCloser(strings.NewReader(sseData))
	si := newGeminiStreamInterceptor(inner, time.Now(), func(model string, usage GeminiUsageMetadata, ttft int64) {
		gotModel = model
		gotUsage = usage
		gotTTFT = ttft
		called = true
	})

	// Read all data through the interceptor.
	out, err := io.ReadAll(si)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	// Data must pass through unchanged.
	if string(out) != sseData {
		t.Errorf("data was modified during passthrough")
	}

	if !called {
		t.Fatal("onComplete was not called")
	}
	if gotModel != "gemini-2.5-flash" {
		t.Errorf("model = %q, want %q", gotModel, "gemini-2.5-flash")
	}
	// Last chunk's usage should win.
	if gotUsage.PromptTokenCount != 10 {
		t.Errorf("input = %d, want 10", gotUsage.PromptTokenCount)
	}
	if gotUsage.CandidatesTokenCount != 20 {
		t.Errorf("output = %d, want 20", gotUsage.CandidatesTokenCount)
	}
	if gotTTFT <= 0 {
		t.Errorf("ttft = %d, want > 0", gotTTFT)
	}
}

func TestGeminiStreamInterceptor_NoContent(t *testing.T) {
	// Stream with only usage metadata, no content parts — TTFT should be 0.
	sseData := "data: {\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":0},\"modelVersion\":\"gemini-2.5-flash\"}\n\n"

	var gotTTFT int64
	called := false

	inner := io.NopCloser(strings.NewReader(sseData))
	si := newGeminiStreamInterceptor(inner, time.Now(), func(model string, usage GeminiUsageMetadata, ttft int64) {
		gotTTFT = ttft
		called = true
	})

	io.ReadAll(si)

	if !called {
		t.Fatal("onComplete was not called")
	}
	if gotTTFT != 0 {
		t.Errorf("ttft = %d, want 0 (no content parts seen)", gotTTFT)
	}
}

func TestGeminiStreamInterceptor_Close(t *testing.T) {
	sseData := "data: {\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":3},\"modelVersion\":\"gemini-2.5-flash\"}\n\n"

	called := false
	inner := io.NopCloser(strings.NewReader(sseData))
	si := newGeminiStreamInterceptor(inner, time.Now(), func(model string, usage GeminiUsageMetadata, ttft int64) {
		called = true
	})

	// Read partial, then close.
	buf := make([]byte, 10)
	si.Read(buf)
	si.Close()

	if !called {
		t.Fatal("onComplete was not called on Close")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/llmproxy/ -run TestGeminiStream -v`
Expected: compilation errors (types not defined).

- [ ] **Step 3: Implement stream interceptor**

Create `internal/llmproxy/gemini_stream.go`:

```go
package llmproxy

import (
	"bytes"
	"io"
	"time"
)

// geminiStreamInterceptor wraps a response body, transparently passing through
// all bytes while parsing SSE events to extract Gemini usage data and TTFT.
type geminiStreamInterceptor struct {
	inner      io.ReadCloser
	buf        bytes.Buffer
	startTime  time.Time
	model      string
	usage      GeminiUsageMetadata
	ttft       int64
	gotFirst   bool
	onComplete func(model string, usage GeminiUsageMetadata, ttft int64)
	completed  bool
}

func newGeminiStreamInterceptor(inner io.ReadCloser, startTime time.Time, onComplete func(string, GeminiUsageMetadata, int64)) *geminiStreamInterceptor {
	return &geminiStreamInterceptor{
		inner:      inner,
		startTime:  startTime,
		onComplete: onComplete,
	}
}

func (si *geminiStreamInterceptor) Read(p []byte) (int, error) {
	n, err := si.inner.Read(p)
	if n > 0 {
		si.buf.Write(p[:n])
		si.processLines()
	}
	if err == io.EOF {
		si.flushRemaining()
		si.finish()
	}
	return n, err
}

func (si *geminiStreamInterceptor) Close() error {
	si.flushRemaining()
	si.finish()
	return si.inner.Close()
}

func (si *geminiStreamInterceptor) processLines() {
	for {
		line, err := si.buf.ReadBytes('\n')
		if err != nil {
			si.buf.Write(line)
			return
		}
		si.parseLine(line)
	}
}

func (si *geminiStreamInterceptor) flushRemaining() {
	if si.buf.Len() > 0 {
		si.parseLine(si.buf.Bytes())
		si.buf.Reset()
	}
}

func (si *geminiStreamInterceptor) parseLine(line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return
	}
	data := bytes.TrimPrefix(line, []byte("data: "))

	model, usage, hasUsage, hasParts := ParseGeminiStreamChunk(data)
	if model != "" {
		si.model = model
	}

	// TTFT: first chunk with content parts.
	if !si.gotFirst && hasParts {
		si.gotFirst = true
		si.ttft = time.Since(si.startTime).Milliseconds()
	}

	// Last chunk's usage wins (overwrite on each chunk that has it).
	if hasUsage {
		si.usage = usage
	}
}

func (si *geminiStreamInterceptor) finish() {
	if si.completed {
		return
	}
	si.completed = true
	if si.onComplete != nil {
		si.onComplete(si.model, si.usage, si.ttft)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/agentserver && go test ./internal/llmproxy/ -run TestGeminiStream -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llmproxy/gemini_stream.go internal/llmproxy/gemini_stream_test.go
git commit -m "feat(llmproxy): add Gemini SSE stream interceptor with tests"
```

---

### Task 4: Gemini Proxy Handler

**Files:**
- Create: `internal/llmproxy/gemini.go`

- [ ] **Step 1: Create the Gemini proxy handler**

Create `internal/llmproxy/gemini.go`:

```go
package llmproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// handleGeminiProxy proxies Gemini API requests, recording token usage and trace data.
func (s *Server) handleGeminiProxy(w http.ResponseWriter, r *http.Request) {
	// 1. Validate proxy token (x-api-key header).
	proxyToken := r.Header.Get("x-api-key")
	if proxyToken == "" {
		http.Error(w, "missing api key", http.StatusUnauthorized)
		return
	}

	sbx, err := s.ValidateProxyToken(r.Context(), proxyToken)
	if err != nil {
		s.logger.Error("token validation failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sbx == nil {
		http.Error(w, "invalid api key", http.StatusUnauthorized)
		return
	}
	if sbx.Status != "running" && sbx.Status != "creating" {
		http.Error(w, "sandbox not active", http.StatusForbidden)
		return
	}

	// 2. Determine upstream target.
	targetURL := s.config.GeminiBaseURL
	useModelserver := sbx.ModelserverUpstreamURL != ""
	if useModelserver {
		targetURL = sbx.ModelserverUpstreamURL
	} else if s.config.GeminiAPIKey == "" {
		http.Error(w, "gemini not configured", http.StatusServiceUnavailable)
		return
	}

	// 3. Check RPD quota (only for generate endpoints, skip for modelserver).
	isGenerateEndpoint := strings.Contains(r.URL.Path, ":generateContent") || strings.Contains(r.URL.Path, ":streamGenerateContent")
	if isGenerateEndpoint && !useModelserver {
		if exceeded, current, max := s.checkRPD(sbx.WorkspaceID); exceeded {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"code":    429,
					"message": fmt.Sprintf("workspace requests per day quota exceeded (%d/%d)", current, max),
					"status":  "RESOURCE_EXHAUSTED",
				},
			})
			return
		}
	}

	// 4. Read body for trace extraction.
	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Detect streaming from URL path.
	isStreaming := strings.Contains(r.URL.Path, ":streamGenerateContent")

	// 5. Extract trace ID.
	traceID, source := s.ExtractGeminiTraceID(r, bodyBytes)
	requestID := GenerateGeminiRequestID()

	logger := s.logger.With(
		"trace_id", traceID,
		"request_id", requestID,
		"sandbox_id", sbx.ID,
		"workspace_id", sbx.WorkspaceID,
	)

	// 6. Persist trace (only for generate endpoints).
	if isGenerateEndpoint && s.store != nil {
		if _, err := s.store.GetOrCreateTrace(traceID, sbx.ID, sbx.WorkspaceID, source); err != nil {
			logger.Error("failed to create trace", "error", err)
		}
	}

	// 7. Set up reverse proxy.
	target, err := url.Parse(targetURL)
	if err != nil {
		logger.Error("invalid upstream URL", "error", err)
		http.Error(w, "invalid upstream URL", http.StatusInternalServerError)
		return
	}

	// 7a. Pre-fetch modelserver token if needed.
	var msToken string
	if useModelserver {
		var tokenErr error
		msToken, tokenErr = s.fetchModelserverToken(sbx.WorkspaceID)
		if tokenErr != nil {
			logger.Error("failed to get modelserver token", "error", tokenErr)
			http.Error(w, "modelserver token unavailable", http.StatusBadGateway)
			return
		}
	}

	startTime := time.Now()

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = r.URL.Path
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = target.Host

			// Remove the proxy token header.
			req.Header.Del("x-api-key")

			if useModelserver {
				req.Header.Set("Authorization", "Bearer "+msToken)
			} else {
				req.Header.Set("x-goog-api-key", s.config.GeminiAPIKey)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if !isGenerateEndpoint {
				return nil
			}
			if isStreaming {
				return s.interceptGeminiStreaming(resp, sbx, traceID, requestID, logger, startTime)
			}
			return s.interceptGeminiNonStreaming(resp, sbx, traceID, requestID, logger, startTime)
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("proxy error", "error", err)
			http.Error(w, "proxy error", http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

// interceptGeminiNonStreaming reads the full response body, extracts usage, and records it.
func (s *Server) interceptGeminiNonStreaming(resp *http.Response, sbx *SandboxInfo, traceID, requestID string, logger *slog.Logger, startTime time.Time) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		logger.Error("failed to read response body", "error", err)
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	model, usage, err := ParseGeminiNonStreamingResponse(body)
	if err != nil {
		logger.Warn("failed to parse gemini response", "error", err)
		return nil
	}

	duration := time.Since(startTime).Milliseconds()
	s.recordGeminiUsage(sbx, traceID, requestID, model, usage, false, duration, 0, logger)
	return nil
}

// interceptGeminiStreaming wraps the response body with a Gemini stream interceptor.
func (s *Server) interceptGeminiStreaming(resp *http.Response, sbx *SandboxInfo, traceID, requestID string, logger *slog.Logger, startTime time.Time) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	resp.Body = newGeminiStreamInterceptor(resp.Body, startTime, func(model string, usage GeminiUsageMetadata, ttft int64) {
		duration := time.Since(startTime).Milliseconds()
		s.recordGeminiUsage(sbx, traceID, requestID, model, usage, true, duration, ttft, logger)
	})
	return nil
}

// recordGeminiUsage persists a Gemini usage record and logs it.
func (s *Server) recordGeminiUsage(sbx *SandboxInfo, traceID, requestID, model string, usage GeminiUsageMetadata, streaming bool, duration, ttft int64, logger *slog.Logger) {
	logger.Info("gemini request completed",
		"model", model,
		"input_tokens", usage.PromptTokenCount,
		"output_tokens", usage.CandidatesTokenCount,
		"cache_read_input_tokens", usage.CachedContentTokenCount,
		"streaming", streaming,
		"duration", duration,
		"ttft", ttft,
	)

	if s.store == nil {
		return
	}

	u := TokenUsage{
		ID:                   requestID,
		TraceID:              traceID,
		SandboxID:            sbx.ID,
		WorkspaceID:          sbx.WorkspaceID,
		Provider:             "gemini",
		Model:                model,
		InputTokens:          usage.PromptTokenCount,
		OutputTokens:         usage.CandidatesTokenCount,
		CacheReadInputTokens: usage.CachedContentTokenCount,
		Streaming:            streaming,
		Duration:             duration,
		TTFT:                 ttft,
		CreatedAt:            time.Now(),
	}

	if err := s.store.RecordUsage(u); err != nil {
		logger.Error("failed to record usage", "error", err)
	}
	if err := s.store.UpdateTraceActivity(traceID); err != nil {
		logger.Error("failed to update trace activity", "error", err)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /root/agentserver && go build ./internal/llmproxy/...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/llmproxy/gemini.go
git commit -m "feat(llmproxy): add Gemini proxy handler with modelserver support"
```

---

### Task 5: Route Registration + Startup Validation

**Files:**
- Modify: `internal/llmproxy/server.go`
- Modify: `cmd/llmproxy/main.go`

- [ ] **Step 1: Add Gemini route to server.go**

In `internal/llmproxy/server.go`, add the Gemini route after the existing Anthropic route (after line 48):

```go
// Gemini API proxy (all /v1beta/* paths).
r.HandleFunc("/v1beta/*", s.handleGeminiProxy)
```

The `Routes()` function should now look like:

```go
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Anthropic API proxy (all /v1/* paths).
	r.HandleFunc("/v1/*", s.handleAnthropicProxy)

	// Gemini API proxy (all /v1beta/* paths).
	r.HandleFunc("/v1beta/*", s.handleGeminiProxy)

	// Internal API (requires database, network-isolated — only agentserver can reach these).
	r.Route("/internal", func(r chi.Router) {
		r.Use(s.requireStore)
		r.Get("/usage", s.handleQueryUsage)
		r.Get("/traces", s.handleQueryTraces)
		r.Get("/traces/{id}", s.handleGetTrace)
		r.Get("/quotas/{workspace_id}", s.handleGetWorkspaceQuota)
		r.Put("/quotas/{workspace_id}", s.handleSetWorkspaceQuota)
		r.Delete("/quotas/{workspace_id}", s.handleDeleteWorkspaceQuota)
	})

	return r
}
```

- [ ] **Step 2: Relax startup validation in main.go**

In `cmd/llmproxy/main.go`, replace the Anthropic-only validation (lines 19-21):

```go
// Before:
if cfg.AnthropicAPIKey == "" && cfg.AnthropicAuthToken == "" {
	log.Fatal("either ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN is required")
}

// After:
hasAnthropic := cfg.AnthropicAPIKey != "" || cfg.AnthropicAuthToken != ""
hasGemini := cfg.GeminiAPIKey != ""
if !hasAnthropic && !hasGemini {
	log.Fatal("at least one LLM provider must be configured: set ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN and/or GEMINI_API_KEY")
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /root/agentserver && go build ./cmd/llmproxy/`
Expected: no errors.

- [ ] **Step 4: Run all existing tests**

Run: `cd /root/agentserver && go test ./internal/llmproxy/ -v`
Expected: all tests PASS (including the new Gemini parser and stream tests from Tasks 2-3).

- [ ] **Step 5: Commit**

```bash
git add internal/llmproxy/server.go cmd/llmproxy/main.go
git commit -m "feat(llmproxy): register Gemini route and relax startup validation"
```
