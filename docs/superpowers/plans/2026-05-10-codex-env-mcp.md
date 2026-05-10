# codex env-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `env-mcp` subcommand of a new `codex-app-gateway` Go
binary: a stdio MCP server that bridges `tools/call` traffic from a
spawned codex subprocess to one executor's exec-server endpoint via a
WebSocket connection to `/bridge/{exe_id}` on codex-exec-gateway.

**Architecture:** One env-mcp child process per executor per turn,
spawned by the codex-app-gateway's session worker (out of scope for
this plan; that worker is in the runtime plan). Stdio in/out talks
newline-delimited MCP JSON-RPC; one outbound WebSocket talks codex
exec-server JSON-RPC. Exposes a single `shell` MCP tool whose
`tools/call` is translated into an exec-server `process/start` plus a
`process/read` polling loop, returning aggregated stdout/stderr as MCP
`text` content with `isError` set from exit code.

**Tech Stack:** Go 1.26 (matches `/root/agentserver/go.mod`),
`nhooyr.io/websocket v1.8.17` (already in `go.mod`; defaults to no
`permessage-deflate` — required, see Open risks), `encoding/json`,
`encoding/base64`, `os`, `bufio`. **No external MCP SDK** — the surface
this plan implements (initialize / tools/list / tools/call /
prompts/list / resources/list) is small enough to inline cleanly.

**Spec:** `/root/agentserver/docs/superpowers/specs/2026-05-10-codex-gateway-mcp-rewrite.md`,
specifically § Subsystem 4 (env-mcp) and § PoC log § PoC #2.

**Working directory:** All tasks operate in `/root/agentserver` unless
otherwise noted.

**Module path:** `github.com/agentserver/agentserver`.

**Plan dependency note:** This plan stands alone — env-mcp is a
self-contained binary entry point that ships before the
codex-app-gateway runtime/session-worker. The runtime plan, when
written, will spawn this binary with `--exe-id ... --bridge-url ...
--token-env ...`. No type/symbol from this plan is exported beyond the
binary boundary, so the runtime plan only needs to know the CLI shape
(documented in Task 1).

---

## File Structure

| File | Responsibility |
|---|---|
| `cmd/codex-app-gateway/main.go` | Process entrypoint: dispatch on first arg between `serve` (placeholder) and `env-mcp`; future `serve` will land in the runtime plan |
| `Dockerfile.codex-app-gateway` | Multi-stage build (golang:1.26-trixie → debian:trixie-slim); EXPOSE 8086; the runtime plan will extend this to also include the codex CLI binary |
| `internal/codexappgateway/envmcp/envmcp.go` | `RunArgs`, `Run(ctx, args, stdin, stdout, stderr) error` — wiring + graceful shutdown |
| `internal/codexappgateway/envmcp/types.go` | Wire types: MCP messages (Request/Response/Notification/Error), MCP `Tool`, MCP `tools/call` content; exec-server method-name constants + `ProcessStartParams` / `ProcessReadParams` / `ProcessReadResponse` / `ProcessOutputChunk` |
| `internal/codexappgateway/envmcp/bridge.go` | `BridgeClient`: dial `ws://.../bridge/{exe_id}` with `Authorization: Bearer <cap-token>`, run a goroutine reading frames, expose `Call(ctx, method, params) (json.RawMessage, error)` and `Notify(ctx, method, params) error` |
| `internal/codexappgateway/envmcp/translator.go` | `Translator`: `RunShell(ctx, argv, cwd) (text string, exitCode *int, err error)` — issues `process/start` then polls `process/read` until exited/closed |
| `internal/codexappgateway/envmcp/mcp_server.go` | `MCPServer`: read newline-delimited JSON requests from `stdin`, dispatch to handlers (initialize / notifications/initialized / tools/list / tools/call / prompts/list / resources/list / resources/templates/list), write responses to `stdout` |
| `internal/codexappgateway/envmcp/envmcp_test.go` | Top-level wiring test (Run with fake stdin/stdout + fake bridge) |
| `internal/codexappgateway/envmcp/bridge_test.go` | `BridgeClient` against a `httptest`-hosted fake exec-server |
| `internal/codexappgateway/envmcp/translator_test.go` | `Translator` with a fake bridge that scripts process/start + read responses |
| `internal/codexappgateway/envmcp/mcp_server_test.go` | `MCPServer` driven via `bytes.Buffer` stdin/stdout |
| `internal/codexappgateway/envmcp/integration_test.go` | Full Run() round-trip: fake exec-server (real ws) + driven via stdin/stdout |
| `cmd/codex-app-gateway/main_test.go` | Subcommand dispatch test (env-mcp invocation + arg validation) |

Total new files: 12. Estimated LOC budget including tests: ~1100 lines
(translator tests are heaviest because they script multi-frame
exec-server scenarios).

---

## Task 1: Repo bootstrap (cmd entry, env-mcp subcommand routing, Dockerfile)

**Files:**
- Create: `cmd/codex-app-gateway/main.go`
- Create: `cmd/codex-app-gateway/main_test.go`
- Create: `Dockerfile.codex-app-gateway`

**CLI contract** (final, referenced by the runtime plan):

```
codex-app-gateway env-mcp \
    --exe-id     <id> \
    --bridge-url <ws-url> \
    --token-env  <env-var-name> \
    [--exe-desc  <text>] \
    [--turn-id   <id>]

codex-app-gateway serve              # placeholder; runtime plan wires it
```

`--token-env` names an env var holding the cap token; the binary
reads it once at startup so the token never appears in `/proc/<pid>/cmdline`.
`--exe-desc` is shown to the LLM in the MCP tool description; defaults
to `--exe-id` if omitted. `--turn-id` is logged to stderr only.

- [ ] **Step 1: Verify dependency baseline**

```bash
cd /root/agentserver && head -3 go.mod && grep -E "(chi/v5|nhooyr.io/websocket|lib/pq)" go.mod
```
Expected: module declares `github.com/agentserver/agentserver`, all
three deps present. (No JWT lib needed for this plan — env-mcp does not
verify or mint tokens, only forwards them in an Authorization header.)

- [ ] **Step 2: Write failing main_test.go**

