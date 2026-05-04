# Spec — Codex fork with remote harness

**Date**: 2026-05-04
**Status**: Approved (final architecture)
**Author**: agentserver team

> **Decision history**: this doc went through three drafts.
> - Draft 1: full Codex fork (tool-runtime patches, etc.) — overkill
> - Draft 2: zero-fork, use upstream `codex exec-server` as a listener and tunnel raw bytes through executor-registry yamux — workable but messy at the byte-relay layer
> - **Draft 3 (chosen)**: small Codex fork (~100 LOC, pure addition) adding a `--connect` mode to `codex exec-server` so it dials agentserver outbound. Architecturally cleaner: every cross-laptop-cluster connection is a normal `wss://` upgrade, no L4 tunnel reuse, no byte-relay opacity.
>
> See §6 for the rationale for the fork tradeoff.

## 1. Goal

Replace the from-scratch agentserver TUI (`internal/agent/tui/`) with a fork of OpenAI's Codex CLI that:

- Runs its **UI** on the user's laptop (Codex `tui` crate, mostly unchanged).
- Runs its **harness** (model loop, session state, tool dispatcher) on agentserver.
- Runs its **executor** (concrete tool implementations: bash, read_file, apply_patch, glob, grep, ls, etc.) on the user's laptop.
- Preserves Codex's UX, MCP support, sandboxing concepts, and approval flows where feasible.

**Why this might be better than the in-house TUI**:
- Codex already has years of UX polish, MCP integration, and a clean `app-server`/`tui` split.
- We stop maintaining a parallel TUI codebase.
- Users get a familiar, cross-vendor agent UI.

## 2. Feasibility

### 2.1 Codex already has a UI ↔ harness split

Codex's `app-server` crate **is** the harness; the `tui` crate is the client. They communicate via JSON-RPC defined in `app-server-protocol`. Today the TUI launches `app-server` either in-process or via stdio subprocess — but a websocket transport already exists at `/root/codex/codex-rs/app-server-transport/src/transport/websocket.rs`. So flipping UI ↔ harness onto a network is mostly configuration.

**Wire-boundary types we'd reuse verbatim**:
- `ClientRequest`, `ServerNotification` (JSON-RPC envelope) — `codex-rs/app-server-protocol/src/protocol/common.rs`
- `Op` (user submissions: `UserInput`, `UserTurn`, `ExecApproval`, `PatchApproval`, `ResolveElicitation`, `DynamicToolResponse`) — `codex-rs/protocol/src/protocol.rs`
- `EventMsg` (harness → UI: `TurnStarted`, `TurnComplete`, `AgentMessage`, `ExecApprovalRequested`, `McpToolCallBegin/End`, …)
- `Submission { id, op }`, `Event { id, msg }`

### 2.2 agentserver already has the remote-tools tunnel

cc-broker today does for Claude Code exactly what we want for Codex: model loop on the server, tools executed on the user's laptop. The stack we'd reuse:

- `agentserver-agent` opens a yamux tunnel to `executor-registry` on `/api/executors/{id}/tunnel` (PR #74).
- cc-broker's `remote_*` tools POST to `executor-registry/api/execute`, which routes through that tunnel to the laptop.
- Approval flow: SSE event `permission_request` → user decision via `/api/agents/sessions/{sid}/permissions/{pid}/decide` → broker's `Gate.Resolve()`.

We can plug Codex into this by ensuring its tool calls flow over the same tunnel.

### 2.3 The gap

What Codex doesn't have today and we need to build:
- A "remote-only" mode for tool runtimes (today bash/read/write run in the same process as the harness).
- Awareness of "the harness lives on a different machine from the executor".
- Session persistence outside SQLite (`codex-rs/state/`) — needs to reach Postgres for multi-replica safety.

These are bounded modifications, **not** a rewrite.

### 2.4 Risks

| Risk | Mitigation |
|---|---|
| Codex's tool runtime registration may not have a single chokepoint to swap | Phase 0 spike — confirm before committing to the plan |
| Codex's sandboxing assumes harness==executor (seccomp on Linux, UAPI on Windows) | Disable on harness side, enforce on laptop side |
| Tracking upstream Codex requires a rebase strategy | Minimize fork diff; aim to upstream the network-transport hooks |
| Tool dispatch latency over the tunnel hurts UX for trivial calls (`ls`) | Batch / pipeline tool calls; consider local short-circuit list |
| MCP today is stdio; routing it through a tunnel is non-trivial | v1: keep MCP servers laptop-side; harness instructs laptop to launch them |
| Codex's thread-store is local SQLite | Hybrid: thread metadata locally + per-event log in agentserver Postgres |

## 3. Architecture

```
                          USER LAPTOP                                     AGENTSERVER
                          ───────────                                     ─────────────────

                          ┌── codex-fork tui ─── wss://.../codex/ws ──→  codex-broker
                          │   (Rust, Codex     (app-server-protocol         │
                          │    fork, --remote)  over JSON-RPC)              ├── spawn forked codex
                          │                                                 │   app-server (stdio
                          │                                                 │   transport) per session
                          │                                                 │
                          │                                                 ├── proxy frames
                          │                                                 │   stdio ↔ websocket
                          │                                                 │
                          │                                                 ├── persist session
                          │                                                 │   in postgres
                          │   register exec ─── tunnel ──→  executor-registry
                          ├── (tunnel listener)                              │
                          │                                                 │   POST /api/execute
                          │   tool runtimes:                                 │   ├── tool_kind=codex_*
                          │     codex_bash                                  │   └── route via tunnel
                          │     codex_read                                  │       to executor_id
                          │     codex_write                                 │
                          │     codex_glob/grep/ls                          │
                          │     codex_apply_patch                           │
                          │                                                 │
                          └── exec ←── tool-call envelope ←─────────────────┘
                                       (over the same tunnel)
```

## 4. Wire protocols

agentserver speaks Codex's native protocols on the wire. There are **two** websocket connections from the laptop to agentserver per session, both initiated outbound from laptop:

### 4.1 TUI ↔ remote harness — Codex `app-server-protocol`

- Endpoint: `WSS /api/codex/sessions/{sid}/ws`
- Auth: `Authorization: Bearer <agentserver-token>` (Hydra-introspected)
- Wire format: upstream Codex `app-server-protocol` JSON-RPC (`JSONRPCRequest` / `Notification` / `Response`)
- Implementation: codex-broker frame-relays this WS to a per-session `codex-app-server` subprocess listening on a unix socket. **agentserver does not implement the protocol semantics** — it copies frames between the WS and the subprocess stdio.

Triggered by upstream `codex --remote wss://... --remote-auth-token-env AGENTSERVER_TOKEN` — no Codex change needed on this path.

### 4.2 exec-server ↔ remote harness — Codex `exec-server-protocol`

- Endpoint: `WSS /api/codex/sessions/{sid}/exec`
- Auth: `Authorization: Bearer <agentserver-token>`
- Wire format: upstream Codex exec-server JSON-RPC (`fs/readFile`, `fs/writeFile`, exec calls, etc.)
- Implementation: codex-broker accepts this WS, then internally bridges its frames against a separate WS that the per-session `codex-app-server` subprocess opens to `localhost:N` (where `N` is a port codex-broker binds for that session and points at via `CODEX_EXEC_SERVER_URL=ws://localhost:N`). The bridge is at the WebSocket message level, not raw bytes.

Triggered by **forked** `codex exec-server --connect wss://... --auth-token-env AGENTSERVER_TOKEN`. The `--connect` mode is the load-bearing fork addition (see §5.1).

### 4.3 Lifecycle

- Wrapper script `agentserver-agent codex` orchestrates: device flow → `POST /api/codex/sessions` to allocate `sid` → spawn `codex exec-server --connect` → exec into upstream `codex --remote`. All three connections (HTTP create-session, WS exec, WS TUI) carry the same bearer token and reference the same `sid`.
- codex-broker pairs (4.2) and (4.1) on `sid` when the harness subprocess is spawned.

### 4.4 Auth summary

| Edge | Mechanism |
|---|---|
| Wrapper / TUI / exec-server → agentserver | OAuth Bearer (Hydra) |
| codex-broker → spawned codex-app-server | unix socket (no auth, IPC) |
| codex-app-server → localhost:N (its `CODEX_EXEC_SERVER_URL`) | TCP loopback (no auth, IPC) |
| codex-broker → Postgres | existing internal cluster auth |

**Scope decision**: `app-server-protocol` is Codex-specific (skills, hooks, guardian, MCP, sandbox enums all baked in; no third-party server implementations; no version field). We use it on the Codex path only. Existing `/api/agents/*` SSE+REST surface (cc-broker / Claude / web frontend) stays untouched.

### 4.2 Harness ↔ local executor

Codex's `ToolRouter::invoke()` must send the call to the user's laptop instead of executing in-process. **Approach A (recommended)**: introduce a `RemoteToolForwarder` adapter that POSTs to executor-registry:

```http
POST {executor-registry}/api/execute
Content-Type: application/json

{
  "executor_id": "exe_<user_executor>",
  "tool_kind": "codex_bash",
  "session_id": "cse_...",
  "approval_token": "perm_...",      // present iff approved by user
  "arguments": {                      // codex-native shape, passthrough
    "command": "ls -la",
    "working_dir": "/home/user/repo"
  },
  "timeout_ms": 60000
}
```

Response (success):
```json
{
  "exit_code": 0,
  "stdout": "...",
  "stderr": "",
  "duration_ms": 142,
  "tool_call_id": "tc_..."
}
```

Routing: executor-registry adds tool_kind validation but otherwise reuses today's tunnel framing.

**Approach B (defer to v2)**: replace built-in tools with MCP servers run on the laptop, configured remotely. Cleaner architecturally, harder operationally. Skip for now.

### 4.3 Auth summary

| Edge | Mechanism |
|---|---|
| TUI ↔ codex-broker (over WS) | OAuth Bearer (Hydra introspection) |
| codex-broker → executor-registry | internal cluster, no extra auth |
| executor-registry → laptop (tunnel) | tunnel token (existing) |
| TUI / agent CLI → register executor | OAuth Bearer (existing) |

## 5. Detailed changes

### 5.1 Codex fork — `--connect` mode for `codex exec-server`

Goal: let `codex exec-server` dial agentserver outbound instead of always being a server that listens for inbound. **All other Codex behavior (TUI, app-server, harness, protocol crates) stays upstream-untouched.**

The patch is intentionally a **pure addition**, gated behind a new CLI flag, so that:
- existing `--listen` mode is byte-identical to upstream
- rebase against upstream changes is trivial (no edits to existing code paths)

**Files touched (~100 LOC):**

| File | Change | LOC |
|---|---|---|
| `exec-server/src/server/transport.rs` | Add `pub async fn run_connect_mode(connect_url, auth_token, runtime_paths)`. Reuses `ConnectionProcessor::run_connection` with a client-side `JsonRpcConnection::from_websocket`. | ~50 |
| `exec-server/src/connection.rs` | Make `from_websocket` public (it's already generic over `S: AsyncRead + AsyncWrite + Unpin + Send + 'static`, which the client-side `WebSocketStream<MaybeTlsStream<TcpStream>>` satisfies). Or add a small public wrapper. | ~5 |
| `exec-server/src/lib.rs` | `pub use server::transport::run_connect_mode;` | 1 |
| `cli/src/main.rs` | Extend `ExecServerCommand` with `--connect <URL>` and `--auth-token-env <ENV>`, mutually exclusive with `--listen`. Dispatch in `run_exec_server_command`. | ~25 |
| `exec-server/tests/connect_mode.rs` (new) | Spawn an in-process WS server, run `codex exec-server --connect`, do a round-trip `fs/readFile` against an in-memory file system. | ~50 |

**Sketch:**

```rust
// exec-server/src/server/transport.rs (added function)
pub async fn run_connect_mode(
    connect_url: &str,
    auth_token: Option<&str>,
    runtime_paths: ExecServerRuntimePaths,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    use tokio_tungstenite::tungstenite::client::IntoClientRequest;

    let mut request = connect_url.into_client_request()?;
    if let Some(t) = auth_token {
        request.headers_mut().insert(
            "Authorization",
            format!("Bearer {t}").parse()?,
        );
    }
    let (ws, _resp) = tokio_tungstenite::connect_async(request).await?;

    let processor = ConnectionProcessor::new(runtime_paths);
    let conn = JsonRpcConnection::from_websocket(
        ws,
        format!("exec-server connect {connect_url}"),
    );
    processor.run_connection(conn).await;
    Ok(())
}
```

```rust
// cli/src/main.rs (ExecServerCommand additions)
#[arg(long = "connect", value_name = "URL", conflicts_with = "listen")]
connect: Option<String>,

#[arg(long = "auth-token-env", value_name = "ENV", requires = "connect")]
auth_token_env: Option<String>,
```

**What we deliberately do NOT change in the fork:**
- TUI (`tui/`) — upstream `--remote` flag does what we need
- `app-server` — upstream binary spawned by codex-broker, no changes
- `protocol`, `app-server-protocol` — frame definitions stay upstream
- `core/`, `tools/` — model loop and tool router stay upstream
- `exec-server/` everything else — the existing `--listen` mode is byte-identical to upstream

**Fork strategy:**
- Branch off a pinned upstream tag (e.g., `v0.x.y-agentserver`).
- Patch lives in our `codex-fork` repo as a single commit on top of upstream.
- Rebase cadence: every 2 weeks initially. Conflicts unlikely because the patch is pure addition.
- **Aspirational**: upstream the `--connect` mode (it's a generally-useful capability). If accepted, fork shrinks to zero.

### 5.2 agentserver changes (Go)

#### 5.2.1 New `codex-broker` service

Path: `internal/codexbroker/` — independent lifecycle, distinct config from cc-broker.

**Core design: thin frame relay.** The broker does **not** implement `app-server-protocol` semantics. It spawns **upstream Codex `codex-app-server`** as a subprocess and proxies frames between TUI websocket and subprocess stdio. This means agentserver tracks upstream protocol changes automatically and we don't reimplement Codex's turn loop in Go.

Per-session lifecycle:

1. **Session creation** (HTTP one-shot): client `POST /api/codex/sessions` → `{ "session_id": "cse_..." }`. Bearer-authed. Writes Postgres row with `status='pending'`. **No subprocess yet.**

2. **exec-server connection arrives** (WS): forked `codex exec-server --connect` reaches `/api/codex/sessions/{sid}/exec` with bearer header. codex-broker:
   - validates ownership of `sid`
   - upgrades WS, holds the connection
   - **at this point or later**, spawns `codex-app-server --listen unix:///tmp/codex-{sid}.sock` with env:
     - `CODEX_HOME=/tmp/codex-home/{sid}` (per-session isolation)
     - `CODEX_EXEC_SERVER_URL=ws://localhost:{N}` (locally bound port for harness's outbound exec dial)
     - model-provider / API base URL config injected via `~/.codex/config.toml` written into `CODEX_HOME`
   - waits for the subprocess to dial `localhost:N` (timeout ~5s)
   - **bridges** the harness's outbound WS (to localhost:N) ↔ the laptop's `--connect` WS at the WebSocket message level

3. **TUI connection arrives** (WS): upstream `codex --remote` reaches `/api/codex/sessions/{sid}/ws`. codex-broker validates ownership, upgrades, frame-relays this WS to the spawned subprocess's unix socket.

4. **Frame relay** is pure passthrough at the JSON-RPC message level for both (2)'s exec bridge and (3)'s TUI bridge. Async tap to `codex_session_events` for audit (non-blocking).

5. **Disconnect handling**:
   - TUI WS drops → keep subprocess + exec-bridge alive for grace period (5 min) for reconnect
   - exec-server WS drops → harder failure; the harness will get IO errors on its `CODEX_EXEC_SERVER_URL` connection; v1 marks session failed
   - subprocess exit → mark session ended in DB

Notable code units to add:
- `internal/codexbroker/session.go` — `Session` struct + lifecycle state machine
- `internal/codexbroker/subprocess.go` — `codex-app-server` spawn + waitDialUnix + cleanup
- `internal/codexbroker/relay.go` — bidirectional WS frame copy, with optional tap
- `internal/codexbroker/exec_pair.go` — pair the exec-server WS with the harness's outbound localhost:N connection on `sid`
- `internal/codexbroker/persist.go` — Postgres helpers

**Frame-relay vs. reimplementation**: codex-broker does not parse `app-server-protocol` or `exec-server-protocol`. It copies WebSocket messages verbatim. This means upstream Codex protocol additions flow through automatically; we never have to write a Go decoder for `EventMsg` etc.

#### 5.2.2 Server routes

In `internal/server/server.go`, add to the bearer-only group:

```go
r.Get("/api/codex/sessions", s.handleListCodexSessions)
r.Post("/api/codex/sessions", s.handleCreateCodexSession)
r.Get("/api/codex/sessions/{sid}/ws",   s.handleCodexTuiWS)        // TUI bridge
r.Get("/api/codex/sessions/{sid}/exec", s.handleCodexExecServerWS) // exec-server bridge
r.Delete("/api/codex/sessions/{sid}", s.handleDeleteCodexSession)
```

(Permission decisions flow in-band over the TUI WS via Codex's existing `Op::ExecApproval` — no separate REST endpoint.)

#### 5.2.3 executor-registry: NOT extended

Earlier draft (option A) extended the yamux tunnel for L4 byte forwarding. **Final architecture (option B) does not touch executor-registry**. The forked `codex exec-server` makes its own outbound WS to agentserver, bypassing the existing tunnel infrastructure entirely.

`internal/executorregistry/handler_execute.go`:
- Add `tool_kind` validation; existing kinds (`Bash`, `Read`, …) keep working
- Add codex_* family: `codex_bash`, `codex_read`, `codex_write`, `codex_glob`, `codex_grep`, `codex_ls`, `codex_apply_patch`
- Per-tunnel registration of supported tool kinds (so an old agent without codex tools doesn't accept codex calls)

#### 5.2.4 Persistence

Tables:
```sql
CREATE TABLE codex_sessions (
  id            text PRIMARY KEY,
  workspace_id  text NOT NULL REFERENCES workspaces(id),
  owner_user_id text NOT NULL REFERENCES users(id),
  title         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE codex_session_events (
  id          bigserial PRIMARY KEY,
  session_id  text NOT NULL REFERENCES codex_sessions(id) ON DELETE CASCADE,
  seq         bigint NOT NULL,
  kind        text NOT NULL,            -- "submission" | "event"
  payload     jsonb NOT NULL,           -- raw frame
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (session_id, seq)
);
```

Resume on reconnect: replay events with `seq > last_event_id` from the table, then attach to the running broker subprocess (or respawn and re-feed history if the subprocess died).

#### 5.2.5 Helm + ingress

- Helm chart: new deployment `agentserver-codex-broker`, env `CODEX_BIN`, `DATABASE_URL`, `EXECUTOR_REGISTRY_URL`, `LLMPROXY_URL`
- HTTPRoute: ensure `/api/codex/*` routes to agentserver (already true under the catch-all `/`)

### 5.3 Local agent CLI

`agentserver-agent codex` wrapper subcommand orchestrates the laptop side:

1. Resolve / refresh credentials in `~/.agentserver/credentials.json` (existing Hydra device flow).
2. `POST /api/codex/sessions` → receive `sid`.
3. Start `codex-fork exec-server --connect wss://.../api/codex/sessions/{sid}/exec --auth-token-env AGENTSERVER_TOKEN` as background subprocess; wait for "connected" confirmation on its stderr.
4. Replace process image with upstream `codex --remote wss://.../api/codex/sessions/{sid}/ws --remote-auth-token-env AGENTSERVER_TOKEN` (Unix `execve` syscall).

Wrapper distribution: same `agentserver-agent` binary that currently handles agent registration. Bundle `codex-fork exec-server` and upstream `codex` binaries alongside in release archives.

## 6. Phased build

| Phase | Goal | Days | Done when |
|---|---|---|---|
| 0 | **Validation spikes** — confirm `--connect` patch is feasible (ConnectionProcessor reusable), confirm `codex-app-server --listen ws://...` is production-ready, confirm `chatgpt_base_url` override works | 1–2 | DONE (see `2026-05-04-codex-fork-phase-0-findings.md`) |
| 1 | **Codex fork** — `--connect` mode for `codex exec-server`, fork repo, CI image build | 3 | `codex-fork exec-server --connect ws://localhost:9999/path` connects to a local stub WS, completes a `fs/readFile` round-trip |
| 2 | **agentserver codex-broker minimal** — Postgres tables, `/api/codex/sessions/{sid}/ws` endpoint, spawn `codex-app-server` subprocess, frame relay | 4–5 | TUI on laptop runs `codex --remote wss://localhost/api/codex/sessions/{sid}/ws` (against a local agentserver dev instance), JSON-RPC initialize completes; chat-only turn (no tools) works |
| 3 | **exec-server pairing** — `/api/codex/sessions/{sid}/exec` endpoint, codex-broker localhost:N bridge, both WS connections paired on `sid` | 3 | One full turn: user prompt → harness → bash tool → laptop's exec-server → result back to TUI |
| 4 | **Wrapper + e2e** — `agentserver-agent codex` orchestrator, deploy to staging, smoke test from real laptop against `agent.cs.ac.cn` | 2 | New developer can run `agentserver-agent codex` cold and have a working session |
| 5 | **Reconnect + persistence** — Postgres event log, TUI reconnect with replay, subprocess grace timeout | 2 | TUI WS killed mid-turn, reconnect, see assistant message replayed |
| 6 | **Approvals UX** — verify `ExecApproval` round-trip, sandbox policy default for remote-exec | 1 | Allow / deny / always works through the chain |
| 7 | **Helm + production deploy** — `agentserver-codex-broker` deployment, ingress, observability | 2 | Deployed to `agent.cs.ac.cn`, dogfooded |

**Total to MVP: ~17 working days ≈ 3.5 weeks** (one engineer, full-time).

## 6.5 On not unifying everything onto app-server-protocol

A reasonable instinct is "since we're adopting app-server-protocol, why not also retire `/api/agents/*` (cc-broker SSE+REST) and run Claude through the same wire?" An audit of the protocol shows this is a bad trade:

- The `ClientRequest` / `ServerNotification` / `Op` / `EventMsg` catalogs are **deeply Codex-specific** — `SkillsList`, `HooksList`, `ThreadApproveGuardianDeniedAction`, `ThreadRealtimeAppendAudio`, `ItemGuardianApprovalReviewStarted` etc. are built around Codex's internal subsystems.
- `SandboxPolicy` is Unix-path / Codex-sandbox specific; `AskForApproval::Granular` carries `skill_approval` and `mcp_elicitations` fields that have no Claude equivalent.
- `McpServerElicitationRequest` is a server-initiated request that blocks turn flow; non-MCP-using harnesses can't cleanly stub it.
- The protocol has **no version field** and no formal stability guarantees — only `#[experimental("...")]` annotations.
- **No third-party server implementations exist**; the "community" leverage is essentially "the Codex TUI itself".

Translating Claude turn semantics into this protocol shape would be lossy and introduce a permanent maintenance tax. Better stance: app-server-protocol is **the Codex path's protocol**, not a universal one. If a generic agent-platform protocol becomes desirable later, it would be a separate effort (likely built on MCP — which does have multi-vendor adoption — plus a thin chat extension).

## 7. Open questions

**Resolved during Phase 0:**

- ~~Tool chokepoint — does Codex's ToolRouter have a single hook to swap?~~ **Resolved**: we don't touch ToolRouter at all. The architecture pivots on `--connect` mode for `exec-server` instead.
- ~~Pricing / quota — does Codex support `OPENAI_BASE_URL` override?~~ **Resolved**: yes, both via top-level `chatgpt_base_url` and per-provider `model_providers.{name}.base_url`. Already in production use in the dogfood `~/.codex/config.toml`.
- ~~Reading credentials — how does TUI consume the bearer?~~ **Resolved**: upstream `--remote-auth-token-env <ENV_VAR>` is sufficient; wrapper sets the env var.

**Still open for v1:**

1. **Sandbox policy** — Codex's `SandboxPolicy::WorkspaceWrite` etc. is enforced at the exec-server side. With our deployment, the exec-server runs on the laptop where the workspace lives, so policy enforcement is correct in concept — but the harness has to decide policy without seeing the laptop's actual files. v1: harness sends policy spec via `exec-server-protocol`, laptop's exec-server interprets it as it would in standalone mode. Spike in Phase 3 to confirm semantics.
2. **MCP** — laptop-side MCP servers as stdio subprocesses spawned by the forked exec-server. v1 doesn't address remote MCP servers; future work.
3. **Multi-machine** — one user, two laptops. v1 supports one session per `agentserver-agent codex` invocation. Multiple parallel invocations from multiple laptops yield multiple parallel sessions; each is isolated.
4. **Data residency** — Postgres event log + Codex SQLite (per-session pod-local) + S3 tarballs for workspace mirrors. Document trust boundary clearly before dogfood.
5. **Rebase cadence** — track upstream every 2 weeks initially. Patch is pure addition so conflicts unlikely.
6. **Should we sunset the in-house TUI immediately?** Run in parallel for one release cycle. Sunset after Phase 5 lands and dogfood confirms parity.

## 8. Reference comparison

For context — three architectures we drew from:

| Aspect | Codex (today) | Claude Code v2.1 (`/root/cc`) | agentserver cc-broker |
|---|---|---|---|
| UI ↔ harness split | Yes (`tui` ↔ `app-server`, JSON-RPC) | Yes (REPL ↔ `QueryEngine`, via `bridge/`) | Yes (TUI ↔ broker, via SSE+POST) |
| Harness location | Local subprocess or in-proc | Local or remote (CCR v2) | Remote (cluster) |
| Tool location | Local in-proc | Local in-proc; remote-MCP available | Remote (laptop, via tunnel) |
| Wire format | `Submission`/`Event` JSON-RPC | `SDKMessage`+`SDKControl*` | Server-Sent Events + REST |
| Persistence | SQLite (`state/log_db`) | JSONL transcripts | Postgres + S3 workspace tarballs |
| Approval flow | `ExecApprovalRequested` → `Op::ExecApproval` | `SDKControlPermissionRequest` → `SDKControlResponse` | `permission_request` event → POST decide |

The Codex split is the cleanest of the three for our purposes — that's why this plan is viable.
