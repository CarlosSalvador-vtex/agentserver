# codex-app-gateway + codex-exec-gateway — Design Spec

**Status:** draft
**Date:** 2026-05-05
**Owner:** agentserver / codex integration
**Scope:** Three subsystems that together let users run codex locally with a
remote harness, contribute their machine as a remote executor, and let a
single spawned codex turn fan out to multiple executors:

1. `codex-app-gateway` — Go service that speaks the codex **app-server v2**
   JSON-RPC protocol over WebSocket to local `codex-app` clients (TUI users),
   spawns `codex exec` per turn (via `codex-agent-sdk-go`), and translates
   SDK events back into app-server `ServerNotification`s.
2. `codex-exec-gateway` — Go service that speaks the codex **exec-server**
   JSON-RPC protocol on two ws surfaces: inbound from `codex-exec`
   (`codex exec-server --connect`) acting as an executor, and outbound for
   spawned codex processes that need to dispatch RPCs to a chosen executor.
3. **codex fork patches P1–P4** — additive, fork-only changes that make the
   codex CLI capable of loading multiple `Environment`s from a JSON manifest
   and letting the LLM pick one per tool call.

## Why

Need:

1. **Local codex TUI ↔ remote harness.** A user runs `codex-app` on their
   laptop; the LLM/agent runs on agentserver. The local app is a thin client
   that speaks codex's native app-server protocol to the gateway and renders
   events.
2. **Local codex as executor.** A user runs `codex-exec` on their laptop and
   contributes it as a remote-executor. Spawned codex turns can dispatch
   shell / file ops to it without bespoke tunneling.
3. **Multi-executor per turn.** A single spawned codex process can fan out
   commands to several executors during one turn, with the LLM choosing
   which one per tool call.

Existing pieces we reuse:

- `codex` (fork) `RemoteAppServerClient` — local TUI already supports
  connecting to a remote app-server over wss
- `codex` (fork) `codex exec-server --connect` — local exec-server already
  supports outbound ws dial-out with bearer auth
- `codex` v2 protocol — already defines `TurnEnvironmentSelection` and
  `environments: Vec<...>`, but the LLM-tool layer and CLI loader only
  honor `environments[0]`. Patches P1–P4 finish what the protocol layer
  already declared.
- `codex-agent-sdk-go` — Go SDK wrapping `codex exec --experimental-json`,
  fully aligned with the TS SDK on wire and API. Used as the per-turn
  driver inside `codex-app-gateway`.

This is **not** compatible with the existing cc-broker turn API. cc-broker
may migrate to this stack later; that migration is out of scope.

## Non-goals

- Not implementing the full ~144-RPC codex app-server / exec-server surface
  in phase 1. Phase 1 covers 8 client requests + 1 client notification + 8
  server notifications + 4 server-request approvals = 21 RPCs (the minimum
  for a usable TUI session). The rest is reserved for later phases (see
  § Phase 1 vs deferred).
- Not extending `executor-registry`. The new gateways are independent from
  the yamux+MCP tunnel registry. Existing executor-registry continues to
  serve cc-broker; once cc-broker migrates onto codex-exec-gateway, the
  registry can be deprecated.
- Not migrating cc-broker. cc-broker keeps running on its existing turn API.
- Not contributing fork patches P1–P4 upstream. They live in the
  agentserver branch of `/root/codex` and are versioned via the existing
  `rust-vX.Y.Z-agentserver.N` tag scheme.
- Not implementing dynamic environment attach/detach during a thread. Phase
  1 fixes the manifest at codex-spawn time, per turn, from the
  workspace's static executor binding.

## High-level architecture