`cmd/codex-app-gateway/main_test.go`:
```go
package main

import (
	"strings"
	"testing"
)

func TestParseEnvMcpArgs_HappyPath(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--exe-id", "exe_alpha",
		"--bridge-url", "ws://exec-gateway:6060/bridge/exe_alpha",
		"--token-env", "CXG_BRIDGE_TOKEN_EXE_ALPHA",
		"--exe-desc", "Daisy's MacBook",
		"--turn-id", "trn_xxx",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.ExeID != "exe_alpha" {
		t.Errorf("ExeID = %q", args.ExeID)
	}
	if args.BridgeURL != "ws://exec-gateway:6060/bridge/exe_alpha" {
		t.Errorf("BridgeURL = %q", args.BridgeURL)
	}
	if args.TokenEnv != "CXG_BRIDGE_TOKEN_EXE_ALPHA" {
		t.Errorf("TokenEnv = %q", args.TokenEnv)
	}
	if args.ExeDesc != "Daisy's MacBook" {
		t.Errorf("ExeDesc = %q", args.ExeDesc)
	}
}

func TestParseEnvMcpArgs_RequiresExeID(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--bridge-url", "ws://x/bridge/y",
		"--token-env", "T",
	})
	if err == nil || !strings.Contains(err.Error(), "--exe-id") {
		t.Fatalf("want --exe-id required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_RequiresBridgeURL(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--exe-id", "x", "--token-env", "T",
	})
	if err == nil || !strings.Contains(err.Error(), "--bridge-url") {
		t.Fatalf("want --bridge-url required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_RequiresTokenEnv(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--exe-id", "x", "--bridge-url", "ws://x/bridge/y",
	})
	if err == nil || !strings.Contains(err.Error(), "--token-env") {
		t.Fatalf("want --token-env required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_DescDefaultsToExeID(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--exe-id", "exe_x",
		"--bridge-url", "ws://x/bridge/y",
		"--token-env", "T",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.ExeDesc != "exe_x" {
		t.Errorf("ExeDesc default = %q, want exe_x", args.ExeDesc)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd /root/agentserver && go test ./cmd/codex-app-gateway/ -run TestParseEnvMcpArgs -v
```
Expected: build error (`undefined: parseEnvMcpArgs`).

- [ ] **Step 4: Implement main.go**

`cmd/codex-app-gateway/main.go`:
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentserver/agentserver/internal/codexappgateway/envmcp"
)

const usage = `codex-app-gateway — codex gateway binary

Subcommands:
  env-mcp     Run as a stdio MCP child for one executor (per spawned codex turn)
  serve       Run the gateway HTTP/WS server (not implemented in this plan)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "env-mcp":
		runEnvMcp(os.Args[2:])
	case "serve":
		fmt.Fprintln(os.Stderr, "codex-app-gateway: serve subcommand not implemented in this plan")
		os.Exit(2)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runEnvMcp(rawArgs []string) {
	args, err := parseEnvMcpArgs(rawArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-app-gateway env-mcp:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := envmcp.Run(ctx, args, os.Stdin, os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("env-mcp exited with error", "err", err)
		os.Exit(1)
	}
}

func parseEnvMcpArgs(rawArgs []string) (envmcp.RunArgs, error) {
	fs := flag.NewFlagSet("env-mcp", flag.ContinueOnError)
	exeID := fs.String("exe-id", "", "executor id (required)")
	bridgeURL := fs.String("bridge-url", "", "ws URL for /bridge/{exe_id} (required)")
	tokenEnv := fs.String("token-env", "", "env var name holding the cap token (required)")
	exeDesc := fs.String("exe-desc", "", "executor description shown to the LLM (defaults to --exe-id)")
	turnID := fs.String("turn-id", "", "turn id (logged to stderr only)")
	if err := fs.Parse(rawArgs); err != nil {
		return envmcp.RunArgs{}, err
	}
	if *exeID == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--exe-id is required")
	}
	if *bridgeURL == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--bridge-url is required")
	}
	if *tokenEnv == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--token-env is required")
	}
	desc := *exeDesc
	if desc == "" {
		desc = *exeID
	}
	return envmcp.RunArgs{
		ExeID:     *exeID,
		BridgeURL: *bridgeURL,
		TokenEnv:  *tokenEnv,
		ExeDesc:   desc,
		TurnID:    *turnID,
	}, nil
}
```

- [ ] **Step 5: Stub the envmcp package so the import compiles**

`internal/codexappgateway/envmcp/envmcp.go` (minimal):
```go
package envmcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

// RunArgs is the parsed CLI input for `codex-app-gateway env-mcp`.
type RunArgs struct {
	ExeID     string
	BridgeURL string
	TokenEnv  string
	ExeDesc   string
	TurnID    string
}

// Run is the env-mcp entry point. Filled in by Task 6.
func Run(_ context.Context, _ RunArgs, _ io.Reader, _ io.Writer, _ io.Writer, _ *slog.Logger) error {
	return errors.New("envmcp.Run: not yet implemented")
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
cd /root/agentserver && go test ./cmd/codex-app-gateway/ -run TestParseEnvMcpArgs -v
```
Expected: PASS (5 tests).

- [ ] **Step 7: Write Dockerfile.codex-app-gateway**

`Dockerfile.codex-app-gateway`:
```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o /out/codex-app-gateway ./cmd/codex-app-gateway

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/codex-app-gateway /usr/local/bin/codex-app-gateway
ENTRYPOINT ["/usr/local/bin/codex-app-gateway"]
CMD ["serve"]
EXPOSE 8086
```

- [ ] **Step 8: Smoke-build the binary**

```bash
cd /root/agentserver && go build ./cmd/codex-app-gateway/
```
Expected: builds with no errors. Then:
```bash
./codex-app-gateway env-mcp 2>&1 | head -3 ; rm codex-app-gateway
```
Expected: prints `codex-app-gateway env-mcp: --exe-id is required` to
stderr and exits non-zero.

- [ ] **Step 9: Commit**

```bash
git add cmd/codex-app-gateway/ internal/codexappgateway/envmcp/envmcp.go Dockerfile.codex-app-gateway
git commit -m "feat(codex-app-gateway): bootstrap binary + env-mcp subcommand routing"
```

---

## Task 2: MCP + exec-server wire types

**Files:**
- Create: `internal/codexappgateway/envmcp/types.go`

This file defines all wire structs used by both directions plus the
exec-server method-name constants. The MCP types intentionally cover
only the methods env-mcp implements; richer MCP servers belong elsewhere.

- [ ] **Step 1: Write failing types_test.go**

