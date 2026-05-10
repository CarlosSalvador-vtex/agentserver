# codex-app-gateway + codex-exec-gateway — MCP-Path Rewrite

> **🔧 PARTIALLY REFINED 2026-05-10** by
> [`2026-05-10-codex-app-gateway-subprocess.md`](2026-05-10-codex-app-gateway-subprocess.md).
> That refinement replaces only **Subsystem 2** (codex-app-gateway runtime)
> with a thin auth-proxy + per-thread `codex app-server` subprocess
> design. It cuts ~1100 LOC of Go that this spec planned to write.
> Subsystems 1 (no fork patches), 3 (codex-exec-gateway), and 4 (env-mcp)
> are **unchanged** and still source-of-truth here. Read the refinement
> spec first if you're planning the codex-app-gateway runtime; read this
> spec for everything else.

**Status:** draft (central technical claim PoC-validated 2026-05-10, see § PoC log)
**Date:** 2026-05-10
**Owner:** agentserver / codex integration
**Supersedes:** [`2026-05-05-codex-app-gateway-and-exec-gateway-design.md`](2026-05-05-codex-app-gateway-and-exec-gateway-design.md)
   (the four 2026-05-05 plan files derived from that spec are obsolete in
   their current form; they are kept on disk for reference until the new
   plan set lands.)

## What changed vs the 2026-05-05 spec

The original spec required a four-patch fork of codex (P1–P4, ~730 LOC)
to make the codex CLI multi-environment-aware: a JSON manifest loader,
a `select_environment` helper, an `environment_id` field on every
`shell` / `apply_patch` JSON-schema, and a `<environments>` block in the
system prompt. Two facts make those patches unnecessary:

1. **Upstream codex already exposes the wire-level connectors we need.**
   - TUI: `codex --remote <ws-url> --remote-auth-token-env <ENV>` is a
     stable CLI flag wired straight into `RemoteAppServerClient`
     (`codex-rs/cli/src/main.rs:682-690`).
   - Executor side: `codex exec-server --remote <ws-url> --executor-id <ID>`
     dials out and registers as a remote executor
     (`codex-rs/cli/src/main.rs:464-470` plus `RemoteExecutorConfig`).
   - The spawned codex subprocess that *runs* the LLM loop is a normal
     MCP host: `codex-rs/codex-mcp/` provides a fully-implemented MCP
     client over stdio + StreamableHttp transports, configured via
     `[mcp_servers]` in `~/.codex/config.toml`.
2. **The "MCP-server-per-environment" pattern is already validated.**
   The (otherwise abandoned) cc-app-gateway design used exactly this
   trick to dispatch shell calls to multiple environments without
   patching the harness binary; PoC F (2026-05-05) confirmed it works.
   Carrying that pattern over to codex is *easier* than carrying it to
   Claude Code, because codex's MCP client is first-class rather than
   bolt-on.

Net effect:

| Fork patch (2026-05-05) | Status under this rewrite |
|---|---|
| **P1** ManifestEnvironmentProvider (~250 LOC) | **Deleted.** `[mcp_servers]` config replaces it. |
| **P2** `select_environment` + 4 tool runtime sites (~250 LOC) | **Deleted.** Each environment is its own MCP server; the LLM picks via tool name (`mcp__exe_alpha__shell` vs `mcp__exe_beta__shell`). |
| **P3** `shell` / `apply_patch` schema gain `environment_id` (~150 LOC) | **Deleted.** The MCP-tool name *is* the environment id; no extra schema field. |
| **P4** System prompt `<environments>` block (~80 LOC) | **Deleted.** Each MCP tool's `description` carries its environment context naturally. Optional turn-time prompt nudge stays in the gateway, not the codex source. |

**Codex fork ships zero new patches** for this work. agentx-release
cycle decouples completely from agentserver gateway iterations.

## Architecture