```
Local codex-app (TUI)                                Local codex-exec
codex --remote-app-server                            codex exec-server --connect
  wss://AS/codex-app/...                              wss://AS/codex-exec/exe_xxx
     │                                                       │
     │ app-server JSON-RPC                                   │ exec-server JSON-RPC
     ▼                                                       ▼
┌──────────────────────────────┐         ┌─────────────────────────────────┐
│  codex-app-gateway           │         │  codex-exec-gateway             │
│  (cmd/codex-app-gateway)     │         │  (cmd/codex-exec-gateway)       │
│                              │         │                                 │
│  - Bearer ws auth (user)     │         │  - Bearer ws auth (exec token)  │
│  - app-server v2 RPC handler │         │  - exe_id ↔ inbound ws map      │
│  - turn queue + persistence  │         │  - outbound bridge ws endpoint  │
│  - sessionWorker             │         │    /bridge/{exe_id}             │
│  - spawn `codex exec` per    │         │  - byte-level bidirectional     │
│    turn via codex-agent-sdk- │         │    forward                      │
│    go; sets:                 │         │                                 │
│      CODEX_EXEC_SERVERS_JSON │         │                                 │
│      BROKER_TOKEN            │         │                                 │
│  - SDK ThreadEvent → app-    │         │                                 │
│    server ServerNotification │         │                                 │
│  - inject <environments>     │         │                                 │
│    block in system prompt    │         │                                 │
└──────────────┬───────────────┘         └────────────────┬────────────────┘
               │ spawn codex exec --experimental-json     │
               │   manifest references each exe's bridge  │
               ▼                                          │
   ┌────────────────────────────────────────┐             │
   │ codex subprocess (in app-gateway pod)  │             │
   │  reads manifest → registers N envs     │             │
   │  LLM tools dispatch via env_id         │             │
   │  ws (exec-server) per env →────────────┼─────────────┘
   └────────────────────────────────────────┘
```

**Three protocol surfaces, all native codex:**

| Direction | Protocol | Endpoint |
|---|---|---|
| codex-app → app-gateway | codex app-server v2 JSON-RPC over wss | `/codex-app/*` |
| codex subprocess → exec-gateway | codex exec-server JSON-RPC over ws | `/bridge/{exe_id}` |
| exec-gateway ← codex-exec | codex exec-server JSON-RPC over wss | `/codex-exec/{exe_id}` |

The exec-gateway is a transparent byte-level forwarder between the bridge
endpoint and the inbound exec connection — it does not parse exec-server
RPCs.

## Repository layout

```
agentserver/
├── cmd/
│   ├── codex-app-gateway/main.go          (new)
│   └── codex-exec-gateway/main.go         (new)
├── internal/
│   ├── codexappgateway/                   (new)
│   │   ├── config.go
│   │   ├── server.go                      chi routes + lifecycle
│   │   ├── transport/
│   │   │   ├── ws_listener.go
│   │   │   └── jsonrpc.go                 envelope encode/decode
│   │   ├── protocol/
│   │   │   ├── client_request.go          ClientRequest sum-type (8 in P1)
│   │   │   ├── server_notification.go     ServerNotification (8 in P1)
│   │   │   ├── server_request.go          ServerRequest (4 approvals in P1)
│   │   │   ├── types.go                   Thread, Turn, Item, Usage
│   │   │   └── schema_fixture_test.go     align with codex schema/json/*
│   │   ├── handlers/
│   │   │   ├── initialize.go
│   │   │   ├── thread.go
│   │   │   ├── turn.go
│   │   │   └── approvals.go
│   │   ├── runner/
│   │   │   ├── runner.go                  per-turn spawn via SDK
│   │   │   ├── manifest.go                CODEX_EXEC_SERVERS_JSON writer
│   │   │   └── event_mapper.go            SDK event → ServerNotification
│   │   ├── session_worker.go              (mirrors ccbroker pattern)
│   │   ├── store.go                       Postgres
│   │   ├── migrations/
│   │   ├── workspace/                     S3 + tmp ~/.codex/sessions/<id>.jsonl
│   │   └── exectoken/                     per-turn HMAC capability tokens
│   └── codexexecgateway/                  (new)
│       ├── config.go
│       ├── server.go                      chi routes + lifecycle
│       ├── inbound.go                     /codex-exec/{exe_id} acceptor
│       ├── bridge.go                      /bridge/{exe_id} acceptor + forward
│       ├── registry.go                    in-memory exe_id ↔ ws conn map
│       └── auth.go                        bearer + capability validation
├── Dockerfile.codex-app-gateway           (new)
├── Dockerfile.codex-exec-gateway          (new)
└── deploy/                                (helm values, k8s manifests)
```

Both gateways are independent Go programs in separate Pods.

## Subsystem 1: codex fork patches (P1–P4)

All four patches live in `/root/codex` on the agentserver fork branch. They
are additive: callers that don't set the new env var or new tool field see
behavior identical to upstream codex.

### P1 — `ManifestEnvironmentProvider`

**Files:** `codex-rs/exec-server/src/environment.rs`,
`codex-rs/exec-server/src/environment_provider.rs`,
`codex-rs/exec-server/src/lib.rs`.

Add env var `CODEX_EXEC_SERVERS_JSON` pointing to a JSON file:

```json
{
  "default_environment_id": "exe_alpha",
  "environments": [
    {
      "id": "exe_alpha",
      "url": "ws://codex-exec-gateway:6060/bridge/exe_alpha",
      "auth_token_env": "BROKER_TOKEN",
      "description": "Daisy's MacBook Pro, /home/daisy/projects"
    },
    {
      "id": "exe_beta",
      "url": "ws://codex-exec-gateway:6060/bridge/exe_beta",
      "auth_token_env": "BROKER_TOKEN",
      "description": "EC2 us-east-1, /var/projects/api"
    }
  ]
}
```

`EnvironmentManager::new()` flow:

```
if CODEX_EXEC_SERVERS_JSON is set:
    read & parse manifest file
    for each entry: insert Environment::remote_inner(url, auth_token) into HashMap
    set default_environment_id from manifest (or first entry if absent)
    return EnvironmentManager
else:                                  (existing path, byte-identical)
    fall through to CODEX_EXEC_SERVER_URL single-URL loader
```

If both env vars are set, manifest wins and a warning is logged. If
`auth_token_env` references an unset variable, codex fails fast with a
clear diagnostic (mirrors the `--auth-token-env` validation already in
`codex exec-server --connect`).

If `Environment::remote_inner` does not currently accept a per-env auth
token, this patch extends it to do so (~50 LOC). Confirm during plan.

Tests: parse valid manifest, missing default → first wins, bad URL,
missing auth env, empty environments[] rejection, manifest-vs-single-URL
precedence + warning.

### P2 — Tool runtimes select environment by id

**Files:** `codex-rs/core/src/session/turn_context.rs`,
`codex-rs/core/src/tools/runtimes/unified_exec.rs`,
`codex-rs/core/src/tools/runtimes/apply_patch.rs`,
`codex-rs/core/src/unified_exec/process_manager.rs` (and any other
constructor of `UnifiedExecRequest` / `ApplyPatchRequest`).

Add helper on `TurnContext`:

```rust
impl TurnContext {
    pub(crate) fn select_environment(&self, requested: Option<&str>) -> Option<&TurnEnvironment> {
        match requested {
            Some(id) => self.environments.iter().find(|e| e.environment_id == id),
            None     => self.environments.first(),     // existing primary behavior
        }
    }
}
```

Add field `pub environment_id: Option<String>` to `UnifiedExecRequest` and
`ApplyPatchRequest`. Replace all 4 existing `primary_environment()` call
sites in tool runtimes with `select_environment(req.environment_id.as_deref())`.
Constructors that don't care fill `None` (behavior unchanged).

If `environment_id` is `Some(id)` but no environment with that id exists in
the turn context, return `ToolError::Rejected` with a message naming the
unknown id and listing available ids. The error is reported back to the
LLM via the standard tool-call error path.

Tests: unified_exec routes to second env when env_id given; routes to
primary when not given; unknown env id returns descriptive error;
apply_patch same coverage.

### P3 — LLM tool schema gains `environment_id`

**Files:** the source modules that declare the `shell` and `apply_patch`
tool JSON schemas inside `codex-rs/core/src/tools/`, plus the tool
dispatcher that builds `UnifiedExecRequest` / `ApplyPatchRequest` from JSON
args. Plan task 1 locates the exact files (`grep -rn "name.*shell" codex-rs/core/src/tools/`
plus following the dispatcher chain) before P3 starts, so the patch knows
its target list.

Add optional field to `shell` and `apply_patch` JSON schemas:

```json
{
  "name": "shell",
  "parameters": {
    "type": "object",
    "properties": {
      "command": { "type": "array", "items": { "type": "string" } },
      "workdir": { "type": "string" },
      "environment_id": {
        "type": "string",
        "description": "Optional. Identifier of the execution environment to run this command in. Defaults to the primary environment for the turn. See <environments> in the system prompt for available ids."
      }
    },
    "required": ["command"]
  }
}
```

Dispatcher reads the field and plumbs it into `UnifiedExecRequest.environment_id`.
Same for apply_patch.

Tests: snapshot the generated tool schema includes `environment_id`;
dispatcher correctly threads it through; missing field is treated as
`None`.

### P4 — System prompt `<environments>` block

**Files:** `codex-rs/core/src/session/...` (system prompt assembly path),
`codex-rs/exec-server/src/environment.rs` (add `description: Option<String>`
to `Environment`).

When the turn's `environments` list has at least one entry, render a block
modeled after the existing `<skills_instructions>` / `<plugins_instructions>`
pattern:

```xml
<environments>
You may run shell commands and edit files in any of the following execution
environments. Pick the one whose description matches the user's intent.
If unsure, omit `environment_id` to use the primary environment.

| id          | description                          | default |
| ----------- | ------------------------------------ | ------- |
| exe_alpha   | Daisy's MacBook Pro, /home/daisy     | yes     |
| exe_beta    | EC2 us-east-1, /var/projects/api     | no      |
| exe_gamma   | RPi 4 in lab, /home/pi/sensors       | no      |
</environments>
```

`description` flows manifest → `Environment::description` → block. The
block is appended once at turn start; not regenerated mid-turn.

Tests: block renders for multi-env turn; absent / single-env turns omit
the block; default flag matches `default_environment_id`; description
escaping for pipe / newline characters.

### Patch sizing summary

| Patch | LOC (incl. tests) |
|---|---|
| P1 ManifestEnvironmentProvider | ~250 |
| P2 select_environment + tool runtime | ~250 |
| P3 LLM tool schema | ~150 |
| P4 system prompt | ~80 |
| **Total** | **~730** |

Four independent PRs into the agentserver fork; each runs the existing
codex Rust test suite plus its new tests.

## Subsystem 2: codex-app-gateway

### Responsibilities

1. Accept ws connections from `codex-app` clients; authenticate with bearer
   JWT (issued by agentserver auth service).
2. Speak codex app-server v2 JSON-RPC: `initialize`, `thread/*`, `turn/*`,
   `execCommandApproval`, `applyPatchApproval`.
3. Maintain per-thread turn queue + persistence; recover pending turns on
   restart.
4. For each turn, build the manifest of environments the workspace can use,
   spawn `codex exec` via `codex-agent-sdk-go`, stream SDK events back to
   the connected client as `ServerNotification`s, and append them to the
   per-turn event log for replay.

### Phase 1 RPC surface

**ClientRequest (8):**

| Method | Action |
|---|---|
| `initialize` | Return `InitializeResponse` with capabilities |
| `thread/start` | Insert `codex_threads` row, return new `thread_id` |
| `thread/resume` | Look up by id; download `<thread_id>.jsonl` from S3 to tmp workspace |
| `thread/read` | Return persisted history (replays `codex_turn_events` rows) |
| `thread/list` | DB query, paginated |
| `thread/turns/list` | DB query for one thread |
| `turn/start` | Enqueue `codex_turns` row; return `TurnStartResponse{turn:{id, status:in_progress}}`; sessionWorker runs asynchronously and emits notifications |
| `turn/interrupt` | Cancel in-flight turn (cancel ctx → SDK → SIGTERM codex subprocess) |

**ClientNotification (1):**

| Method | Action |
|---|---|
| `initialized` | Mark connection ready for normal RPC traffic |

**ServerNotification (8):**

| Method | When emitted |
|---|---|
| `thread/started` | First time codex emits its thread.started for a freshly-started thread |
| `thread/status/changed` | Thread status transitions (idle → running → idle, errored, archived) |
| `turn/started` | After codex emits turn.started, before any items |
| `turn/completed` | After codex emits turn.completed (carries usage) |
| `item/started` | For each codex item.started (item types: agent_message, reasoning, command_execution, file_change, etc.) |
| `item/completed` | For each codex item.completed |
| `item/agentMessage/delta` | For incremental text deltas from codex (mapped from codex item streaming or item.updated as appropriate) |
| `error` | Top-level codex errors not tied to a specific turn |

**ServerRequest (4 approvals):**

| Method | Trigger |
|---|---|
| `execCommandApproval` | codex emits an exec approval request item |
| `applyPatchApproval` | codex emits a file-change approval request item |
| `item/commandExecution/requestApproval` | granular per-tool-call approval (newer form) |
| `item/fileChange/requestApproval` | granular per-tool-call approval (newer form) |

The gateway suspends the codex subprocess's tool execution until the TUI
client returns a response on the same JSON-RPC id. Internally the gateway
has held the codex `permission_request` object via the exec-server bridge
flow; the response decision is forwarded back into the codex process.

### Turn lifecycle (mirrors ccbroker)