`internal/codexappgateway/envmcp/types_test.go`:
```go
package envmcp

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestProcessOutputChunk_DecodesBase64Stream(t *testing.T) {
	raw := []byte(`{"seq":7,"stream":"stdout","chunk":"aGVsbG8="}`)
	var c ProcessOutputChunk
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Seq != 7 || c.Stream != "stdout" {
		t.Errorf("seq=%d stream=%q", c.Seq, c.Stream)
	}
	got, err := base64.StdEncoding.DecodeString(c.Chunk)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("decoded = %q", got)
	}
}

func TestExecServerMethods_Constants(t *testing.T) {
	if ExecMethodInitialize != "initialize" ||
		ExecMethodProcessStart != "process/start" ||
		ExecMethodProcessRead != "process/read" {
		t.Fatalf("method constants drifted: %s/%s/%s",
			ExecMethodInitialize, ExecMethodProcessStart, ExecMethodProcessRead)
	}
}

func TestMCPCallToolResultMarshal(t *testing.T) {
	r := MCPCallToolResult{
		Content: []MCPToolContent{{Type: "text", Text: "ok"}},
		IsError: true,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"content":[{"type":"text","text":"ok"}],"isError":true}` {
		t.Fatalf("unexpected JSON: %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestProcessOutputChunk|TestExecServerMethods|TestMCPCallToolResultMarshal' -v
```
Expected: build error — types not defined.

- [ ] **Step 3: Implement types.go**

`internal/codexappgateway/envmcp/types.go`:
```go
package envmcp

import "encoding/json"

// --- MCP wire types (subset implemented) ---

// JSONRPCRequest / Response / Notification / Error are the JSON-RPC 2.0
// envelopes shared by both MCP (over stdio) and exec-server (over ws).
// Kept identical for both directions; the only sender-specific concern
// is whether `id` is present, which the encoder handles by zero-value
// omission below.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// MCPInitializeResult is the response to `initialize`.
type MCPInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      MCPServerInfo  `json:"serverInfo"`
}

type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPListToolsResult is the response to `tools/list`.
type MCPListToolsResult struct {
	Tools []MCPTool `json:"tools"`
}

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPCallToolParams is the request body of `tools/call`.
type MCPCallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// MCPCallToolResult is the response body of `tools/call`.
type MCPCallToolResult struct {
	Content []MCPToolContent `json:"content"`
	IsError bool             `json:"isError"`
}

type MCPToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// --- exec-server wire types (subset env-mcp uses) ---

// Method names — must match codex-rs/exec-server/src/protocol.rs.
const (
	ExecMethodInitialize    = "initialize"
	ExecMethodInitialized   = "initialized" // notification
	ExecMethodProcessStart  = "process/start"
	ExecMethodProcessRead   = "process/read"
	ExecMethodProcessExited = "process/exited" // notification (informational; we poll instead)
	ExecMethodProcessClosed = "process/closed" // notification (informational)
)

// ExecInitializeParams matches codex-rs's InitializeParams (camelCase).
type ExecInitializeParams struct {
	ClientName      string  `json:"clientName"`
	ResumeSessionID *string `json:"resumeSessionId,omitempty"`
}

type ExecInitializeResult struct {
	SessionID string `json:"sessionId"`
}

type ProcessStartParams struct {
	ProcessID string            `json:"processId"`
	Argv      []string          `json:"argv"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	TTY       bool              `json:"tty"`
	PipeStdin bool              `json:"pipeStdin"`
	Arg0      *string           `json:"arg0"`
}

type ProcessStartResult struct {
	ProcessID string `json:"processId"`
}

type ProcessReadParams struct {
	ProcessID string `json:"processId"`
	AfterSeq  uint64 `json:"afterSeq"`
	MaxBytes  int    `json:"maxBytes"`
	WaitMs    int    `json:"waitMs"`
}

type ProcessReadResult struct {
	Chunks   []ProcessOutputChunk `json:"chunks"`
	NextSeq  uint64               `json:"nextSeq"`
	Exited   bool                 `json:"exited"`
	ExitCode *int                 `json:"exitCode"`
	Closed   bool                 `json:"closed"`
	Failure  *string              `json:"failure"`
}

// ProcessOutputChunk: chunk is base64-encoded raw bytes (per codex's
// ByteChunk wrapper that uses serde_with for base64 encoding).
type ProcessOutputChunk struct {
	Seq    uint64 `json:"seq"`
	Stream string `json:"stream"` // "stdout" | "stderr"
	Chunk  string `json:"chunk"`
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -v
```
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/envmcp/types.go internal/codexappgateway/envmcp/types_test.go
git commit -m "feat(envmcp): MCP + exec-server wire types"
```

---

## Task 3: BridgeClient (ws dial + JSON-RPC client)

**Files:**
- Create: `internal/codexappgateway/envmcp/bridge.go`
- Create: `internal/codexappgateway/envmcp/bridge_test.go`

The BridgeClient holds one WebSocket to `/bridge/{exe_id}`, runs a
goroutine reading frames, and exposes `Call`/`Notify`/`Close`. ID
allocation is monotonic int64 starting at 1 (id=0 is reserved as
"never sent" so `*int64` zero in `JSONRPCMessage.ID` means "this is a
notification"). Pending requests are tracked in a `map[int64]chan
*JSONRPCMessage` under one mutex.

- [ ] **Step 1: Write failing bridge_test.go**

`internal/codexappgateway/envmcp/bridge_test.go`:
```go
package envmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeExecServer accepts one ws connection, echoes each JSON-RPC
// request as a result whose body is the request's params, and exposes
// the last Authorization header it saw.
type fakeExecServer struct {
	srv          *httptest.Server
	gotAuth      string
	connectErr   error
}

func newFakeExecServer(t *testing.T) *fakeExecServer {
	t.Helper()
	f := &fakeExecServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			f.connectErr = err
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg JSONRPCMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.ID == nil {
				continue // notification, no reply
			}
			resp := JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: msg.Params}
			out, _ := json.Marshal(&resp)
			_ = c.Write(ctx, websocket.MessageText, out)
		}
	}))
	return f
}

func (f *fakeExecServer) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http")
}

func (f *fakeExecServer) Close() { f.srv.Close() }

func TestBridgeClient_DialAndCall(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bc, err := DialBridge(ctx, f.wsURL(), "tok-123")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer bc.Close()

	if f.gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want %q", f.gotAuth, "Bearer tok-123")
	}

	res, err := bc.Call(ctx, "ping", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(res) != `{"x":1}` {
		t.Errorf("result = %s", res)
	}
}

func TestBridgeClient_Notify_NoReply(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bc, err := DialBridge(ctx, f.wsURL(), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer bc.Close()
	if err := bc.Notify(ctx, "initialized", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

func TestBridgeClient_Call_AfterClose_Errors(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bc, err := DialBridge(ctx, f.wsURL(), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	bc.Close()
	if _, err := bc.Call(ctx, "ping", nil); err == nil {
		t.Fatal("expected error after Close")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestBridgeClient' -v
```
Expected: build error (`undefined: DialBridge`).

- [ ] **Step 3: Implement bridge.go**

`internal/codexappgateway/envmcp/bridge.go`:
```go
package envmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"nhooyr.io/websocket"
)

// BridgeClient wraps one WebSocket connection to /bridge/{exe_id} on
// codex-exec-gateway and exposes a JSON-RPC client interface that env-mcp
// uses to talk codex's exec-server protocol.
//
// Concurrency model: a single background goroutine reads frames and
// dispatches them to a per-id reply channel; Call() blocks on its
// channel until the goroutine delivers, the context is cancelled, or
// the connection closes.
type BridgeClient struct {
	ws       *websocket.Conn
	nextID   atomic.Int64
	mu       sync.Mutex
	pending  map[int64]chan *JSONRPCMessage
	closed   chan struct{}
	closeErr error
	cancel   context.CancelFunc
}

// DialBridge dials wsURL and, when authToken is non-empty, sets
// `Authorization: Bearer <authToken>` on the upgrade request. Returns
// once the WebSocket handshake completes; subsequent reads are pumped
// by a background goroutine.
//
// nhooyr.io/websocket does NOT request `permessage-deflate` by default —
// we rely on that, because codex's exec-server closes connections that
// do (see spec § PoC #2 gotchas).
func DialBridge(ctx context.Context, wsURL, authToken string) (*BridgeClient, error) {
	opts := &websocket.DialOptions{}
	if authToken != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + authToken}}
	}
	ws, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		return nil, fmt.Errorf("ws dial %s: %w", wsURL, err)
	}
	ws.SetReadLimit(-1) // exec-server can stream large process/read responses

	loopCtx, cancel := context.WithCancel(context.Background())
	bc := &BridgeClient{
		ws:      ws,
		pending: make(map[int64]chan *JSONRPCMessage),
		closed:  make(chan struct{}),
		cancel:  cancel,
	}
	go bc.readLoop(loopCtx)
	return bc, nil
}

// Call sends a JSON-RPC request and blocks until the response arrives,
// the context is cancelled, or the connection closes.
func (bc *BridgeClient) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := bc.nextID.Add(1)
	ch := make(chan *JSONRPCMessage, 1)

	bc.mu.Lock()
	if bc.isClosedLocked() {
		bc.mu.Unlock()
		return nil, errors.New("bridge: connection closed")
	}
	bc.pending[id] = ch
	bc.mu.Unlock()
	defer func() {
		bc.mu.Lock()
		delete(bc.pending, id)
		bc.mu.Unlock()
	}()

	msg := JSONRPCMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	out, err := json.Marshal(&msg)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if err := bc.ws.Write(ctx, websocket.MessageText, out); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-bc.closed:
		if bc.closeErr != nil {
			return nil, bc.closeErr
		}
		return nil, errors.New("bridge: connection closed")
	case reply := <-ch:
		if reply.Error != nil {
			return nil, fmt.Errorf("%s: %s (code=%d)", method, reply.Error.Message, reply.Error.Code)
		}
		return reply.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no id, no reply expected).
