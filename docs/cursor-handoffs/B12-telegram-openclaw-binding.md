# B12 — Allow Telegram channels to bind OpenClaw sandboxes (not just nanoclaw)

> Spec authored via `/spec` (gstack). GitHub Issues are disabled on this fork, so the
> spec lives here as a backlog-ready handoff. Unblocks `docs/multibot-im-simulation-plan.md`.

## Context

The multi-bot simulation (sales / collections / SAC over Telegram) needs **skill-running
OpenClaw bots** to converse on Telegram and receive proactive outreach via Automations
(PR1–3, #134/#138/#143). Today Telegram binding is gated to `nanoclaw` sandboxes, so an
OpenClaw bot (which runs the sales/cobrança skills) cannot bind a Telegram bot token.
WhatsApp would need 5 SIMs (unavailable), so Telegram is the chosen channel for the sim.

## Current State (verified 2026-05-29)

- `internal/imbridgesvc/handlers.go:405` hard-gates the Telegram configure handler:
  ```go
  if sbx.Type != "nanoclaw" {
      http.Error(w, "telegram binding is only available for nanoclaw sandboxes", http.StatusBadRequest)
      return
  }
  ```
  Any non-nanoclaw sandbox (e.g. `openclaw`) → **400**.
- The rest of `handleIMTelegramConfigure` is type-agnostic: validates the token
  (`TelegramGetMe`), `CreateIMChannel`, `SaveIMChannelCredentials`, `BindSandboxToChannel`,
  then `StartPoller`.
- **Inbound** routing is driven by `routing_mode` ("codex" default; "nanoclaw" deprecated),
  NOT by sandbox type: poller → `forwardMessage` → `forwardToCodex` → `POST
  /api/internal/imbridge/codex/turn` → `processTurn` → `runTurnSync` → `ExecSimple` runs a
  turn in whatever pod the channel's sandbox is. (`internal/imbridge/bridge.go:446`,
  `internal/server/codex_im_inbound.go`.)
- **Outbound** (incl. Automations proactive fire) delivers via `POST
  /api/internal/imbridge/send` → the channel's provider (`telegram`) `sendMessage`.
- Sandbox type constant: `SandboxTypeOpenclaw = "openclaw"` (`internal/sandbox/types.go:7`).

**First-principles finding:** the nanoclaw gate is **vestigial** (from when Telegram was
nanoclaw-only). Inbound + outbound already route through the codex/OpenClaw-agnostic path.
Relaxing the gate is expected to be the only change needed.

## Proposed Change

Relax the gate to an **allowlist of `{openclaw, nanoclaw}`** (decision D1=A; reject
unknown/other types like `hermes` with a clear 400). Keep WhatsApp/weixin/matrix paths
untouched.

### Implementation Details

`internal/imbridgesvc/handlers.go` ~line 405, replace:
```go
if sbx.Type != "nanoclaw" {
    http.Error(w, "telegram binding is only available for nanoclaw sandboxes", http.StatusBadRequest)
    return
}
```
with:
```go
if sbx.Type != "openclaw" && sbx.Type != "nanoclaw" {
    http.Error(w, "telegram binding requires an openclaw or nanoclaw sandbox", http.StatusBadRequest)
    return
}
```
No other handler changes expected. Confirm `handleIMTelegramDisconnect` and the poller
lifecycle have no separate nanoclaw assumption (grep shows none).

## Acceptance Criteria

1. `POST /api/sandboxes/{id}/im/telegram/configure` on an **openclaw** sandbox with a valid
   bot token returns 200 `{connected:true, bot_id}` and binds the channel + starts the poller.
2. The same on a **nanoclaw** sandbox still works (no regression).
3. A sandbox of any other type (e.g. `hermes`) returns 400 "telegram binding requires an
   openclaw or nanoclaw sandbox".
4. Inbound: a Telegram message to the bot reaches the OpenClaw pod (turn runs, skill replies
   via `sendMessage`).
5. Outbound: an Automation firing on the Telegram channel delivers the message to the chat.
6. WhatsApp / weixin / matrix configure + routing unchanged.
7. `go build -tags goolm ./...` + `go vet` pass.

## Testing Plan

| Layer | What | Count |
|-------|------|-------|
| Unit | `handleIMTelegramConfigure` rejects non-{openclaw,nanoclaw} (400), accepts openclaw type past the gate | +1–2 |
| Integration (manual, dev) | 1 OpenClaw sales bot: BotFather token → configure → /start → inbound reply → 1 Automation fires outbound | E2E |

Unit test needs a fake sandbox store + bridge in imbridgesvc; if no harness exists, at
minimum assert the gate decision (type → allowed/blocked) on the handler.

## Rollback Plan

Revert the one-line gate change. No migration, no data, no schema. Zero rollback risk.

## Effort Estimate

- Gate change: ~5 min. Unit test: ~30 min (depends on imbridgesvc test harness). Manual
  Telegram E2E: ~30 min once a BotFather token exists (user action). Total dev: ~1h.

## Files Reference

| File | Change |
|------|--------|
| `internal/imbridgesvc/handlers.go:405` | Relax nanoclaw gate → allow openclaw+nanoclaw |
| `internal/imbridgesvc/*_test.go` | Add gate test (new file if none) |

## Out of Scope

- Authoring the 5 sales / 5 collection / 1 SAC skills (separate work).
- WhatsApp multi-number setup. Webhook-vs-polling changes. Multi-team orchestration.
- Any change to the codex/OpenClaw turn path (it already works type-agnostically).

## Related

- `docs/multibot-im-simulation-plan.md` (#144) — the simulation this unblocks.
- Automations PR1–3 (#134/#138/#143) — proactive outreach engine.