```
Local codex-app (TUI)                            Local codex-exec
codex --remote wss://AS/codex-app/...            codex exec-server --remote wss://AS/codex-exec/exe_xxx --executor-id exe_xxx
       │                                                  │
       │ codex app-server v2 JSON-RPC over wss            │ codex exec-server JSON-RPC over wss
       ▼                                                  ▼
┌───────────────────────────────┐         ┌────────────────────────────────┐
│  codex-app-gateway            │         │  codex-exec-gateway            │
│  (Go, cmd/codex-app-gateway)  │         │  (Go, cmd/codex-exec-gateway)  │
│                               │         │                                │
│  - app-server v2 RPC handler  │         │  - exe_id ↔ inbound ws map     │
│  - turn queue + persistence   │         │  - bridge endpoint forwarding  │
│  - workspace setup (S3 + tmp) │         │    /bridge/{exe_id}            │
│  - sessionWorker per thread   │         │  - cap-token verify + revoke   │
│                               │         │                                │
│  - per-turn:                  │         │                                │
│    1. mint cap token          │         │                                │
│    2. write [mcp_servers]     │         │                                │
│       config.toml fragment    │         │                                │
│    3. spawn `codex exec` with │         │                                │
│       CODEX_HOME pointing at  │         │                                │
│       the per-turn dir; this  │         │                                │
│       enables ONLY env-mcp    │         │                                │
│       servers                 │         │                                │
│    4. spawn N stdio subproc-  │         │                                │
│       esses, one per bound    │         │                                │
│       executor: `codex-app-   │         │                                │
│       gateway env-mcp         │         │                                │
│       --exe-id exe_xxx        │         │                                │
│       --bridge-url ws://...   │         │                                │
│       --token-env ...`        │         │                                │
│    5. SDK ThreadEvent → app-  │         │                                │
│       server ServerNotification│        │                                │
└──────────────┬────────────────┘         └────────────────┬───────────────┘
               │                                           │
               │ spawns codex exec --experimental-json     │
               │   + stdio MCP children (one per env)      │
               ▼                                           │
   ┌────────────────────────────────────┐                  │
   │ codex subprocess (in app-gateway   │                  │
   │ pod):                              │                  │
   │  - reads ~/.codex/config.toml      │                  │
   │  - [features].shell_tool = false   │                  │
   │  - [features].unified_exec = false │                  │
   │  - [features].apply_patch_freeform │                  │
   │       = false                      │                  │
   │  - [mcp_servers.exe_alpha] stdio   │                  │
   │  - [mcp_servers.exe_beta]  stdio   │                  │
   │  LLM tool calls dispatch to MCP    │                  │
   │  children, which forward over ws   │                  │
   │  bridges to exec-gateway:          │                  │
   │  ws (exec-server JSON-RPC) per env─┼──────────────────┘
   └────────────────────────────────────┘
```

**Three protocol surfaces, all native codex-or-MCP:**

