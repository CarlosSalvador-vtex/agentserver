# Phase 0 findings — Codex fork plan

**Date**: 2026-05-04
**Status**: Complete; informed final architecture (see `2026-05-04-codex-fork-remote-harness-design.md`)

## TL;DR — almost nothing to fork

Codex already supports the deployment topology we want **upstream**:

- `codex --remote ws://host:port --remote-auth-token-env ENV_VAR` — TUI dials a remote app-server
- `codex-app-server --listen ws://IP:PORT [--ws-auth ...]` — app-server accepts incoming WS connections
- `CODEX_EXEC_SERVER_URL=ws://...` — tells the harness to route file/exec ops to a remote exec-server
- `codex exec-server --listen ws://IP:PORT` — standalone exec-server binary (laptop side)

Plus all the trait machinery (`RemoteFileSystem`, `RemoteProcess`, `LazyRemoteExecServerClient`, `EnvironmentProvider`) is implemented and used.

**Final architecture decision**: we add ONE small upstream patch (~100 LOC, pure addition) — a `--connect` mode for `codex exec-server` that dials outbound instead of listening. This makes both cross-laptop-cluster connections clean WSS endpoints, avoids tunneling raw WS frames at byte level, and keeps debugging tractable. See the design spec §5.1 for the patch sketch.

The phrase "fork" survives but the diff is so small it's effectively a feature flag's worth of code. Rebase against upstream is trivial because the patch is purely additive.

## Evidence (file paths)

### Remote harness on agentserver

| Capability | File / line | Notes |
|---|---|---|
| `--listen ws://IP:PORT` CLI flag | `app-server/src/main.rs:33-37` | Default `stdio://`; supports `unix://`, `ws://`, `off` |
| WS server transport | `app-server-transport/src/transport/websocket.rs` | Axum-based, full server (origin filtering, health, backpressure, broadcast) |
| WS auth modes | `app-server-transport/src/transport/auth.rs` — `WebsocketAuthCliMode::CapabilityToken` / `SignedBearerToken` | SHA-256 capability or signed JWT |

### Remote exec from harness

| Capability | File / line | Notes |
|---|---|---|
| `EnvironmentProvider` trait | `exec-server/src/environment_provider.rs:18` | Returns `Environment` per ID |
| `REMOTE_ENVIRONMENT_ID = "remote"` | `exec-server/src/environment.rs:44` | Picked as default when `CODEX_EXEC_SERVER_URL` is set |
| `CODEX_EXEC_SERVER_URL` env var | `exec-server/src/environment_provider.rs:39` | Single switch |
| `RemoteFileSystem` impl | `exec-server/src/remote_file_system.rs` (305 lines) | Implements `ExecutorFileSystem` over WS to remote |
| `RemoteProcess` impl | `exec-server/src/remote_process.rs` (92 lines) | Implements `ExecBackend` over WS |
| `LazyRemoteExecServerClient` | `exec-server/src/client.rs` | Takes `websocket_url: String` |

### Standalone exec-server binary

| Capability | File / line | Notes |
|---|---|---|
| `codex exec-server --listen ws://...` | `cli/src/main.rs:171-173, 1182-1184, 1256-1268` | Marked `[EXPERIMENTAL]` but functional |
| Default listen address | `cli/src/main.rs:1207` | `ws://127.0.0.1:0` (random port) |

### Remote TUI mode

| Capability | File / line | Notes |
|---|---|---|
| `--remote ws://...` CLI flag | `cli/src/main.rs:666` | Targets a remote app-server |
| `--remote-auth-token-env ENV_VAR` | `cli/src/main.rs:671` | Bearer token from env var |
| `AppServerTarget::Remote { ws_url, auth_token }` | `tui/src/lib.rs:294-298` | Internal mode the flags select |
| WS+auth validation | `tui/src/lib.rs:362-371` | Auth tokens require `wss://` or loopback `ws://` |

## Validation results

| Spike | What we tested | Status |
|---|---|---|
| Spike 1 (RemoteEnvironmentManager) | Env-var switch routes file/exec to remote URL | ✅ Confirmed via code reading; `RemoteFileSystem` + `RemoteProcess` impls + `CODEX_EXEC_SERVER_URL` switch exist upstream |
| Spike 2 (WS transport) | `codex-app-server --listen ws://127.0.0.1:9999` binds & accepts JSON-RPC initialize | ✅ **Live** — server bound, `/healthz` 200, JSON-RPC `initialize` handshake completed; response: `{"codexHome":"/root/.codex","platformFamily":"unix","platformOs":"linux",...}` |
| Spike 3 (model base_url) | `chatgpt_base_url` / per-provider `base_url` is respected at runtime | ✅ Validated in production: existing `~/.codex/config.toml` already routes Codex traffic via `[model_providers.modelserver] base_url = "https://code.ai.cs.ac.cn/v1"` — same mechanism we'd use for our llmproxy |

