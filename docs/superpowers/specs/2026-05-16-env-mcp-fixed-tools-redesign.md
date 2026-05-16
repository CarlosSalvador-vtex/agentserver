# env-mcp Fixed-Tools Redesign

**Date:** 2026-05-16
**Status:** Approved (supersedes per-server design from 2026-05-10 spec § "Why per-MCP-server")

## Goal

Replace the per-executor MCP-server-per-bridge design with **one fixed MCP server** whose tools mirror codex's built-in remote-execution tool surface, taking `env_id` as a parameter. Solves the executor-list-refresh race and gives the LLM a stable, discoverable tool surface.

## Why now

The 2026-05-10 design (one env-mcp child per registered executor) had two unfixable flaws in practice:

1. **Executor-list race.** `Connected()` runs at codex-app-server spawn time and writes a fixed `[mcp_servers.exe_*]` block into config.toml. Executors registering after the spawn never appear; executors going offline leave dangling tools. Operators must POST `/admin/sessions/restart` to refresh — confusing UX.
2. **Tool naming surface depends on registration state.** LLM has to pick `mcp__exe_<random_hex>__shell` and infer which physical machine that is from the tool description, instead of asking "which environments are available?".

The original spec's escape clause ("collapsing to one MCP server with environment_id-style multiplexing becomes worth revisiting") fires now.

## Tool surface

env-mcp exposes one MCP server named `agentserver` with this fixed tool set, mirroring `codex-rs/exec-server/src/protocol.rs` RPC capabilities:

| Tool | Maps to exec-server | Description |
|---|---|---|
| `list_environments` | (loopback → app-gateway) | `{}` → `[{env_id, description, is_default, last_seen}]` |
| `shell` | `process/start` + poll `process/read` until exit | `{env_id, command:[], cwd?, timeout_ms?}` |
| `unified_exec` | `process/start` (returns persistent session_id) | `{env_id, command:[], cwd?}` → `{session_id}` |
| `write_stdin` | `process/write` | `{env_id, session_id, data}` |
| `read_output` | `process/read` | `{env_id, session_id, after_seq?, wait_ms?}` → `{chunks, next_seq, exited, exit_code}` |
| `terminate` | `process/terminate` | `{env_id, session_id}` |
| `apply_patch` | env-mcp parses patch → `fs/readFile` + `fs/writeFile` | `{env_id, patch}` — accepts codex's apply_patch grammar |
| `read_file` | `fs/readFile` | `{env_id, path, offset?, limit?}` |

Deferred to v2: `grep_files` (LLM can use `shell` with `rg`), `write_file` (covered by apply_patch in most cases), `fs/getMetadata` / `fs/readDirectory` / `fs/remove` / `fs/copy`, `http/request`.

codex's built-in tool features stay disabled (`shell_tool=false`, `unified_exec=false`, `apply_patch_freeform=false`) so the LLM uses our MCP versions exclusively.

## Cap token

**Before:** payload `{turn_id, workspace_id, exe_ids[]}` — one token per executor per turn, baked into MCP child env at spawn.

**After:** payload `{turn_id, workspace_id}` — one token per turn, valid for any executor in `workspace_executors[workspace_id]`. env-mcp uses the same token for every `/bridge/{exe_id}` it dials.

`/bridge/{exe_id}` handler change:
1. Verify cap-token signature + TTL + revoke set (unchanged)
2. Confirm `workspace_executors` table contains `(token.workspace_id, exe_id)` — replaces the current "exe_id ∈ token.allow_list" check

Blast radius trade-off: a leaked workspace token now exposes every executor in the workspace, not just one. Mitigations:
- TTL stays at 1h (unchanged)
- Turn revocation works the same (we revoke by `turn_id`, not by `exe_id`)
- env-mcp child lives in the codex-app-gateway pod; the only egress for the token is the loopback /internal/connected and the codex-exec-gateway /bridge. No user-machine code ever sees it.

## Architecture

```
codex-app-gateway pod
 ├── per-workspace "codex app-server" subprocess  (supervisor; lifetime = idle reaper)
 │     └─ MCP child: env-mcp (single, stateless multiplexer)
 │            ├─ tools/list → 8 fixed tools (no executor enumeration)
 │            ├─ tools/call list_environments → GET 127.0.0.1:8080/internal/connected
 │            └─ tools/call shell/unified_exec/.../apply_patch/read_file
 │                  └─ map[exe_id]*BridgeClient pool, lazily dialed
 │                     ws → codex-exec-gateway /bridge/{exe_id}  (workspace cap-token)
codex-exec-gateway pod
 └── /bridge/{exe_id}: verify cap-token, check (token.workspace_id, exe_id) ∈ workspace_executors
       ↓ frame-level transparent forwarding (unchanged)
[user machine: codex exec-server --remote]
```

What disappears:
- Per-executor spawn-time `[mcp_servers.exe_*]` blocks in config.toml
- Per-executor `CXG_BRIDGE_TOKEN_<EXE>` env vars
- `POST /admin/sessions/restart` (no longer needed; executor list is live)
- `SpawnConfig.Executors` slice in supervisor.SpawnConfig

What's added:
- `GET /internal/connected` on codex-app-gateway, bound to 127.0.0.1, auth via per-spawn one-time loopback token
- env-mcp connection pool with per-exe lazy dial + close on first read error
- Pure-Go apply_patch grammar parser

## Failure modes

- **Executor disappears mid-call:** `process/start` or `process/read` returns ws close; env-mcp wraps as MCP `isError=true` content with "environment exe_xxx is no longer connected". LLM can call `list_environments` to re-discover.
- **No executors in workspace:** `list_environments` returns `[]`. Calling any other tool with an `env_id` returns `isError=true` with "no such environment". LLM should call `list_environments` first.
- **apply_patch on a file the executor sandbox can't write:** `fs/writeFile` returns JSON-RPC error; surfaced as `isError=true`.
- **Loopback /internal/connected unavailable:** rare (same pod), but env-mcp returns `isError=true` with "executor list temporarily unavailable; retry".

## Compatibility

- Breaking for **agentserver-issued tokens** — existing ast_* tokens unchanged; cap-token shape change is internal between app-gateway and exec-gateway.
- Breaking for **rolling upgrades**: app-gateway v0.51 + exec-gateway v0.50.x is incompatible (allow_list check fails). Both must upgrade together. Helm chart bundles both, so this is automatic.
- env-mcp child is bundled in the codex-app-gateway binary, so no separate version skew there.

## Non-goals

- Multi-workspace executor sharing (still strict workspace boundary)
- Connection pooling across codex app-server subprocess restarts (each spawn gets a fresh pool)
- Streaming `process/output` push notifications (env-mcp still polls `process/read` — exec-server already supports both, but the polling code is simpler and `read_output` semantics naturally fit the MCP request/response model)
- Per-tool authorization (single workspace token covers all executors and all tools — same as before, just unified)

## Versioning

Ship as `v0.51.0` (minor bump — internal protocol break between app-gateway and exec-gateway).