```
TUI: turn/start { thread_id, input[], cwd?, model? }
  ├─→ gateway:
  │     1. validate, auth: thread belongs to user
  │     2. INSERT codex_turns(turn_id, thread_id, user_message, status='pending', metadata)
  │     3. notify per-thread sessionWorker
  │     4. RESPOND TurnStartResponse{ turn:{ id: turn_id, status:'in_progress' } }
  │
  └─→ sessionWorker (async):
        1. PickNextPending → mark turn 'running'
        2. workspace.Setup: download S3 ~/.codex/sessions/<thread_id>.jsonl → tmp
        3. build manifest from workspace.executors → write CODEX_EXEC_SERVERS_JSON
        4. issue per-turn capability token (BROKER_TOKEN)
        5. codex.New(opts).ResumeThread(thread_id).RunStreamed(ctx, input, ...)
        6. for each SDK event:
             - map to ServerNotification or ServerRequest
             - INSERT codex_turn_events(turn_id, seq, event)
             - push to client conn (if connected)
        7. wait stream.Wait(); upload jsonl back to S3; cleanup tmp + manifest
        8. mark 'done' / 'failed' / 'cancelled' as appropriate
```

Reconnection: TUI calls `thread/read` after re-authenticating; gateway
serves persisted events. If a turn is still running, gateway also resumes
push of new events on the same connection.

### Manifest construction

For each turn, the gateway:

1. Loads the workspace's bound executors from DB (`workspace_executors`
   table — see Data model § below).
2. Filters to executors currently connected to codex-exec-gateway (live
   liveness check via internal HTTP `GET /api/exec-gateway/connected`).
3. For each live executor, emits a manifest entry with:
   - `id`: the executor's `exe_id`
   - `url`: `ws://codex-exec-gateway:6060/bridge/{exe_id}`
   - `auth_token_env`: `"BROKER_TOKEN"` (single env var holds the cap token)
   - `description`: executor's user-supplied label + cwd hint
4. Picks `default_environment_id`:
   - If TUI passed `turn/start.environments`, use the first
   - Else use workspace's `default_executor_id`
   - Else use the first manifest entry
5. Writes manifest to `/tmp/codex-app-gateway/<turn_id>/exec_servers.json`
   (mode 0600), exports path as `CODEX_EXEC_SERVERS_JSON` to the codex
   subprocess.

Cleanup: sessionWorker `defer`s removal of the manifest tmpdir at turn
end (success / failure / cancel).

### Capability token (BROKER_TOKEN)

Per-turn HMAC token signed by gateway shared secret:

```
BROKER_TOKEN = base64( hmac_sha256(secret, turn_id || ":" || exp_unix) || ":" || turn_id || ":" || exp_unix )
```

`exp` = turn start + 1h (loose upper bound on turn duration). Single token
covers all executors in the manifest — exec-gateway extracts `turn_id`
from the token, validates the HMAC, then on `/bridge/{exe_id}` looks up
"is this turn allowed to use exe_xxx" by joining with workspace.executors
in DB.

### Data model

```sql
CREATE TABLE codex_threads (
  thread_id      TEXT PRIMARY KEY,
  workspace_id   TEXT NOT NULL,
  user_id        TEXT NOT NULL,
  title          TEXT,
  status         TEXT NOT NULL,           -- active, archived
  created_at     TIMESTAMPTZ NOT NULL,
  updated_at     TIMESTAMPTZ NOT NULL,
  metadata       JSONB
);

CREATE TABLE codex_turns (
  turn_id          TEXT PRIMARY KEY,
  thread_id        TEXT NOT NULL REFERENCES codex_threads(thread_id),
  user_input       JSONB NOT NULL,        -- the prompt items[]
  turn_options     JSONB,                 -- model, sandbox, env selections, ...
  status           TEXT NOT NULL,         -- pending, running, done, failed, cancelled
  error_message    TEXT,
  enqueued_at      TIMESTAMPTZ NOT NULL,
  started_at       TIMESTAMPTZ,
  finished_at      TIMESTAMPTZ
);
CREATE INDEX ON codex_turns(thread_id, enqueued_at);
CREATE INDEX ON codex_turns(status) WHERE status IN ('pending','running');

CREATE TABLE codex_turn_events (
  turn_id   TEXT NOT NULL REFERENCES codex_turns(turn_id),
  seq_num   BIGSERIAL,
  payload   JSONB NOT NULL,               -- a ServerNotification or ServerRequest
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (turn_id, seq_num)
);

CREATE TABLE workspace_executors (
  workspace_id        TEXT NOT NULL,
  exe_id              TEXT NOT NULL,
  is_default          BOOLEAN NOT NULL DEFAULT false,
  created_at          TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, exe_id)
);
```

`workspace_executors` is the static binding referenced in § Manifest
construction. `executors` table itself (executor identity + metadata) is
new and lives in codex-exec-gateway:

```sql
CREATE TABLE executors (
  exe_id          TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL,
  display_name    TEXT,
  description     TEXT,                   -- shown in <environments> block
  default_cwd     TEXT,
  registration_token_hash TEXT NOT NULL,  -- bcrypt of long-lived token
  registered_at   TIMESTAMPTZ NOT NULL,
  last_seen_at    TIMESTAMPTZ
);
```

### Workspace persistence

- Tmp dir per turn: `/tmp/codex-app-gateway/<turn_id>/{codex-home,project-dir}`
- Before turn: download S3 `s3://codex-sessions/<workspace_id>/<thread_id>.jsonl`
  → `<codex-home>/sessions/<thread_id>.jsonl`
- Spawn codex with `--cd <project-dir>`, `CODEX_HOME=<codex-home>`,
  `CODEX_EXEC_SERVERS_JSON=<manifest path>`, `BROKER_TOKEN=<cap token>`,
  `CODEX_API_KEY=<workspace token>`
- After turn (success or fail): upload `<codex-home>/sessions/<thread_id>.jsonl`
  back to S3; remove tmp dir
- The S3 store wrapper is factored out to `internal/storage/agentworkspace/`
  so cc-broker (and future codex-broker callers) share the same code.

### Spawning codex (driver)

Uses `codex-agent-sdk-go` exactly as documented:

```go
codexClient := codex.New(codex.CodexOptions{
    APIKey: workspaceToken,
    BaseURL: cfg.LLMProxyURL,
    Env: map[string]string{
        "CODEX_HOME":                tmpHome,
        "CODEX_EXEC_SERVERS_JSON":   manifestPath,
        "BROKER_TOKEN":              capToken,
    },
})
thread := codexClient.ResumeThread(threadID, codex.ThreadOptions{
    SandboxMode:      codex.SandboxWorkspaceWrite,
    WorkingDirectory: projectDir,
    SkipGitRepoCheck: true,
    Model:            turnReq.Model,
})
stream, err := thread.RunStreamed(turnCtx, inputItems, codex.TurnOptions{
    OutputSchema: turnReq.OutputSchema,
})
for evt := range stream.Events() {
    notif := mapEventToServerNotification(evt, turnID, threadID)
    insertEvent(turnID, notif)
    pushToClient(connID, notif)
}
return stream.Wait()
```

`mapEventToServerNotification` is a pure function from the SDK's
`codex.ThreadEvent` union to the gateway's `protocol.ServerNotification`
union. Tested with table-driven cases per event type.

## Subsystem 3: codex-exec-gateway

### Responsibilities

1. Accept ws connections at `/codex-exec/{exe_id}` from local
   `codex exec-server --connect` processes; authenticate with persistent
   executor tunnel token.
2. Maintain in-memory map `exe_id → inbound ws connection` (one connection
   per exe_id; new connection evicts old).
3. Accept ws connections at `/bridge/{exe_id}` from the spawned codex
   subprocess inside codex-app-gateway; authenticate with per-turn
   capability token (`BROKER_TOKEN`).
4. After both endpoints' connection-time auth checks pass, forward ws
   frames bidirectionally between the inbound and bridge connections at
   the frame level. The gateway does not parse exec-server JSON-RPC inside
   frames; it is a transparent forwarder. (Auth happens once per
   connection, not per frame.)
5. Expose `GET /api/exec-gateway/connected` (internal, mTLS / shared-secret
   protected) for codex-app-gateway to query liveness when building manifests.

### Connection lifecycle

```
codex-exec (laptop) ──connect──> /codex-exec/{exe_id}
   gateway:
     - validate exe_id + tunnel_token against `executors` table
     - upsert executors.last_seen_at, store ws conn in registry[exe_id]
     - on disconnect: remove from registry

codex subprocess (gateway pod) ──connect──> /bridge/{exe_id}
   gateway:
     - extract turn_id from BROKER_TOKEN, verify HMAC
     - check workspace_executors: is this exe_id allowed for this turn's workspace?
     - look up registry[exe_id]; if absent, reject with 503
     - pair the two conns; spawn copy goroutines:
         go pump(bridge → inbound)
         go pump(inbound → bridge)
     - on either side close: close both, remove pair
```

### Auth model

| Connection | Credential | Validator |
|---|---|---|
| codex-exec → /codex-exec/{exe_id} | persistent tunnel token (executor registration) | bcrypt-compare against `executors.registration_token_hash` |
| codex subprocess → /bridge/{exe_id} | per-turn capability token (BROKER_TOKEN) | HMAC verify with shared secret |

