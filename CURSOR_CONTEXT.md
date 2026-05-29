# Cursor Agent Context

> Updated: 2026-05-29T00:00:00Z
> Branch: feat/openclaw-direct-turn
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **B13 PR1** — the OpenClaw-direct IM **inbound** turn path, so an IM message to
an OpenClaw-bound channel actually gets a reply (today it dead-ends at codex/cxg → 405).
Scope PR1 to the **inbound** path + session mapping + tests. Defer the Automations swap
(reuses the same helper) and codex removal to a follow-up.

## Source of truth

`docs/cursor-handoffs/B13-openclaw-direct-im-turn.md` — full spec, root cause, decision
(B1), confirmed mechanism, files. Read it first. Also `docs/ops/codex-not-used.md` (why we
avoid cxg).

## Root cause (recap)

`internal/imbridge/bridge.go` `forwardMessage` → `forwardToCodex` → `POST /api/internal/
imbridge/codex/turn`, registered only when `CODEX_APP_GATEWAY_*` is set
(`internal/server/server.go:339`). Dev has none → 405 loop → no turn. codex is
do-not-extend (#119); cxg isn't deployable from this repo.

## Decision (B1) — confirmed mechanism

Run the turn IN the OpenClaw pod and deliver via the imbridge provider. The OpenClaw image
ships a one-shot CLI (verified live in a pod):

```
node openclaw.mjs agent --message "<text>" --json [--session-id <id>] [--to <e164>]
# "Run an agent turn via the Gateway" — returns the reply (use --json to parse)
```

Do NOT use `--deliver` (let imbridge deliver, keeping the bridge as the single IM I/O owner
= multi-tenant model). Do NOT configure channels inside the pod (that's the rejected B2).

## Key wiring that already exists (reuse, do NOT rebuild)

- `internal/imbridge/bridge.go`: the `Bridge` already holds an **`ExecCommander`** (`exec`)
  and a **`SandboxResolver`** (injected in `NewBridge(db, resolver, exec, providers)`), plus
  `getChannelRoutingMode(channelID)` and `forwardMessage`. The openclaw turn can run
  entirely inside the bridge — no new agentserver endpoint needed.
- `ExecCommander.ExecSimple(...)` — runs a command in a sandbox pod, returns stdout
  (impl: `internal/imbridgesvc/exec.go` `K8sExec.ExecSimple(ctx, sandboxID, command []string)`).
- `SandboxResolver` / `DispatchInboundChannel` / `GetSandboxForChannel` — channel → sandbox id.
- Providers expose `SendMessage` (e.g. `TelegramSendMessage`) — used to deliver the reply.

## Files in Scope

- `internal/imbridge/bridge.go` — in `forwardMessage`, add an **`openclaw`** routing mode
  (alongside the existing `codex`): resolve the channel's sandbox id, build the
  `node openclaw.mjs agent --message <text> --json --session-id <derived>` command, call
  `b.exec.ExecSimple(ctx, sandboxID, cmd)`, parse the JSON reply (extract the agent text),
  and deliver via the channel's provider `SendMessage`. On exec/parse/deliver error: log
  (don't crash the poller).
- Session mapping: derive a stable `--session-id` (or `--to`) from (channelID, fromUserID)
  so multi-turn memory persists. Mirror how the codex path derives its session key
  (`codex_im_inbound.go` uses WechatUserID/externalID).
- `internal/imbridge/*_test.go` — unit tests: command construction (message/session-id flags),
  reply-JSON parsing (happy + malformed), routing-mode selection (openclaw vs codex).
- Routing-mode source: a channel bound to an OpenClaw sandbox should resolve
  `routing_mode = "openclaw"`. Decide the cleanest signal (sandbox type lookup, or a
  channel/binding field). Document the choice in the PR. Default OpenClaw channels to the
  direct path; leave `codex` channels untouched.

## Constraints

- No force-push. No new top-level deps. Do NOT remove the codex path (channels with
  `routing_mode="codex"` keep working when cxg is configured).
- Reuse the bridge's existing `exec`/resolver/providers — do NOT add a new agentserver HTTP
  endpoint for this.
- `--json` parse must tolerate the OpenClaw `agent --json` output shape (inspect it; the CLI
  prints config warnings to stderr — read stdout only).
- Build tag `goolm` in all Go commands; integration tests gate on env where needed.
- No migration/OpenAPI change expected. Do NOT commit `web/dist/`. One PR.

## Next Action

Read `docs/cursor-handoffs/B13-openclaw-direct-im-turn.md` + `internal/imbridge/bridge.go`
(`forwardMessage`, `forwardToCodex`, `NewBridge`, `ExecCommander`), then add the `openclaw`
routing branch in `forwardMessage`.

## Done When

- `go build -tags goolm ./...` + `go vet -tags goolm ./...` pass.
- An inbound message on an OpenClaw channel runs `openclaw agent` in the pod and the parsed
  reply is delivered via the provider (no codex, no 405). Logged on error, poller survives.
- Multi-turn memory works (stable session id).
- codex routing untouched for `routing_mode="codex"` channels.
- Unit tests pass (command build + JSON parse + routing selection).
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-29T00:00:00Z STARTED — orchestrator reseeded context for B13 PR1 (openclaw-direct inbound turn)