func (bc *BridgeClient) Notify(ctx context.Context, method string, params json.RawMessage) error {
	bc.mu.Lock()
	closed := bc.isClosedLocked()
	bc.mu.Unlock()
	if closed {
		return errors.New("bridge: connection closed")
	}
	msg := JSONRPCMessage{JSONRPC: "2.0", Method: method, Params: params}
	out, err := json.Marshal(&msg)
	if err != nil {
		return fmt.Errorf("marshal notify: %w", err)
	}
	return bc.ws.Write(ctx, websocket.MessageText, out)
}

// Close shuts the connection. Safe to call repeatedly; first call wins.
func (bc *BridgeClient) Close() {
	bc.mu.Lock()
	if bc.isClosedLocked() {
		bc.mu.Unlock()
		return
	}
	close(bc.closed)
	bc.mu.Unlock()
	bc.cancel()
	_ = bc.ws.Close(websocket.StatusNormalClosure, "client closing")
}

func (bc *BridgeClient) isClosedLocked() bool {
	select {
	case <-bc.closed:
		return true
	default:
		return false
	}
}

func (bc *BridgeClient) readLoop(ctx context.Context) {
	defer func() {
		bc.mu.Lock()
		if !bc.isClosedLocked() {
			close(bc.closed)
		}
		bc.mu.Unlock()
	}()
	for {
		_, data, err := bc.ws.Read(ctx)
		if err != nil {
			bc.mu.Lock()
			bc.closeErr = err
			bc.mu.Unlock()
			return
		}
		var msg JSONRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID == nil {
			// Server-pushed notification (process/exited, process/output, ...).
			// env-mcp polls process/read instead, so we drop these for now.
			// Future runtime/cancel work may want to consume them.
			continue
		}
		bc.mu.Lock()
		ch, ok := bc.pending[*msg.ID]
		bc.mu.Unlock()
		if !ok {
			continue
		}
		select {
		case ch <- &msg:
		default:
		}
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestBridgeClient' -v
```
Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/envmcp/bridge.go internal/codexappgateway/envmcp/bridge_test.go
git commit -m "feat(envmcp): bridge ws client with JSON-RPC call/notify"
```

---

## Task 4: Translator (MCP shell → exec-server process/start + read loop)

**Files:**
- Create: `internal/codexappgateway/envmcp/translator.go`
- Create: `internal/codexappgateway/envmcp/translator_test.go`

The translator owns the protocol translation. It is fed a `BridgeCaller`
interface (same shape as BridgeClient: `Call` + `Notify`) so tests can
script multi-frame sequences without standing up a real ws server.

The poll loop:
1. `process/start` with a fresh `processId` (UUID-ish; just monotonic +
   pid is fine, no need for crypto entropy).
2. Loop: `process/read` with `waitMs=250`, accumulate chunks until
   `exited || closed`.
3. Aggregate stdout + stderr into a single text block; set `isError`
   from `exitCode != 0` (and `true` on transport failure).
4. Bound the loop with a hard cap (e.g. 240 iterations × 250ms = 60s of
   silent stalled output) and return a `[exec timeout]` text on cap.

- [ ] **Step 1: Write failing translator_test.go**

`internal/codexappgateway/envmcp/translator_test.go`:
```go
package envmcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedBridge is a BridgeCaller whose responses are pre-scripted.
// It records every method call for assertion.
type scriptedBridge struct {
	startResult   ProcessStartResult
	reads         []ProcessReadResult
	readIdx       atomic.Int32
	calls         []scriptedCall
}

type scriptedCall struct {
	method string
	params json.RawMessage
}

func (s *scriptedBridge) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	s.calls = append(s.calls, scriptedCall{method: method, params: params})
	switch method {
	case ExecMethodProcessStart:
		out, _ := json.Marshal(s.startResult)
		return out, nil
	case ExecMethodProcessRead:
		i := int(s.readIdx.Add(1)) - 1
		if i >= len(s.reads) {
			return nil, errors.New("scriptedBridge: out of read responses")
		}
		out, _ := json.Marshal(s.reads[i])
		return out, nil
	default:
		return nil, errors.New("scriptedBridge: unknown method " + method)
	}
}

func (s *scriptedBridge) Notify(_ context.Context, _ string, _ json.RawMessage) error { return nil }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestTranslator_RunShell_HappyPath(t *testing.T) {
	exit := 0
	b := &scriptedBridge{
		startResult: ProcessStartResult{ProcessID: "pid-1"},
		reads: []ProcessReadResult{
			{
				Chunks: []ProcessOutputChunk{
					{Seq: 1, Stream: "stdout", Chunk: b64("hello\n")},
				},
				NextSeq:  2,
				Exited:   true,
				ExitCode: &exit,
				Closed:   true,
			},
		},
	}
	tr := NewTranslator(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := tr.RunShell(ctx, []string{"echo", "hello"}, "/tmp")
	if err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Errorf("Text = %q", res.Text)
	}
	if !strings.Contains(res.Text, "[exit_code=0]") {
		t.Errorf("Text missing exit code: %q", res.Text)
	}
	if res.IsError {
		t.Errorf("IsError = true on exit 0")
	}
	if len(b.calls) != 2 {
		t.Errorf("call count = %d, want 2", len(b.calls))
	}
	if b.calls[0].method != ExecMethodProcessStart {
		t.Errorf("first call method = %q", b.calls[0].method)
	}
}

func TestTranslator_RunShell_NonZeroExit_IsError(t *testing.T) {
	exit := 1
	b := &scriptedBridge{
		startResult: ProcessStartResult{ProcessID: "pid-1"},
		reads: []ProcessReadResult{{NextSeq: 0, Exited: true, ExitCode: &exit, Closed: true}},
	}
	tr := NewTranslator(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := tr.RunShell(ctx, []string{"false"}, "/tmp")
	if err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false on exit 1")
	}
	if !strings.Contains(res.Text, "[exit_code=1]") {
		t.Errorf("Text missing exit code: %q", res.Text)
	}
}

func TestTranslator_RunShell_StderrIncluded(t *testing.T) {
	exit := 0
	b := &scriptedBridge{
		startResult: ProcessStartResult{ProcessID: "pid-1"},
		reads: []ProcessReadResult{
			{
				Chunks: []ProcessOutputChunk{
					{Seq: 1, Stream: "stdout", Chunk: b64("ok\n")},
					{Seq: 2, Stream: "stderr", Chunk: b64("warn\n")},
				},
				NextSeq:  3,
				Exited:   true,
				ExitCode: &exit,
				Closed:   true,
			},
		},
	}
	tr := NewTranslator(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := tr.RunShell(ctx, []string{"sh", "-c", "echo ok; echo warn 1>&2"}, "/tmp")
	if err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !strings.Contains(res.Text, "ok") || !strings.Contains(res.Text, "warn") {
		t.Errorf("Text missing stdout or stderr: %q", res.Text)
	}
	if !strings.Contains(res.Text, "--- stderr ---") {
		t.Errorf("Text missing stderr divider: %q", res.Text)
	}
}

func TestTranslator_RunShell_MultipleReadCycles(t *testing.T) {
	exit := 0
	b := &scriptedBridge{
		startResult: ProcessStartResult{ProcessID: "pid-1"},
		reads: []ProcessReadResult{
			{Chunks: []ProcessOutputChunk{{Seq: 1, Stream: "stdout", Chunk: b64("part1 ")}}, NextSeq: 2},
			{Chunks: []ProcessOutputChunk{{Seq: 2, Stream: "stdout", Chunk: b64("part2")}}, NextSeq: 3},
			{NextSeq: 3, Exited: true, ExitCode: &exit, Closed: true},
		},
	}
	tr := NewTranslator(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := tr.RunShell(ctx, []string{"echo", "x"}, "/tmp")
	if err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !strings.Contains(res.Text, "part1 part2") {
		t.Errorf("Text = %q", res.Text)
	}
	if len(b.calls) != 4 {
		t.Errorf("expected 1 start + 3 reads = 4 calls, got %d", len(b.calls))
	}
	// Verify afterSeq advanced correctly between reads.
	var p1, p2 ProcessReadParams
	_ = json.Unmarshal(b.calls[1].params, &p1)
	_ = json.Unmarshal(b.calls[2].params, &p2)
	if p1.AfterSeq != 0 || p2.AfterSeq != 2 {
		t.Errorf("afterSeq drift: %d → %d", p1.AfterSeq, p2.AfterSeq)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestTranslator' -v
```
Expected: build error (`undefined: NewTranslator`).

- [ ] **Step 3: Implement translator.go**

`internal/codexappgateway/envmcp/translator.go`:
```go
package envmcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// BridgeCaller is the slice of BridgeClient that Translator needs.
// Defined as an interface so tests can script call sequences.
type BridgeCaller interface {
	Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params json.RawMessage) error
}

// Translator turns MCP shell tool calls into exec-server JSON-RPC
// sequences (process/start, then process/read until exited or closed).
type Translator struct {
	bridge BridgeCaller
	pidSeq atomic.Uint64
}

// ShellResult is what RunShell returns; mapped into MCP CallToolResult
// by the caller (mcp_server.go).
type ShellResult struct {
	Text    string
	IsError bool
}

const (
	defaultMaxReadCycles = 240 // ~60s @ 250ms wait
	defaultReadWaitMs    = 250
	defaultMaxBytes      = 65536
)

func NewTranslator(b BridgeCaller) *Translator { return &Translator{bridge: b} }

// RunShell runs argv on the bound executor in cwd and returns the
// aggregated output. Never returns ctx-independent errors — transport
// failures surface as `IsError=true` with the failure in Text.
func (t *Translator) RunShell(ctx context.Context, argv []string, cwd string) (ShellResult, error) {
	if len(argv) == 0 {
		return ShellResult{}, errors.New("RunShell: empty argv")
	}
	pid := fmt.Sprintf("envmcp-%d", t.pidSeq.Add(1))

	startParams, err := json.Marshal(ProcessStartParams{
		ProcessID: pid,
		Argv:      argv,
		Cwd:       cwd,
		Env:       map[string]string{"PATH": "/usr/bin:/bin:/usr/local/bin"},
		TTY:       false,
		PipeStdin: false,
		Arg0:      nil,
	})
	if err != nil {
		return ShellResult{}, fmt.Errorf("marshal process/start: %w", err)
	}
	if _, err := t.bridge.Call(ctx, ExecMethodProcessStart, startParams); err != nil {
		return ShellResult{
			Text:    fmt.Sprintf("[exec failed to start: %v]", err),
			IsError: true,
		}, nil
	}

	var stdout, stderr strings.Builder
	var afterSeq uint64
	var exitCode *int
	var failure *string

	for cycle := 0; cycle < defaultMaxReadCycles; cycle++ {
		readParams, _ := json.Marshal(ProcessReadParams{
			ProcessID: pid,
			AfterSeq:  afterSeq,
			MaxBytes:  defaultMaxBytes,
			WaitMs:    defaultReadWaitMs,
		})
		raw, err := t.bridge.Call(ctx, ExecMethodProcessRead, readParams)
		if err != nil {
			return ShellResult{
				Text:    fmt.Sprintf("%s%s\n[exec read failed: %v]", stdout.String(), stderr.String(), err),
				IsError: true,
			}, nil
		}
		var r ProcessReadResult
		if err := json.Unmarshal(raw, &r); err != nil {
			return ShellResult{
				Text:    fmt.Sprintf("[exec read decode failed: %v]", err),
				IsError: true,
			}, nil
		}
		for _, ch := range r.Chunks {
			data, err := base64.StdEncoding.DecodeString(ch.Chunk)
			if err != nil {
				continue
			}
			if ch.Stream == "stderr" {
				stderr.Write(data)
			} else {
				stdout.Write(data)
			}
		}
		afterSeq = r.NextSeq
		if r.Exited || r.Closed {
			exitCode = r.ExitCode
			failure = r.Failure
			break
		}
	}

	var text strings.Builder
	if stdout.Len() > 0 {
		text.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if text.Len() > 0 {
			text.WriteString("\n--- stderr ---\n")
		}
		text.WriteString(stderr.String())
	}
	if failure != nil {
		text.WriteString(fmt.Sprintf("\n[exec failure: %s]", *failure))
	}
	if exitCode != nil {
		text.WriteString(fmt.Sprintf("\n[exit_code=%d]", *exitCode))
	} else {
		text.WriteString("\n[exec timed out without exit signal]")
	}

	isErr := failure != nil || (exitCode != nil && *exitCode != 0) || exitCode == nil
	return ShellResult{Text: text.String(), IsError: isErr}, nil
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestTranslator' -v
```
Expected: 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/envmcp/translator.go internal/codexappgateway/envmcp/translator_test.go
git commit -m "feat(envmcp): translator turns MCP shell calls into exec-server frames"
```

---

## Task 5: MCPServer (stdio JSON-RPC server)

**Files:**
- Create: `internal/codexappgateway/envmcp/mcp_server.go`
- Create: `internal/codexappgateway/envmcp/mcp_server_test.go`

`MCPServer` reads newline-delimited JSON requests from `stdin`, hands
each off to a method handler synchronously (MCP stdio is request-reply
order-preserving), writes responses to `stdout`. Notifications get no
reply.

The shell tool's description carries the executor description so the
LLM (via codex's MCP namespacing → `mcp__exe_alpha__shell`) sees the
right context.

- [ ] **Step 1: Write failing mcp_server_test.go**

`internal/codexappgateway/envmcp/mcp_server_test.go`:
```go
package envmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubTranslator struct {
	gotArgv []string
	gotCwd  string
	out     ShellResult
	err     error
}

func (s *stubTranslator) RunShell(_ context.Context, argv []string, cwd string) (ShellResult, error) {
	s.gotArgv = argv
	s.gotCwd = cwd
	return s.out, s.err
}

func driveServer(t *testing.T, srv *MCPServer, lines ...string) []map[string]any {
	t.Helper()
	in := bytes.NewBufferString(strings.Join(lines, "\n") + "\n")
	out := &bytes.Buffer{}
	if err := srv.Serve(context.Background(), in, out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var got []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("bad line %q: %v", ln, err)
		}
		got = append(got, m)
	}
	return got
}

func TestMCPServer_InitializeAndToolsList(t *testing.T) {
	srv := NewMCPServer("Daisy's MacBook", &stubTranslator{})
	got := driveServer(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(got) != 2 {
		t.Fatalf("got %d responses: %v", len(got), got)
	}
	res0 := got[0]["result"].(map[string]any)
	if res0["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", res0["protocolVersion"])
	}
	res1 := got[1]["result"].(map[string]any)
	tools := res1["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "shell" {
		t.Errorf("tool name = %v", tool["name"])
	}
	if !strings.Contains(tool["description"].(string), "Daisy's MacBook") {
		t.Errorf("tool description missing executor: %v", tool["description"])
	}
}

func TestMCPServer_ToolsCallShell_DispatchesToTranslator(t *testing.T) {
	tr := &stubTranslator{out: ShellResult{Text: "ok\n[exit_code=0]", IsError: false}}
	srv := NewMCPServer("desc", tr)
	got := driveServer(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shell","arguments":{"command":["ls","-la"],"cwd":"/srv"}}}`,
	)
	if len(got) != 2 {
		t.Fatalf("got %d responses", len(got))
	}
	if got := tr.gotArgv; len(got) != 2 || got[0] != "ls" || got[1] != "-la" {
		t.Errorf("argv = %v", tr.gotArgv)
	}
	if tr.gotCwd != "/srv" {
		t.Errorf("cwd = %q", tr.gotCwd)
	}
	res := got[1]["result"].(map[string]any)
	if res["isError"] != false {
		t.Errorf("isError = %v", res["isError"])
	}
	content := res["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || !strings.Contains(content["text"].(string), "ok") {
		t.Errorf("content = %v", content)
	}
}

func TestMCPServer_ToolsCall_UnknownTool_Error(t *testing.T) {
	srv := NewMCPServer("desc", &stubTranslator{})
	got := driveServer(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bogus","arguments":{}}}`,
	)
	if got[0]["error"] == nil {
		t.Fatalf("expected error response: %v", got[0])
	}
}

func TestMCPServer_PromptsAndResources_EmptyLists(t *testing.T) {
	srv := NewMCPServer("desc", &stubTranslator{})
	got := driveServer(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/templates/list"}`,
	)
	if len(got) != 3 {
		t.Fatalf("got %d responses", len(got))
	}
	for i, key := range []string{"prompts", "resources", "resourceTemplates"} {
		res := got[i]["result"].(map[string]any)
		arr, ok := res[key].([]any)
		if !ok || len(arr) != 0 {
			t.Errorf("response %d missing empty %s: %v", i, key, res)
		}
	}
}

func TestMCPServer_NotificationProducesNoReply(t *testing.T) {
	srv := NewMCPServer("desc", &stubTranslator{})
	got := driveServer(t, srv,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestMCPServer' -v
```
Expected: build error (`undefined: NewMCPServer`).

- [ ] **Step 3: Implement mcp_server.go**

`internal/codexappgateway/envmcp/mcp_server.go`:
```go
package envmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ShellRunner is the slice of Translator that MCPServer uses.
// Defined as an interface so mcp_server tests don't need a real bridge.
type ShellRunner interface {
	RunShell(ctx context.Context, argv []string, cwd string) (ShellResult, error)
}

// MCPServer is a minimal newline-delimited JSON-RPC stdio MCP server
// that exposes a single `shell` tool. Concurrency: requests are handled
// sequentially in the order they arrive; this matches the MCP stdio
// model and keeps the server free of intra-process synchronization
// other than the write-mutex.
type MCPServer struct {
	exeDesc string
	tr      ShellRunner
	writeMu sync.Mutex
}

func NewMCPServer(exeDesc string, tr ShellRunner) *MCPServer {
	return &MCPServer{exeDesc: exeDesc, tr: tr}
}

// Serve reads requests from in until EOF and writes responses to out.
// Returns nil on clean EOF, error on unrecoverable read/write failure.
func (s *MCPServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req JSONRPCMessage
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed input; per JSON-RPC 2.0, parse errors on a
			// notification have no reply target so we log+drop.
			continue
		}
		if err := s.dispatch(ctx, &req, out); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *MCPServer) dispatch(ctx context.Context, req *JSONRPCMessage, out io.Writer) error {
	switch req.Method {
	case "initialize":
		return s.respond(out, req.ID, MCPInitializeResult{
			ProtocolVersion: "2025-06-18",
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      MCPServerInfo{Name: "codex-env-mcp", Version: "0.1"},
		}, nil)

	case "notifications/initialized":
		return nil // notification

	case "tools/list":
		schema := json.RawMessage(`{"type":"object","properties":{` +
			`"command":{"type":"array","items":{"type":"string"},"description":"argv as a list of strings"},` +
			`"cwd":{"type":"string","description":"Working directory; defaults to /tmp"}` +
			`},"required":["command"]}`)
		desc := fmt.Sprintf(
			"Run a shell command on `%s`. Use this tool for any shell operation in this environment.",
			s.exeDesc,
		)
		return s.respond(out, req.ID, MCPListToolsResult{
			Tools: []MCPTool{{Name: "shell", Description: desc, InputSchema: schema}},
		}, nil)

	case "tools/call":
		var p MCPCallToolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32602, Message: "invalid params: " + err.Error()})
		}
		if p.Name != "shell" {
			return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32601, Message: "unknown tool: " + p.Name})
		}
		var args struct {
			Command []string `json:"command"`
			Cwd     string   `json:"cwd"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32602, Message: "invalid arguments: " + err.Error()})
		}
		if len(args.Command) == 0 {
			return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32602, Message: "command must be a non-empty array"})
		}
		cwd := args.Cwd
		if cwd == "" {
			cwd = "/tmp"
		}
		res, err := s.tr.RunShell(ctx, args.Command, cwd)
		if err != nil {
			return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32000, Message: "shell failed: " + err.Error()})
		}
		return s.respond(out, req.ID, MCPCallToolResult{
			Content: []MCPToolContent{{Type: "text", Text: res.Text}},
			IsError: res.IsError,
		}, nil)

	case "prompts/list":
		return s.respond(out, req.ID, map[string]any{"prompts": []any{}}, nil)
	case "resources/list":
		return s.respond(out, req.ID, map[string]any{"resources": []any{}}, nil)
	case "resources/templates/list":
		return s.respond(out, req.ID, map[string]any{"resourceTemplates": []any{}}, nil)

	default:
		if req.ID == nil {
			return nil // notification of unknown method — drop
		}
		return s.respond(out, req.ID, nil, &JSONRPCError{Code: -32601, Message: "method not found: " + req.Method})
	}
}

func (s *MCPServer) respond(out io.Writer, id *int64, result any, errObj *JSONRPCError) error {
	if id == nil && errObj == nil {
		return nil // nothing to say back
	}
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: id, Error: errObj}
	if errObj == nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		msg.Result = raw
	}
	out2, err := json.Marshal(&msg)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := out.Write(append(out2, '\n')); err != nil {
		return errors.New("mcp write: " + err.Error())
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestMCPServer' -v
```
Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/envmcp/mcp_server.go internal/codexappgateway/envmcp/mcp_server_test.go
git commit -m "feat(envmcp): stdio MCP server exposing one shell tool"
```

---

## Task 6: Wire up envmcp.Run

**Files:**
- Modify: `internal/codexappgateway/envmcp/envmcp.go` (replace stub)
- Create: `internal/codexappgateway/envmcp/envmcp_test.go`

`Run` reads the cap token from `args.TokenEnv`, dials the bridge,
initializes the exec-server session, builds a translator, then runs the
MCP server loop. Returns when stdin closes (codex's MCP host has
disconnected) or when the bridge ws closes.

- [ ] **Step 1: Write failing envmcp_test.go**

`internal/codexappgateway/envmcp/envmcp_test.go`:
```go
package envmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeBridgeServer answers initialize, then a single process/start +
// process/read with canned stdout, then closes.
func fakeBridgeServer(t *testing.T, wantAuth string, sawAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*sawAuth = r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		exit := 0
		read := ProcessReadResult{
			Chunks: []ProcessOutputChunk{
				{Seq: 1, Stream: "stdout", Chunk: "ZmFrZS1vdXQ="}, // "fake-out"
			},
			NextSeq:  2,
			Exited:   true,
			ExitCode: &exit,
			Closed:   true,
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg JSONRPCMessage
			_ = json.Unmarshal(data, &msg)
			if msg.ID == nil {
				continue
			}
			var resp JSONRPCMessage
			resp.JSONRPC = "2.0"
			resp.ID = msg.ID
			switch msg.Method {
			case ExecMethodInitialize:
				out, _ := json.Marshal(ExecInitializeResult{SessionID: "fake-session"})
				resp.Result = out
			case ExecMethodProcessStart:
				out, _ := json.Marshal(ProcessStartResult{ProcessID: "p1"})
				resp.Result = out
			case ExecMethodProcessRead:
				out, _ := json.Marshal(read)
				resp.Result = out
			default:
				resp.Error = &JSONRPCError{Code: -32601, Message: "no"}
			}
			payload, _ := json.Marshal(&resp)
			_ = c.Write(ctx, websocket.MessageText, payload)
		}
	}))
}

