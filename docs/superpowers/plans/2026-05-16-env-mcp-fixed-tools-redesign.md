# env-mcp Fixed-Tools Redesign Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox `- [ ]` syntax for tracking.

**Goal:** Replace per-executor env-mcp children with one fixed MCP server exposing `list_environments` plus 7 fixed remote-execution tools that take `env_id`.

**Architecture:** See `docs/superpowers/specs/2026-05-16-env-mcp-fixed-tools-redesign.md`.

**Tech Stack:** Go 1.22, nhooyr.io/websocket, protobuf relay envelopes (existing), chi router.

---

## Task 1 — Cap-token payload simplification

**Files:**
- Modify: `internal/codexexecgateway/auth.go`
- Modify: `internal/codexexecgateway/auth_test.go`
- Modify: `internal/codexappgateway/server.go` (caller)

- [ ] Drop `ExeIDs []string` from `CapabilityTokenPayload`; delete `AllowsExeID`.
- [ ] Update `MintCapabilityToken` (or wrapper) signature to take `{TurnID, WorkspaceID, IssuedAt, ExpiresAt}` only.
- [ ] Update tests: remove every assertion on `ExeIDs` / `AllowsExeID`; add `TestVerifyCapabilityToken_Workspace` that round-trips the new payload.
- [ ] Update caller in `codexappgateway/server.go` to mint one workspace-scoped token per turn (no per-exe loop here yet — that comes in Task 7).
- [ ] `go test ./internal/codexexecgateway/... ./internal/codexappgateway/...` passes.
- [ ] Commit: `refactor(codex-gateway): cap-token becomes workspace-scoped`

---

## Task 2 — /bridge handler verifies workspace ownership

**Files:**
- Modify: `internal/codexexecgateway/bridge.go`
- Modify: `internal/codexexecgateway/store.go`
- Modify: `internal/codexexecgateway/bridge_test.go`

- [ ] Add to store:
  ```go
  func (s *PostgresStore) OwnsExecutor(ctx context.Context, workspaceID, exeID string) (bool, error)
  ```
  Implemented as `SELECT COUNT(1) FROM workspace_executors WHERE workspace_id=$1 AND exe_id=$2`.
- [ ] In `bridge.go`, replace the `payload.AllowsExeID(exeID)` block with `s.store.OwnsExecutor(ctx, payload.WorkspaceID, exeID)` (2s context timeout, 500 on DB error, 403 on not-owned).
- [ ] Add tests:
  - `TestBridge_RejectsWrongWorkspaceExeID` — token's workspace_id ≠ row's → 403
  - `TestBridge_AcceptsExeIDInWorkspace` — matching pair → upgrade succeeds
- [ ] `go test ./internal/codexexecgateway/...` passes.
- [ ] Commit: `feat(codex-exec-gateway): /bridge auth checks workspace ownership`

---

## Task 3 — /internal/connected loopback endpoint on codex-app-gateway

**Files:**
- Create: `internal/codexappgateway/internal_api.go`
- Create: `internal/codexappgateway/internal_api_test.go`
- Modify: `internal/codexappgateway/server.go` (route)
- Modify: `internal/codexappgateway/supervisor/supervisor.go` (per-spawn loopback token)
- Modify: `internal/codexappgateway/supervisor/spawn.go` (inject `CXG_LOOPBACK_TOKEN` env)

- [ ] Per spawn, supervisor mints a 32-byte random token, stores `map[token]workspaceID`, hands it to the subprocess via env `CXG_LOOPBACK_TOKEN`.
- [ ] On supervisor `Shutdown`, evict that token from the map.
- [ ] Expose:
  ```go
  func (s *Supervisor) LookupWorkspaceForLoopbackToken(tok string) (string, bool)
  ```
  Uses constant-time compare in the loop body.