## Updated architecture

```
                          USER LAPTOP                                   AGENTSERVER
                          ───────────                                   ───────────

                          ┌── codex (upstream)                         codex-broker (Go, new)
                          │     --remote wss://.../codex/ws ──────→     ├── /api/codex/sessions/{sid}/ws
                          │     --remote-auth-token-env AS_TOKEN        │   bearer auth → relay frames
                          │                                             │
                          │   register executor ──── tunnel ───────→  executor-registry
                          ├── (tunnel listener)                          │
                          │                                             │
                          │   codex exec-server                         │  spawn codex-app-server
                          │     --listen ws://0.0.0.0:9090              │  --listen unix:///tmp/codex-{sid}.sock
                          │                                             │  with env CODEX_EXEC_SERVER_URL=
                          │     ↑ (exec-server protocol)                │       ws://localhost:N (forwarded
                          │     │                                        │       through tunnel to laptop:9090)
                          └─────┴──── tunnel-forwarded WS ←─────────────┘
```

Key plumbing we add:
1. **WS endpoint** `/api/codex/sessions/{sid}/ws` — Bearer auth, frame-relay to per-session unix-socket app-server
2. **Local TCP forwarder** — for each active session, agentserver opens `ws://localhost:N` on the codex-broker pod that pipes through the executor-registry tunnel to the laptop's `codex exec-server` listener
3. **Lifecycle** — spawn `codex-app-server` per session, set its env, hold it alive, kill on disconnect grace timeout

## Updated workload

| Component | Original estimate | Updated estimate | Delta |
|---|---|---|---|
| Codex fork (Rust) | ~10 working days | **0 days** for v1 (configuration-only); patch layer can be added later if branding / login flow needs it | 🟢 huge reduction |
| `internal/codexbroker/` (Go) | ~3-4 days | ~5-7 days (added: tunnel-WS forwarder, per-session lifecycle) | 🔴 small increase |
| `/api/codex/*` route + bearer auth | ~1 day | ~1 day | unchanged |
| executor-registry: tunnel L4 ws forwarder | ~0.5 day | ~2-3 days (we need an L4 conduit, not just `/api/execute`) | 🔴 increase |
| Persistence (Postgres tables) | ~1 day | ~1 day | unchanged |
| Helm + ingress | ~0.5 day | ~0.5 day | unchanged |
| Local agent — distribute upstream `codex` binary | ~3-4 days reimplement | **~0.5 day** (just package it) | 🟢 huge reduction |
| End-to-end integration | ~1 week | ~1 week | unchanged |
| **Total** | **~4-5 weeks** | **~2-3 weeks** | 🟢 **halved** |

## Risks / what changed

**Lowered risks**:
- ❌ ToolRouter chokepoint risk → **moot** (we don't touch ToolRouter; we use the env-var-driven environment switch)
- ❌ Fork rebase / upstream tracking burden → **moot** for v1
- ❌ Reimplementing tools in Rust or Go → **moot** (`codex exec-server` is the canonical impl)

**Newly visible risks**:
- 🟡 **Tunnel L4 forwarding**: existing tunnel does `/api/execute` HTTP routing; we need **websocket pass-through** for the exec-server protocol. Probably means a new tunnel framing for "open a stream to laptop:port" — non-trivial but bounded
- 🟡 **WS auth integration**: `codex-app-server`'s `--ws-auth` modes (CapabilityToken or SignedBearerToken) don't directly accept Hydra-issued tokens. Cleanest path: bind `codex-app-server` to a unix socket / loopback (no auth there), do bearer auth at the agentserver WS endpoint, frame-relay
- 🟢 **Codex cwd / workspace**: harness needs `cwd: AbsolutePathBuf`; remote exec-server provides files at that path. Need to think through "the laptop's cwd" mapping — but Codex's existing local+remote test infrastructure (`tests/exec_process.rs`, `tests/initialize.rs`) already exercises this

## Decision

Recommend **proceed to Phase 1** with revised plan:
- Start agentserver `internal/codexbroker/` skeleton + bearer-authed WS endpoint
- Extend executor-registry tunnel with L4 WS forwarding
- Set up an end-to-end smoke test (laptop running `codex exec-server` + dialing remote agentserver via `codex --remote`)

Do **not** invest in a Codex fork for v1. Track upstream by version pin only. Revisit fork decision after the integration is running and we have concrete UX gaps.