| Direction | Protocol | Endpoint |
|---|---|---|
| codex-app TUI → codex-app-gateway | codex app-server v2 JSON-RPC over wss | `/codex-app/*` |
| env-mcp child → codex-exec-gateway | codex exec-server JSON-RPC over ws (wrapped in MCP tool dispatch) | `/bridge/{exe_id}` |
| codex-exec → codex-exec-gateway | codex exec-server JSON-RPC over wss | `/codex-exec/{exe_id}` |
| spawned codex ↔ env-mcp child | MCP over stdio (codex's native client) | stdio pipes |

The exec-gateway remains a transparent ws-frame-level forwarder between
the bridge endpoint and the inbound exec connection. The env-mcp child
is what speaks "exec-server JSON-RPC" on the gateway's `/bridge` side;
on the codex-subprocess side it speaks plain MCP. Auth is verified once
at each ws connect and once at each MCP child startup; forwarding is
unconditional thereafter.

## Why

Same as the 2026-05-05 spec: local codex TUI ↔ remote harness, local
codex as executor, multi-executor per turn. The Why section of the
prior spec applies verbatim. Only the *how* changes.

## Subsystem map

| # | Component | Owner | Status under this spec |
|---|---|---|---|
| 1 | codex fork patches | `/root/codex` | **Deleted from scope.** Zero fork patches required for phase 1. |
| 2 | codex-app-gateway | `cmd/codex-app-gateway`, `internal/codexappgateway/` | **Kept** with a smaller manifest layer (writes `mcp_servers` config + spawns env-mcp children, instead of a JSON-manifest file). |
| 3 | codex-exec-gateway | `cmd/codex-exec-gateway`, `internal/codexexecgateway/` | **Kept**, essentially unchanged from the 2026-05-05 plan. |
| 4 | env-mcp child binary | new subcommand of codex-app-gateway: `codex-app-gateway env-mcp ...` | **New.** ~300 LOC including tests. Implements an MCP server (stdio transport) that translates MCP tool calls → exec-server JSON-RPC frames over a single ws connection to `/bridge/{exe_id}`. |

Subsystems 2 and 3 still get their own pods; 4 is an additional invocation
mode of the codex-app-gateway binary, not a separate image.

## Subsystem 1 — codex fork: NONE

No P1, no P2, no P3, no P4. The agentx-release tag scheme keeps tracking
upstream codex versions only.

**One small caveat we accept and live with:** if upstream renames
`Feature::ShellTool` / `Feature::UnifiedExec` / `Feature::ApplyPatchFreeform`,
the spawned codex's config.toml fragment needs the new keys. Detection
strategy: the e2e test inspects the spawned codex's tool list at startup
and asserts shell/apply_patch are absent; any upstream rename surfaces
as a CI failure pointing straight at the broken key.

## Subsystem 2 — codex-app-gateway

Same scope as 2026-05-05 spec § Subsystem 2 with these deltas:

### Deleted from scope
- `runner/manifest.go` (the `CODEX_EXEC_SERVERS_JSON` writer).
- All references to `CODEX_EXEC_SERVERS_JSON` and `CODEX_EXEC_GATEWAY_TOKEN`
  as codex-binary inputs.
- The `<environments>` system-prompt-block expectations (we no longer
  expect codex to render it).

### Replaced with
- `runner/mcpconfig.go`: per-turn writer of a `~/.codex/config.toml`
  fragment to the per-turn `CODEX_HOME` tmpdir. Fragment shape:

  ```toml
  # disable codex's builtin local execution paths so the LLM
  # can only reach executors via the env-mcp children
  [features]
  shell_tool = false
  unified_exec = false
  apply_patch_freeform = false

  [mcp_servers.exe_alpha]
  command = "/usr/local/bin/codex-app-gateway"
  args    = ["env-mcp",
             "--exe-id",      "exe_alpha",
             "--bridge-url",  "ws://codex-exec-gateway:6060/bridge/exe_alpha",
             "--token-env",   "CXG_BRIDGE_TOKEN_EXE_ALPHA",
             "--turn-id",     "trn_xxx"]
  env     = { CXG_BRIDGE_TOKEN_EXE_ALPHA = "<minted cap token>" }

  [mcp_servers.exe_beta]
  command = "/usr/local/bin/codex-app-gateway"
  args    = ["env-mcp",
             "--exe-id",      "exe_beta",
             "--bridge-url",  "ws://codex-exec-gateway:6060/bridge/exe_beta",
             "--token-env",   "CXG_BRIDGE_TOKEN_EXE_BETA",
             "--turn-id",     "trn_xxx"]
  env     = { CXG_BRIDGE_TOKEN_EXE_BETA = "<minted cap token>" }
  ```

  Each MCP server gets its own env var so the cap tokens never appear in
  `/proc/<pid>/cmdline`.

- `runner/runner.go`: spawn codex with `CODEX_HOME` pointing at the
  per-turn tmpdir whose `config.toml` is the fragment above. No new env
  vars on the codex process beyond `CODEX_HOME`, the existing
  `CODEX_API_KEY`, and existing per-turn `--cd <project-dir>`.

- The 17-RPC surface (8 ClientRequest + 1 ClientNotification + 8
  ServerNotification) is **unchanged** from the 2026-05-05 spec. Same
  approval-deferred posture: phase 1 spawns codex with
  `ApprovalPolicy=never`.

### Why per-MCP-server (one per executor) over per-tool (one server, multiple tools-with-env-id)

Two reasons:

1. **No fork patch.** Per-tool with env-id was P3's whole purpose. Per-server
   needs no codex changes.
2. **Cap-token blast radius.** Each MCP child holds the cap token for
   exactly one executor. A bug in one MCP child (e.g. it leaks its token
   in an error message) is contained to that one bridge endpoint, not
   every executor in the workspace.

The cost is N MCP subprocesses per turn instead of 1 — at typical N≤3
this is acceptable. If a workspace ever binds dozens of executors,
collapsing to one MCP server with `environment_id`-style multiplexing
becomes worth revisiting (still without fork patches, since codex's MCP
tools naturally support arbitrary parameters).

## Subsystem 3 — codex-exec-gateway

**Unchanged from the 2026-05-05 spec.** Same /codex-exec/{id} inbound
endpoint, same /bridge/{id} forwarding endpoint, same cap-token verify,
same `executors` and `workspace_executors` tables, same internal API
(`/api/exec-gateway/connected`, `/api/exec-gateway/revoke-turn`).

The only consumer-side change is who connects to /bridge: under the old
spec it was the spawned codex itself, under this spec it is the env-mcp
child. Wire-protocol and auth are identical, so the gateway code is
identical.

## Subsystem 4 — env-mcp (new)

A new subcommand of the codex-app-gateway binary:

```
codex-app-gateway env-mcp \
    --exe-id      exe_alpha \
    --bridge-url  ws://codex-exec-gateway:6060/bridge/exe_alpha \
    --token-env   CXG_BRIDGE_TOKEN_EXE_ALPHA \
    --turn-id     trn_xxx
```

Lifecycle:

```
1. Read CXG_BRIDGE_TOKEN_EXE_ALPHA from env.
2. Dial bridge-url with `Authorization: Bearer <token>`. Fail fast on
   401/403/503 — these become MCP `initialize` failures the spawned
   codex sees and reports to the LLM.
3. Begin two pumps:
     a. STDIN  → MCP request loop:
        - Decode incoming JSON-RPC MCP request from spawned codex.
        - For tool calls (`tools/call`), translate the call payload into
          an exec-server JSON-RPC frame and send over the ws bridge.
        - Other MCP methods (`initialize`, `tools/list`, `prompts/list`,
          `resources/list`) are answered locally without hitting the
          bridge.
     b. WS    → MCP notification loop:
        - Decode incoming exec-server JSON-RPC frame from the bridge.
        - Translate into an MCP `tools/call` *response* (or error) and
          write to STDOUT.
4. On either side close, propagate close to the other and exit non-zero
   so the spawned codex's MCP layer surfaces the failure to the LLM.
```

### MCP tools exposed

For phase 1, the env-mcp child exposes exactly two tools:

| MCP tool name | Translates to exec-server | Description shown to LLM |
|---|---|---|
| `shell` | `unified_exec` request (or `shell_command` per executor capability) | "Run a shell command on `<executor description>`. Working directory defaults to the executor's bound cwd unless overridden." |
| `apply_patch` | `apply_patch` request | "Apply a patch on `<executor description>`. Use this for any source-file edit." |

In the spawned codex, these appear as namespaced tool names per codex's
MCP convention: `mcp__exe_alpha__shell`, `mcp__exe_alpha__apply_patch`,
`mcp__exe_beta__shell`, `mcp__exe_beta__apply_patch`. The namespacing is what
makes the LLM "pick" an environment — there is no separate `environment_id`
parameter and no `<environments>` system block.

The `<executor description>` interpolated into the description text
flows from `executors.description` (set at registration time) → query
to `/api/exec-gateway/connected` → ${CXG_BRIDGE_DESCRIPTION_EXE_ALPHA}
env var → MCP tool description string. This keeps env-mcp stateless w.r.t.
postgres.

### Why one shell tool, not "exec_command" / "read_file" / "write_file" / "list_dir"

Codex's `unified_exec` is already the canonical "do anything in this
sandbox" primitive — it covers shell, file read, file write, ls, etc.
Mirroring its single-tool design keeps env-mcp's surface tiny and lets
the LLM apply the same shell-tool habits it already has. apply_patch is
the only carve-out, because codex models train on it explicitly.

### Implementation skeleton

```
internal/codexappgateway/envmcp/
├── envmcp.go        # Run(args) entry point: dial bridge, start pumps
├── mcp_server.go    # JSON-RPC stdio server: initialize, tools/list, tools/call
├── translator.go    # MCP tools/call ↔ exec-server JSON-RPC frame
├── bridge_client.go # nhooyr.io/websocket dialer with Bearer auth
├── *_test.go        # one per source file
└── integration_test.go  # paired with a fake exec-gateway
```

Estimated LOC including tests: ~300.

## Repository layout (delta vs 2026-05-05 spec)

```
agentserver/
├── cmd/
│   ├── codex-app-gateway/main.go          (one binary, multiple subcommands: serve, env-mcp)
│   └── codex-exec-gateway/main.go         (unchanged from 2026-05-05)
├── internal/
│   ├── codexappgateway/
│   │   ├── ... (everything from the 2026-05-05 plan minus runner/manifest.go)
│   │   ├── runner/mcpconfig.go            (replaces runner/manifest.go)
│   │   └── envmcp/                        (new, see § Subsystem 4)
│   └── codexexecgateway/                  (unchanged from 2026-05-05)
├── Dockerfile.codex-app-gateway           (must include codex CLI binary on PATH; same as 2026-05-05)
└── Dockerfile.codex-exec-gateway          (unchanged)
```

## Auth model (delta only)

Hop-by-hop table is unchanged with one renaming:

| Old name | New name |
|---|---|
| `CODEX_EXEC_GATEWAY_TOKEN` (single env var with token covering all executors) | One token *per executor* per turn, each in its own env var (`CXG_BRIDGE_TOKEN_EXE_ALPHA`, …). Same HMAC scheme; payload's `exe_ids` is now always a single-id slice. |

Per-token-per-executor was implicitly possible in the old design; making
it explicit shrinks each MCP child's blast radius (see § Subsystem 2,
"Why per-MCP-server").