func TestRun_EndToEnd(t *testing.T) {
	var sawAuth string
	srv := fakeBridgeServer(t, "Bearer fake-tok", &sawAuth)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	t.Setenv("CXG_TEST_TOKEN", "fake-tok")

	in := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shell","arguments":{"command":["echo","x"]}}}`,
		"",
	}, "\n"))
	out := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Run(ctx, RunArgs{
		ExeID:     "exe_test",
		BridgeURL: wsURL,
		TokenEnv:  "CXG_TEST_TOKEN",
		ExeDesc:   "Test executor",
	}, in, out, stderr, logger)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if sawAuth != "Bearer fake-tok" {
		t.Errorf("Authorization seen by bridge = %q", sawAuth)
	}
	if !strings.Contains(out.String(), "fake-out") {
		t.Errorf("MCP stdout missing translated output: %q", out.String())
	}
}

func TestRun_EmptyToken_Errors(t *testing.T) {
	t.Setenv("CXG_TEST_TOKEN", "")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	err := Run(context.Background(), RunArgs{
		ExeID:     "x",
		BridgeURL: "ws://127.0.0.1:1",
		TokenEnv:  "CXG_TEST_TOKEN",
		ExeDesc:   "x",
	}, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}, logger)
	if err == nil || !strings.Contains(err.Error(), "CXG_TEST_TOKEN") {
		t.Fatalf("want empty-token error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -run 'TestRun_' -v
```
Expected: stub returns "not yet implemented" — both tests fail.

- [ ] **Step 3: Replace stub with real Run**

`internal/codexappgateway/envmcp/envmcp.go`:
```go
package envmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// RunArgs is the parsed CLI input for `codex-app-gateway env-mcp`.
type RunArgs struct {
	ExeID     string
	BridgeURL string
	TokenEnv  string
	ExeDesc   string
	TurnID    string
}

// Run dials the bridge, initializes the exec-server session, then runs
// the stdio MCP server loop until stdin EOF or context cancellation.
//
// stderr is reserved for env-mcp's own diagnostic logging; stdout is
// dedicated to MCP JSON-RPC frames. Anything written to stdout outside
// of MCPServer.Serve corrupts the MCP stream.
func Run(ctx context.Context, args RunArgs, stdin io.Reader, stdout, _ io.Writer, logger *slog.Logger) error {
	token := os.Getenv(args.TokenEnv)
	if token == "" {
		return fmt.Errorf("env var %s is empty; cannot authenticate to bridge", args.TokenEnv)
	}

	logger.Info("env-mcp starting",
		"exe_id", args.ExeID,
		"bridge_url", args.BridgeURL,
		"turn_id", args.TurnID,
	)

	bc, err := DialBridge(ctx, args.BridgeURL, token)
	if err != nil {
		return fmt.Errorf("dial bridge: %w", err)
	}
	defer bc.Close()

	initParams, _ := json.Marshal(ExecInitializeParams{ClientName: "codex-env-mcp"})
	if _, err := bc.Call(ctx, ExecMethodInitialize, initParams); err != nil {
		return fmt.Errorf("exec-server initialize: %w", err)
	}
	if err := bc.Notify(ctx, ExecMethodInitialized, nil); err != nil {
		return fmt.Errorf("exec-server initialized notify: %w", err)
	}

	tr := NewTranslator(bc)
	srv := NewMCPServer(args.ExeDesc, tr)
	if err := srv.Serve(ctx, stdin, stdout); err != nil {
		// EOF on stdin is the normal exit path (codex's MCP host
		// closed); io.EOF surfaces as nil from bufio.Scanner.Err(), so
		// any non-nil error here is genuinely abnormal.
		return fmt.Errorf("mcp serve: %w", err)
	}
	logger.Info("env-mcp clean exit (stdin closed)")
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -v
```
Expected: every test in the package passes (Tasks 2-6, 14+ tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/envmcp/envmcp.go internal/codexappgateway/envmcp/envmcp_test.go
git commit -m "feat(envmcp): wire bridge + translator + MCP server in Run"
```

---

## Task 7: Integration test against a real codex exec-server

**Files:**
- Create: `internal/codexappgateway/envmcp/integration_test.go`

The unit suite uses a fake exec-server and a fake translator; this test
boots a real `codex exec-server --listen ws://127.0.0.1:0` subprocess
(when the binary is available on PATH) and runs `Run()` against it,
asserting the full chain produces real `ls` output. Skipped when the
codex binary is missing so CI without it still passes.

- [ ] **Step 1: Write the test**

`internal/codexappgateway/envmcp/integration_test.go`:
```go
//go:build integration

package envmcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRun_AgainstRealCodexExecServer is opt-in: build with `-tags
// integration`. It requires `codex` on PATH (any version that supports
// `codex exec-server --listen ws://127.0.0.1:0`) and writes/reads
// from /tmp.
func TestRun_AgainstRealCodexExecServer(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not on PATH; skip integration test")
	}

	cmd := exec.Command("codex", "exec-server", "--listen", "ws://127.0.0.1:0")
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start codex: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// codex exec-server prints `ws://IP:PORT` on its first stdout line.
	scanner := bufio.NewScanner(stdoutPipe)
	if !scanner.Scan() {
		t.Fatalf("codex exec-server did not print listen URL")
	}
	wsURL := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(wsURL, "ws://") {
		t.Fatalf("unexpected first stdout line %q", wsURL)
	}

	t.Setenv("CXG_INT_TOKEN", "ignored-for-local-server")
	in := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shell","arguments":{"command":["sh","-c","printf integration-ok"]}}}`,
		"",
	}, "\n"))
	out := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := Run(ctx, RunArgs{
		ExeID:     "exe_int",
		BridgeURL: wsURL,
		TokenEnv:  "CXG_INT_TOKEN",
		ExeDesc:   "Integration",
	}, in, out, &bytes.Buffer{}, logger); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "integration-ok") {
		t.Fatalf("expected integration-ok in stdout, got: %s", out.String())
	}
}
```

- [ ] **Step 2: Run integration test**

```bash
cd /root/agentserver && go test -tags integration -run TestRun_AgainstRealCodexExecServer ./internal/codexappgateway/envmcp/ -v
```
Expected: PASS when codex is on PATH; SKIP otherwise. (Local dev box
has codex 0.128.0 — this should PASS per the spec's PoC #2.)

- [ ] **Step 3: Run the full envmcp suite once more**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/envmcp/ -v
go vet ./internal/codexappgateway/envmcp/ ./cmd/codex-app-gateway/
```
Expected: every test PASS, vet clean.