Tunnel tokens are issued at executor registration time:

```
POST /api/codex-exec/register
Body: { display_name, description, default_cwd }
Auth: user JWT
→ 201 { exe_id, registration_token }
```

The user copies `registration_token` into their local environment and
launches:

```
codex exec-server --connect wss://AS/codex-exec/exe_xxx \
                  --auth-token-env CODEX_EXEC_TOKEN
CODEX_EXEC_TOKEN=<token> codex exec-server --connect ...
```

### Workspace-executor binding

Separate small admin endpoint set:

```
POST /api/codex-exec/workspaces/{wid}/executors
   Body: { exe_id, is_default }
   Auth: workspace owner JWT
   Effect: INSERT INTO workspace_executors

DELETE /api/codex-exec/workspaces/{wid}/executors/{exe_id}
GET    /api/codex-exec/workspaces/{wid}/executors
```

These can be exposed on either gateway; for separation of concerns, place
them on codex-exec-gateway (it owns the `executors` and `workspace_executors`
tables).

### Internal API (codex-app-gateway → codex-exec-gateway)

```
GET /api/exec-gateway/connected?workspace_id=ws_xxx
Auth: shared-secret bearer
→ 200 [
    {"exe_id":"exe_alpha", "description":"...", "default_cwd":"...", "is_default":true, "last_seen_at":"..."},
    ...
  ]

POST /api/exec-gateway/revoke-turn
Body: { "turn_id": "trn_xxx" }
Auth: shared-secret bearer
→ 204
```

`/connected` returns the intersection of currently-connected executors and
the workspace's binding, used to compose the per-turn manifest.

`/revoke-turn` is called by codex-app-gateway whenever a turn reaches a
terminal state, so exec-gateway can drop the turn's BROKER_TOKEN from its
allow set even while it's still inside the token's `exp` window. See
"Capability-token replay" in § Open risks.

## Auth model (cross-cutting)

| Hop | Credential | Issuer | Lifetime |
|---|---|---|---|
| codex-app TUI → codex-app-gateway | per-user JWT bearer | agentserver auth service | per session, refreshable |
| spawned codex → codex-exec-gateway/bridge | per-turn HMAC capability token (BROKER_TOKEN) | codex-app-gateway | turn duration + 1h slack |
| codex-exec → codex-exec-gateway/codex-exec/{id} | persistent registration token | codex-exec-gateway at registration | until rotated |
| codex subprocess → llmproxy | workspace token | wstoken service (existing) | short-lived |
| codex-app-gateway ↔ codex-exec-gateway internal API | shared bearer secret | env config | static |

Shared secrets live in K8s secrets, mounted as files in each Pod. The
broker capability HMAC secret is rotated quarterly; old tokens stay valid
until their `exp`.

## Deployment

| Component | Image | Replicas | Notes |
|---|---|---|---|
| codex-app-gateway | Dockerfile.codex-app-gateway | 2+ HA | needs `codex` CLI binary on PATH |
| codex-exec-gateway | Dockerfile.codex-exec-gateway | 2+ HA, sticky ws via session affinity | small image, no codex binary needed |

Each gateway owns its own Postgres tables; the cluster is shared with
cc-broker / agentserver but tables are partitioned by ownership:

| Gateway | Owns tables |
|---|---|
| codex-app-gateway | `codex_threads`, `codex_turns`, `codex_turn_events` |
| codex-exec-gateway | `executors`, `workspace_executors` |

codex-app-gateway never reads `executors` / `workspace_executors`
directly; it queries codex-exec-gateway's `GET /api/exec-gateway/connected`
HTTP endpoint to compose manifests, keeping the table-ownership boundary
clean and enabling future split into separate Postgres instances if
needed.

Service discovery uses K8s Service DNS. codex-app-gateway has
`CODEX_EXEC_GATEWAY_URL=ws://codex-exec-gateway:6060` in its env config.

## Phase 1 vs deferred

**Phase 1 (this spec):**