The HMAC secret rotation, cap-token TTL (turn start + 1h), and
turn-completion revocation push to /api/exec-gateway/revoke-turn are
unchanged.

## Non-goals (delta)

In addition to all 2026-05-05 non-goals (still in force), this rewrite
explicitly drops:

- All four codex fork patches (P1–P4) from phase 1 scope.
- The `<environments>` system-prompt expectation in any form.
- Per-tool `environment_id` parameter in any MCP tool surface — the env
  is identified by tool *name*.

## Phase 1 acceptance (replaces 2026-05-05 acceptance)

A user can:

1. Start `codex exec-server --remote wss://AS/codex-exec/exe_xxx --executor-id exe_xxx`
   on their laptop → codex-exec-gateway shows the executor as connected.
2. Bind that executor to their workspace via the admin endpoint.
3. Start `codex --remote wss://AS/codex-app/...` on a different laptop →
   codex-app-gateway authenticates the user and lists their threads.
4. Submit a prompt that requires a shell command. The codex spawned in
   the gateway pod sees ONLY the `mcp__exe_alpha__shell` and
   `mcp__exe_alpha__apply_patch` MCP tools (plus codex's non-shell builtins
   like web_search if enabled). It picks `mcp__exe_alpha__shell`, the env-mcp
   child translates it to an `unified_exec` exec-server frame, the
   exec-gateway forwards it to the user's laptop, output flows back
   through the chain into the TUI.