- [ ] Handler `GET /internal/connected`:
  - Reject non-loopback RemoteAddr (host ≠ 127.0.0.1/::1) → 403.
  - Read `X-Loopback-Token` header → `LookupWorkspaceForLoopbackToken` → 401 on miss.
  - Call existing `execClient.Connected(ctx, workspaceID)`.
  - Return JSON `[{env_id, description, is_default, last_seen}]`.
- [ ] Wire route in `Routes()`.
- [ ] Tests:
  - `TestInternalConnected_OnlyBoundToLoopback`
  - `TestInternalConnected_RequiresLoopbackToken`
  - `TestInternalConnected_ReturnsList`
- [ ] Commit: `feat(codex-app-gateway): /internal/connected loopback endpoint for env-mcp`

---

## Task 4 — env-mcp BridgePool

**Files:**
- Create: `internal/codexappgateway/envmcp/pool.go`
- Create: `internal/codexappgateway/envmcp/pool_test.go`

- [ ] Type:
  ```go
  type BridgePool struct { gatewayURL, token string; logger *slog.Logger; mu sync.Mutex; conns map[string]*BridgeClient }
  func NewBridgePool(gatewayURL, token string, logger *slog.Logger) *BridgePool
  func (p *BridgePool) Get(ctx context.Context, exeID string) (*BridgeClient, error)
  func (p *BridgePool) Close()
  ```
- [ ] Semantics:
  - Cache hit + still open → return.
  - Cache hit + closed → drop entry, dial fresh.
  - Cache miss → dial outside the lock; on success store; on race lose-but-reuse.
  - After successful dial, send `initialize` + `initialized` notify; if either fails, treat dial as failed.
- [ ] Tests:
  - `TestBridgePool_DialsOncePerExeID`
  - `TestBridgePool_RedialsAfterClose`
  - `TestBridgePool_ParallelGetSameID` (10 goroutines, 1 underlying dial)
- [ ] Commit: `feat(env-mcp): BridgeClient pool keyed by exe_id`

---

## Task 5 — Tool registry + 8 fixed tools

Sub-steps each get their own commit.

**Files:**
- Modify: `internal/codexappgateway/envmcp/mcp_server.go`
- Create: `internal/codexappgateway/envmcp/tools.go`
- Create: `internal/codexappgateway/envmcp/tool_shell.go`
- Create: `internal/codexappgateway/envmcp/tool_unified_exec.go`
- Create: `internal/codexappgateway/envmcp/tool_fs.go`
- Create: `internal/codexappgateway/envmcp/tool_list_envs.go`
- Create: `internal/codexappgateway/envmcp/apply_patch.go`
- Create: `internal/codexappgateway/envmcp/apply_patch_test.go`
- Create: `internal/codexappgateway/envmcp/tool_apply_patch.go`
- Create: per-tool `_test.go` files
- Delete: `internal/codexappgateway/envmcp/translator.go` (logic moves into tool files)

### 5a — Registry skeleton
- [ ] Interface:
  ```go
  type Tool interface {
      Name() string
      Description() string
      InputSchema() json.RawMessage
      Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error)
  }
  ```
- [ ] MCPServer holds `map[string]Tool`; `tools/list` derives from it; `tools/call` dispatches via name.
- [ ] Commit: `feat(env-mcp): tool registry abstraction`

### 5b — list_environments
- [ ] Type `ListEnvironmentsTool{ url, token string; httpClient *http.Client }`.
- [ ] Call hits the loopback URL with `X-Loopback-Token` header, 3s timeout, returns the JSON as MCP text content.
- [ ] httptest.Server-backed unit test.
- [ ] Commit: `feat(env-mcp): list_environments tool`