- [ ] **Step 4: Commit**

```bash
git add internal/codexappgateway/envmcp/integration_test.go
git commit -m "test(envmcp): integration test against real codex exec-server"
```

---

## Open risks (carried forward from spec § PoC #2)

1. **WS compression negotiation.** `nhooyr.io/websocket` defaults to no
   compression; if a future version flips that default the bridge dial
   silently breaks (codex exec-server closes deflate-requesting peers
   without reason text). Mitigation: integration test will catch this
   on CI.
2. **Cap-token Bearer rejected by exec-gateway.** This plan only forwards
   the token; verification lives in the codex-exec-gateway plan. If
   that plan rejects the token shape we mint, env-mcp surfaces it as
   "ws dial: ...HTTP 401". Acceptable: surfaces as MCP `initialize`
   failure on the codex side.
3. **No cancellation propagation.** A stuck `process/read` keeps the
   poll loop spinning until `defaultMaxReadCycles`. Phase 1 accepts
   this; the runtime plan can extend BridgeClient with a `process/terminate`
   helper bound to context cancellation when codex's MCP host adds
   `notifications/cancelled` (not in 0.128.0).
4. **Single shell tool only.** `apply_patch` is intentionally not exposed;
   the LLM uses heredoc patterns through `shell` per the spec § Subsystem
   4 rationale. If e2e shows the LLM struggles with this, the runtime
   plan can add `apply_patch` as a second MCP tool that internally uses
   the same translator with `argv = ["apply_patch", ...]` or `fs/writeFile`
   primitives.