- 8 ClientRequest + 1 ClientNotification + 8 ServerNotification + 4 ServerRequest in codex-app-gateway
- codex fork patches P1–P4
- codex-exec-gateway with inbound + bridge endpoints
- Per-workspace static executor binding
- Capability-token auth for bridge, with **turn-completion revocation push**
  (codex-app-gateway notifies codex-exec-gateway when a turn ends so any
  bridge connection attempt with the now-stale token is rejected, even
  inside the token's `exp` window)
- Tmp workspace + S3 sessions persistence
- Executor self-registration + workspace-binding admin endpoints

**Phase 2 candidates (out of scope, listed for traceability):**

- Full app-server v2 RPC surface: `account/*`, `config/*`, `fs/*`,
  `mcpServer/*`, `skills/*`, `plugin/*`, `marketplace/*`,
  `apps/*`, `experimentalFeature/*`, `feedback/*`, `command/exec` (vs
  codex-internal exec)
- `command/exec/outputDelta` true streaming through bridge
- Dynamic environment attach/detach mid-thread
- gateway-injected MCP server (broker-side `ask_user`, `im_send`)
- Realtime conversation (`thread/realtime/*`)
- External-agent session import
- Multi-region exec-gateway with executor home routing
- cc-broker migration to codex-app-gateway

## Testing strategy

| Layer | Approach |
|---|---|
| codex fork P1–P4 | rust unit + integration; multi-env spawn that runs `shell` against two distinct fake environments |
| codex-app-gateway protocol layer | Go table-driven RPC envelope encode/decode; validated against codex `schema/json/*.json` fixtures (snapshot-style) |
| codex-app-gateway handlers | mock store + mock SDK driver; one test file per handler per RPC path (happy + error) |
| codex-app-gateway runner | reuses `codex-agent-sdk-go`'s existing `testdata/fake_codex/*.sh` scripts + scripted ThreadEvent → ServerNotification mapper assertions |
| codex-exec-gateway forwarding | bring up two fake ws endpoints, paired via the bridge, assert byte-for-byte fidelity in both directions, including frame-boundary preservation and close propagation |
| End-to-end | docker-compose: codex-app-gateway + codex-exec-gateway + Postgres + a fake `codex-app` ws client + a fake `codex-exec` ws server; one scripted turn that proves "TUI sends prompt → codex spawns → tool call routed to exec → output flows back" |

## Open risks

1. **`Environment::remote_inner` per-env auth**: P1 assumes it accepts a
   per-env auth token. If today only the global `CODEX_EXEC_SERVER_URL` /
   single-token path supports it, P1 grows by ~50 LOC to plumb a
   `Option<auth_token: String>` through `Environment` constructors. Verify
   in plan task 1.
2. **TUI `--remote-app-server` flag**: codex's `RemoteAppServerClient` is
   in-tree, but the TUI's invocation path may need a dedicated CLI flag we
   haven't audited. If absent, that's a small fork patch (~50 LOC) to add a
   `codex --remote-app-server <wss-url>` option to the TUI subcommand.
   Verify in plan task 2.
3. **Backpressure across the bridge**: a slow executor stalls codex's
   exec-server protocol. The bridge is byte-level; backpressure propagates
   naturally through ws flow control, but very long-running streams may hit
   ws idle timeouts. Need explicit ping/pong configuration on both sides.
4. **Capability-token replay**: BROKER_TOKEN is good for `exp` window
   (turn start + 1h). Without revocation, a leaked token from a finished
   turn could connect to bridge during slack. Phase 1 mitigation (listed
   above): on turn finish (success / fail / cancel), codex-app-gateway
   POSTs `turn_id` to codex-exec-gateway's `/api/exec-gateway/revoke-turn`
   endpoint; exec-gateway adds it to an in-memory revoked set (sized cap
   ~10k, periodically pruned of entries past their original `exp`); future
   bridge connects whose token decodes to that turn_id are rejected with
   401.
5. **Manifest staleness within a turn**: if an executor disconnects
   mid-turn, its bridge URL becomes a dead endpoint. codex's tool call
   will return an error to the LLM, which is acceptable behavior; no
   automatic retry / reroute in phase 1.

## Acceptance

A user can:

1. Start `codex-exec` on their laptop → gateway shows the executor as
   connected.
2. Bind that executor to their workspace via the admin endpoint.
3. Start `codex-app` on a different laptop pointed at agentserver →
   gateway authenticates them and lists their threads.
4. Submit a prompt that requires a shell command. The codex spawned in
   the gateway pod sees one environment in the manifest, picks it,
   dispatches the command via bridge → exec-gateway → the user's laptop.
   Output flows back through the chain into the TUI.
5. With two executors bound to the workspace, system prompt's
   `<environments>` block lists both. The LLM picks one per `shell` call
   via `environment_id`.
6. Disconnect the TUI mid-turn → reconnect → `thread/read` replays all
   events; if turn was still running, new events resume streaming.

A working end-to-end harness in docker-compose is the acceptance gate
before declaring phase 1 complete.