### 5c — shell (env_id multiplexed)
- [ ] Type `ShellTool{ pool *BridgePool }`.
- [ ] Schema includes `env_id`, `command:[]`, optional `cwd`, `timeout_ms`.
- [ ] Body: pool.Get → process/start → poll process/read (same code path as today's RunShell, factored into a helper).
- [ ] If pool.Get fails (no such executor) → `isError=true` content with hint to call list_environments.
- [ ] Tests against fake relay server.
- [ ] Commit: `feat(env-mcp): shell tool (workspace-multiplexed)`

### 5d — unified_exec / write_stdin / read_output / terminate
- [ ] env-mcp keeps `sessions map[string]struct{ exeID, processID string; createdAt time.Time }`; session_id is a fresh UUID returned to LLM.
- [ ] `unified_exec({env_id, command:[], cwd?})` → process/start → wrap pid as session_id; return `{session_id}`.
- [ ] `write_stdin({env_id, session_id, data})` → lookup → process/write (base64 wrap data).
- [ ] `read_output({env_id, session_id, after_seq?, wait_ms?})` → process/read passthrough; result has `{chunks, next_seq, exited, exit_code}`.
- [ ] `terminate({env_id, session_id})` → process/terminate, drop session.
- [ ] Background GC: sessions older than 30 minutes get reaped + their processes terminated.
- [ ] Tests with full session lifecycle.
- [ ] Commit: `feat(env-mcp): unified_exec family of tools`

### 5e — read_file
- [ ] Schema `{env_id, path, offset?, limit?}`.
- [ ] `fs/readFile` call; decode base64; return as text content (or hex if non-utf8 — see codex's behavior).
- [ ] Add `FS_READ_FILE_METHOD = "fs/readFile"` to types.go constants.
- [ ] Tests.
- [ ] Commit: `feat(env-mcp): read_file tool`

### 5f — apply_patch
- [ ] Pure-Go parser for codex's apply_patch grammar in `apply_patch.go`. Reference grammar: `/root/codex/codex-rs/core/src/tools/handlers/apply_patch.lark`.
- [ ] Output structure: `[]FileOp` where `FileOp = {Op: add|update|delete|move, Path, Contents?, NewPath?, Hunks?}`.
- [ ] Parser unit tests with fixtures: empty, add, update single-hunk, update multi-hunk, delete, move.
- [ ] Tool body for each op:
  - **add** → `fs/writeFile`
  - **update** → `fs/readFile` → apply hunks in memory (3-line context match) → `fs/writeFile`
  - **delete** → `fs/remove`
  - **move** → `fs/copy` then `fs/remove` (exec-server has no fs/move)
- [ ] Per-file outcome surfaced in MCP content as `path: ok` / `path: error: <msg>`.
- [ ] Add fs/write/remove/copy method constants.
- [ ] Tests with fake exec-server.
- [ ] Commit: `feat(env-mcp): apply_patch tool with pure-Go grammar parser`

---

## Task 6 — env-mcp Run() rewiring

**Files:**
- Modify: `internal/codexappgateway/envmcp/envmcp.go`
- Modify: `cmd/codex-app-gateway/main.go` (env-mcp subcommand flags)

- [ ] Replace `RunArgs`:
  ```go
  type RunArgs struct {
      WorkspaceID       string  // --workspace-id
      ExecGatewayURL    string  // --exec-gateway-url  (base; pool appends /bridge/<exe_id>)
      AppGatewayInternal string // --app-gateway-internal  (e.g. http://127.0.0.1:8080)
      WorkspaceTokenEnv string  // --workspace-token-env
      LoopbackTokenEnv  string  // --loopback-token-env
  }
  ```
- [ ] `Run()`: read both tokens from env, construct pool, construct tool list, hand to MCPServer.
- [ ] No `initialize` call here — initialize moves into BridgePool's first-time-dial path (per exe_id).
- [ ] Update `cmd/codex-app-gateway/main.go`'s env-mcp subcommand flag parsing.
- [ ] Commit: `feat(env-mcp): Run() rewires to pool + fixed tool registry`

---

## Task 7 — codexhome single-server output

**Files:**
- Modify: `internal/codexappgateway/codexhome/codexhome.go`
- Modify: `internal/codexappgateway/codexhome/codexhome_test.go`
- Modify: `internal/codexappgateway/server.go`

- [ ] Replace `ConfigInput.Executors []ExecutorEntry` with:
  ```go
  type AgentServerMCP struct {
      CodexBin              string
      WorkspaceID           string
      ExecGatewayURL        string
      AppGatewayInternalURL string
      WorkspaceToken        string
      LoopbackToken         string
  }
  ```
- [ ] Replace the per-executor TOML loop with a single `[mcp_servers.agentserver]` block embedding the env-mcp argv + `env = { CXG_WORKSPACE_TOKEN = ..., CXG_LOOPBACK_TOKEN = ... }`.
- [ ] `server.go` builder: mint one workspace cap-token + one loopback token per spawn; populate AgentServerMCP; drop the per-executor loop and `CXG_BRIDGE_TOKEN_<EXE>` env vars.
- [ ] Update / replace tests: assert exactly one `[mcp_servers.agentserver]` section, correct args list, both env vars present.
- [ ] Commit: `refactor(codex-app-gateway): config.toml emits one fixed mcp_servers.agentserver`

---

## Task 8 — Drop /admin/sessions/restart

**Files:**
- Modify: `internal/codexappgateway/server.go`
- Search and clean up any callers (web UI, docs)

- [ ] Remove `r.Post("/admin/sessions/restart", s.handleAdminRestart)` from Routes.
- [ ] Remove `handleAdminRestart` method.
- [ ] Search the tree for `admin/sessions/restart` references; remove or note as follow-up.
- [ ] `go test ./internal/codexappgateway/...`
- [ ] Commit: `chore(codex-app-gateway): drop /admin/sessions/restart (executor list is live)`

---

## Task 9 — Integration test refresh

**Files:**
- Modify: `internal/codexappgateway/envmcp/integration_test.go`

- [ ] Fake exec-server speaking relay-wrapped protobuf (reuse existing helper).
- [ ] Drive env-mcp Run() with stdin/stdout pipes.
- [ ] Assertions:
  - `tools/list` returns exactly the 8 tools by name.
  - `list_environments` returns the fixture's connected list (mocked /internal/connected handler).
  - `shell({env_id: "fake", command: ["echo","hi"]})` returns `"hi\n[exit_code=0]"`.
  - `apply_patch` for an `add` op causes fs/writeFile to be called on the fake.
- [ ] Commit: `test(env-mcp): full-stack integration test for redesign`

---

## Task 10 — Chart bump + tag + deploy

**Files:**
- Modify: `deploy/helm/agentserver/Chart.yaml` (0.50.12 → 0.51.0)
- Modify: `/root/k8s/stacks/agentserver.ts` (chart version)

- [ ] `go test ./...` clean across the board.
- [ ] Bump Chart.yaml `version` + `appVersion` to `0.51.0`.
- [ ] Commit: `release: v0.51.0 — fixed-tool env-mcp redesign`
- [ ] Tag `v0.51.0`, push, wait for CI Build and Publish workflow.
- [ ] Update pulumi stack file (already prepared in step) — bump `version:` to `0.51.0`.
- [ ] Pulumi preview targeted at `helm-agentserver`; user confirms; pulumi up.
- [ ] `kubectl rollout restart` both `codex-app-gateway` and `codex-exec-gateway` deployments (both pick up new image tag = main).
- [ ] Smoke test: user reconnects, asks "what environments are available", expects `list_environments` to surface the registered executor without `/admin/sessions/restart`.

---

## Out of scope (record for v2)

- `grep_files`, `write_file`, fs/getMetadata/readDirectory/remove/copy MCP tools
- `http/request` exposure
- Streaming `process/output` push notifications (still polling via `read_output`)
- Connection pooling across codex app-server subprocess restarts
- Per-tool authorization (granular access beyond workspace boundary)