5. With two executors bound, the spawned codex sees four MCP tools:
   `mcp__exe_alpha__shell`, `mcp__exe_alpha__apply_patch`, `mcp__exe_beta__shell`,
   `mcp__exe_beta__apply_patch`. The LLM picks per call by tool name. No
   `<environments>` system block is rendered (and the test asserts that).
6. Disconnect the TUI mid-turn → reconnect → `thread/read` replays all
   events; if a turn was still running, new events resume streaming.
7. e2e harness in docker-compose proves the full chain end-to-end.

## PoC log (2026-05-10) — both gating claims validated

Two PoCs were run on `codex-cli 0.128.0` (upstream binary, not the
agentserver fork). Artifacts on disk under `/tmp/codex-poc-mcp/` —
keep until plan rewrite lands.

### PoC #1: spawned codex loads our MCP server with shell disabled

```bash
mkdir -p /tmp/codex-poc-mcp
# config.toml: features.shell_tool=false, features.unified_exec=false,
#              [mcp_servers.local_test] command=stub_mcp.py
CODEX_HOME=/tmp/codex-poc-mcp codex features list \
  | grep -E 'shell_tool|unified_exec|apply_patch_freeform'
# →
# apply_patch_freeform   under development  false
# shell_tool             stable             false   <- per-CODEX_HOME override worked
# unified_exec           stable             false   <- per-CODEX_HOME override worked

CODEX_HOME=/tmp/codex-poc-mcp codex exec --skip-git-repo-check --cd /tmp \
  'Use the local_test__shell tool to run `ls /tmp/codex-poc-mcp` ...'
# stub_mcp.py log shows codex sent us:
#   1. initialize  (protocolVersion 2025-06-18, clientInfo codex-mcp-client@0.128.0)
#   2. notifications/initialized
#   3. tools/list  → received our shell tool definition back
# (LLM call later 401'd because the user's API key expired — irrelevant to
#  this PoC, which is about codex's tool surface assembly, not LLM routing.)
```