---

## Self-review

**Spec coverage** (§ Subsystem 4 of `2026-05-10-codex-gateway-mcp-rewrite.md`):
- "stdio MCP server fronting one ws connection" → Tasks 5 + 6 ✓
- "single `shell` tool" with description carrying executor label → Task 5 ✓
- "translates MCP `tools/call` → `process/start` + `process/read` cycles" → Task 4 ✓
- "Returns aggregated stdout/stderr as MCP `text` content with `isError`" → Tasks 4, 5 ✓
- "Read CXG_BRIDGE_TOKEN_EXE_<ID> from env, dial with Bearer auth" → Task 6 ✓
- "Fail fast on 401/403/503 — surfaces as MCP initialize failure" → Task 6 returns error to caller, which Task 1 logs + exits non-zero ✓
- "On either side close, propagate close" → BridgeClient.Close + scanner EOF (Task 5+6) ✓
- "Subcommand of codex-app-gateway binary" → Task 1 ✓
- "MCP tool naming `mcp__<server>__<tool>`" → automatic via codex's MCP host (no env-mcp work needed)

Uncovered (deferred): cancellation hook (risk #3), apply_patch tool (risk #4), realistic non-PATH env (lib uses minimal hardcoded `PATH=/usr/bin:/bin:/usr/local/bin`; the runtime plan should plumb a configurable env per executor).

**Placeholder scan:** No `TBD`, no "implement appropriate error handling",
no "similar to Task N". All test code spelled out; all impl spelled out.

**Type consistency:** `RunArgs`, `ShellResult`, `BridgeCaller`,
`ShellRunner`, `MCPServer`, `Translator`, `BridgeClient`, `JSONRPCMessage`,
`JSONRPCError`, `MCPTool`, `MCPInitializeResult`, `MCPListToolsResult`,
`MCPCallToolParams`, `MCPCallToolResult`, `MCPToolContent`,
`ExecInitializeParams`, `ExecInitializeResult`, `ProcessStartParams`,
`ProcessStartResult`, `ProcessReadParams`, `ProcessReadResult`,
`ProcessOutputChunk` — each name used identically in every task that
references it. Method-name constants (`ExecMethodInitialize`,
`ExecMethodInitialized`, `ExecMethodProcessStart`, `ExecMethodProcessRead`)
likewise consistent.