What this proves:

- ✅ A per-`CODEX_HOME` `[features]` table can disable `shell_tool` /
  `unified_exec` on the upstream binary. No fork patch needed.
- ✅ A per-`CODEX_HOME` `[mcp_servers.X]` entry causes upstream codex to
  spawn the configured stdio child and complete the standard MCP
  initialize → tools/list handshake.
- ✅ codex normalizes MCP tool names as `mcp__<server>__<tool>` (verified
  via `codex-rs/codex-mcp/src/tools.rs:137` and the `connection_manager_tests`
  assertions). So `[mcp_servers.exe_alpha]` exposing `shell` becomes
  `mcp__exe_alpha__shell` to the LLM — the namespacing is automatic.

What this does NOT prove (deferred to e2e harness):

- That codex without a builtin shell still produces a coherent
  `tools/list` to the LLM (no implicit "you must have a shell" prompt
  injection by codex). The PoC got far enough to send the request to
  the LLM, suggesting yes; the e2e test will verify by intercepting the
  outbound LLM payload.
- That the LLM consistently picks `mcp__exe_alpha__shell` rather than
  inventing a `shell` call. This is an LLM-behavior assertion that the
  e2e test must check.

### PoC #2: env-mcp translator (MCP ↔ exec-server JSON-RPC)

Spun up `codex exec-server --listen ws://127.0.0.1:7878` (upstream
binary), built a minimal Python env-mcp child, and drove it via stdin
exactly as codex's MCP host would:

```bash
# 1. Direct exec-server protocol round-trip (exec_client_poc.py)
codex exec-server --listen ws://127.0.0.1:7878 &
python exec_client_poc.py
# →  initialize (sessionId), process/start (processId),
#    process/read polling, base64 chunks decoded → real `ls` output,
#    exited=true, exitCode=0.

# 2. Wrapped translator (env_mcp_poc.py + drive_env_mcp.sh)
EXEC_WS=ws://127.0.0.1:7878 EXE_DESC="PoC executor" \
  ./drive_env_mcp.sh
# Sends MCP initialize / tools/list / tools/call, env-mcp:
#   - returns one shell tool with description carrying executor label,
#   - on tools/call name=shell args={command:["ls","-la",...]}, runs
#     process/start + read loop, returns aggregated stdout as
#     MCP {content:[{type:"text", text:"…\n[exit_code=0]"}], isError:false},
#   - on tools/call args={command:["false"]}, returns isError:true
#     and [exit_code=1] in the text body.
```

What this proves:

- ✅ Upstream codex `exec-server` accepts a plain WebSocket on
  `ws://host:port` (no special headers, just **don't request
  `permessage-deflate`** — the server closes connections that do; the
  PoC client sets `compression=None`). This is one bullet for the
  future Go implementation: use `nhooyr.io/websocket` with default
  settings (it doesn't request deflate by default) — confirmed safe.
- ✅ The exec-server method names + payload shapes match the spec:
  `initialize` / `process/start` / `process/read` (camelCase fields:
  `processId`, `argv`, `cwd`, `env`, `tty`, `pipeStdin`, `arg0`,
  `afterSeq`, `maxBytes`, `waitMs`, `nextSeq`, `exitCode`, `chunks[]`
  with `seq`/`stream`/`chunk` (base64)).
- ✅ A single ws connection per env-mcp child can drive arbitrarily
  many `process/start` cycles back-to-back — sessionId is stable, ids
  monotonically increment locally.
- ✅ Translator can produce a clean, deterministic `text` aggregate
  + `isError` flag suitable for direct relay into codex's MCP tool
  output stream.

What this does NOT prove (still deferred):

- Behavior under stdin-needed shells (interactive `python -i`, etc.).
  Phase 1 only needs non-interactive shell — out of scope.
- Cap-token Bearer auth on /bridge (the PoC uses a local exec-server
  with no auth). The auth path is just an Authorization header on the
  ws upgrade; trivially additive.
- Concurrency / cancellation. The PoC is single-call. Phase 1 plan
  must add a per-MCP-call cancellation hook so codex's
  abort-tool-call surface terminates the underlying process.

## Pre-implementation PoC (gate before plan rewrite)

Before writing the new plan set, run a one-day PoC:

1. By hand, write a `~/.codex/config.toml` with `[features].shell_tool = false`,
   `[features].unified_exec = false`, `[features].apply_patch_freeform = false`,
   and one `[mcp_servers.local_test]` pointing at a trivial stdio MCP
   server (any throwaway script that exposes a `shell` tool returning
   stub output).
2. Run `codex exec --experimental-json --cd /tmp "list files in cwd"` in
   that CODEX_HOME. Confirm:
   - The LLM call sees only `local_test__shell` (no `shell`, no
     `apply_patch`, no `unified_exec`).
   - The LLM picks `local_test__shell` and the stub MCP server's stdout
     reaches codex's tool-output stream.
3. If step 2 fails (e.g. some builtin re-asserts itself, or codex
   complains about no shell at startup), file the gap as a "small fork
   patch we now need" and update this spec before the plan rewrite.
   Otherwise proceed to write the new plans.

## Open risks (replaces 2026-05-05 § Open risks 1-2; risks 3-5 unchanged)

1. **Feature key drift.** `[features].shell_tool` etc. are stable today,
   but if upstream codex renames or removes them, the per-turn config
   fragment silently re-enables local execution. Mitigation: e2e test
   fetches `tools/list` from the spawned codex at startup and asserts
   `shell`/`apply_patch`/`unified_exec` absent.
2. **codex MCP host parity with `codex exec`.** All previous Anthropic
   internal MCP usage of codex is in `codex` interactive / `codex exec`
   non-interactive paths; we depend on `codex exec --experimental-json`
   loading `[mcp_servers]` from CODEX_HOME the same way the interactive
   path does. The PoC step verifies this directly. If it doesn't, we add
   `--mcp-config-file <path>` flag passing in `runner/runner.go` (no fork
   patch needed; just need to pick the right invocation).
3-5. **Backpressure across the bridge / capability-token replay /
   manifest staleness within a turn** — same as 2026-05-05 spec §
   Open risks 3, 4, 5. No new risks specific to the MCP path.

## Testing strategy (delta)

- **codex fork tests:** N/A in this rewrite.
- **codex-app-gateway tests:** add a snapshot test for `runner/mcpconfig.go`
  rendering exactly the TOML shape above for representative manifests
  (1 exe, 2 exes, default-only). The MCP child path is exercised in
  e2e.
- **env-mcp tests:** unit tests per source file + integration test pairs
  against a fake exec-gateway that returns canned exec-server frames.
- **codex-exec-gateway tests:** unchanged from 2026-05-05.
- **End-to-end:** docker-compose test now includes `codex` CLI inside
  the codex-app-gateway image, plus a tmpfile-based assertion that the
  spawned codex's tools/list matches `[mcp__exe_alpha__shell, mcp__exe_alpha__apply_patch]`
  before the first turn message.

## Migration

There is no migration: the prior plan set produced no code. The four
2026-05-05 plan files (`...-foundations.md`, `...-runtime.md`,
`...-codex-exec-gateway.md`, `...-codex-gateway-e2e-tests.md`) will be
rewritten in place to match this spec. Until then they should be treated
as historical context and **not executed**.
