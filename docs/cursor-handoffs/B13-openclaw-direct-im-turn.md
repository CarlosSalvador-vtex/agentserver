# B13 — OpenClaw-direct IM turn (replace codex/cxg dependency)

> Spec + design (gstack `/spec`). Issues disabled on fork → handoff doc.
> Supersedes the codex-routed inbound path for OpenClaw channels. Unblocks the
> multi-bot sim (`docs/multibot-im-simulation-plan.md`) AND live Automations delivery.

## Context — the real blocker (found via live Telegram E2E, 2026-05-29)

We wired a Telegram bot end-to-end on dev: bot created (@vendas_sim1_cs_bot), workspace
Telegram channel configured (200, poller started), OpenClaw sandbox bound, a customer
message delivered to agentserver. The bot never replied. Logs show why:

```
POST http://agentserver:8080/api/internal/imbridge/codex/turn - 405 0B   (every ~2s, in a loop)
```

The imbridge poller forwards every inbound message to `forwardToCodex` →
`POST /api/internal/imbridge/codex/turn`. That route is **only registered when
`CODEX_APP_GATEWAY_REST_URL` / `CODEX_APP_GATEWAY_URL` is set** (`internal/server/server.go:339`).
Dev has neither → route absent → **405 → no agent turn ever runs**.

### Why this matters beyond Telegram

- **All IM inbound** (Telegram, WhatsApp, weixin) on this deploy routes through
  `forwardToCodex` → the same dead `/codex/turn` endpoint.
- **Automations** run the same way: `fireAutomation → runTurnSync → h.codex.RunTurn → cxg`.
  So Automations PR1–3 (#134/#138/#143) cannot deliver live in dev either — their tests
  pass only because they use fakes. (Consistent with the "pending live E2E" note in
  `docs/productized-automations-spec.md`.)
- `codex-app-gateway` (cxg) is **not in this repo's deploy** (`deploy/helm/` has only
  `agentserver` + `litellm`) and codex is documented **do-not-extend / not-used** (#119).
- The nanoclaw gate (B12 #146) was NOT the blocker — that handler is sandbox-scoped and
  unrouted (404). The live workspace-scoped configure has no gate.

## Decision

**Run the IM turn directly in the OpenClaw pod, not via codex/cxg.** Reuse the LLM stack
OpenClaw already uses (llmproxy → Bedrock). This is what the automations eng-review (#133)
locked ("run via `GetSandboxForChannel` → `ExecSimple`") and what `codex-not-used` (#119)
implies.

### Mechanism (confirmed by spike in a live pod)

The OpenClaw image ships a one-shot turn CLI:

```
openclaw agent --message "<text>" [--json] [--session-id <id>] [--to <e164>] \
               [--channel telegram --deliver]
# "Run an agent turn via the Gateway"
```

- `--message` — the inbound text.
- `--json` — structured result (capture reply text).
- `--session-id` / `--to` — derive/scope the session (conversation memory).
- `--channel <c> --deliver` — optionally let OpenClaw deliver the reply itself.

`ExecSimple(sandboxID, cmd)` already exists (`internal/sandbox/manager.go:1033`) and runs a
command in the pod, returning stdout. `GetSandboxForChannel(channelID)`
(`internal/db/im_channels.go`) resolves a channel → its bound sandbox.

### B1 vs B2 — deliver via imbridge (chosen) vs OpenClaw-native

- **B1 (chosen):** agentserver execs `openclaw agent --message <text> --json` (NO
  `--deliver`), captures the reply text, delivers via the existing imbridge send path
  (`POST /api/internal/imbridge/send` → provider). **imbridge owns IM I/O; the pod just
  computes the turn.** Fits multi-tenant (one bridge brokers all channels).
- **B2 (rejected):** configure the Telegram channel inside each OpenClaw pod (`openclaw
  channels add`) and let the pod poll + deliver itself, bypassing imbridge. Simpler per-bot
  but bypasses the multi-tenant bridge + duplicates channel state. (This is how the user's
  personal "Hector OpenClaw" runs, but it's not the platform model.)

## Proposed Change (B1)

1. **New inbound routing for OpenClaw channels.** In the imbridge inbound path
   (`internal/imbridge/bridge.go` `forwardMessage`), add a routing mode (e.g.
   `routing_mode="openclaw"`) that, instead of `forwardToCodex`, calls a new agentserver
   internal endpoint (or in-process path) that:
   - `GetSandboxForChannel(channelID)` → sandboxID (resume/spawn if none — reuse the
     automations fallback).
   - `ExecSimple(sandboxID, ["node","openclaw.mjs","agent","--message",text,"--session-id",
     <derived>,"--json"])` → capture stdout.
   - Parse the JSON reply, deliver via the imbridge send path.
   - On error, log + (for automations) set `last_error`.
2. **Automations reuse the same path.** Replace `runTurnSync → h.codex.RunTurn` with the
   OpenClaw-exec turn so `fireAutomation` delivers without codex.
3. **Keep codex path intact but optional.** Channels with `routing_mode="codex"` still use
   cxg when configured; default for OpenClaw sandboxes becomes the direct path.
4. **Session mapping.** Derive a stable `--session-id`/`--to` from (channel, user) so
   conversation memory persists across turns (mirror the codex session store).

## Acceptance Criteria

1. A Telegram message to an OpenClaw-bound channel triggers `openclaw agent` in the pod and
   the reply is delivered back to the chat (no codex/cxg, no 405).
2. Multi-turn memory works (second message continues the session).
3. An Automation firing on an OpenClaw channel delivers a proactive message via the same path.
4. `routing_mode="codex"` channels unchanged when cxg IS configured.
5. WhatsApp/weixin inbound on OpenClaw channels work via the same direct path.
6. `go build -tags goolm ./...` + `go vet` pass; unit test for the exec-turn command
   construction + a parse-reply test.

## Out of Scope / follow-ups

- Authoring the 5 sales / 5 collection / 1 SAC skills. NOTE secondary finding: a
  **prompt.md-only skill does NOT load as an OpenClaw plugin** ("plugin not found" warning);
  persona must live in the **soul** (systemPrompt) or a proper plugin (manifest + index.mjs).
  Sim personas should use souls, not bare-prompt skills.
- Spawning/lifecycle of a persistent (non-test-TTL) sandbox per bot.
- Removing the dead sandbox-scoped telegram configure handler.

## Effort

- Spike: done (mechanism confirmed). Core routing + exec + deliver: ~M (new bridge routing
  mode + an internal turn endpoint + session mapping). Automations swap: ~S. Tests: ~S.

## Files Reference

| File | Change |
|------|--------|
| `internal/imbridge/bridge.go` (`forwardMessage`) | add `openclaw` routing → call direct-turn endpoint instead of `forwardToCodex` |
| `internal/server/*` (new handler) | internal endpoint: GetSandboxForChannel → ExecSimple `openclaw agent` → parse → imbridge send |
| `internal/server/automation_scheduler.go` / codex_im_inbound | automations + inbound reuse the exec-turn path (drop hard codex dependency) |
| `internal/sandbox/manager.go:1033` | reuse `ExecSimple` |
| `internal/db/im_channels.go` | reuse `GetSandboxForChannel` |

## Related

- `docs/multibot-im-simulation-plan.md` (#144) — the sim this unblocks.
- `docs/productized-automations-spec.md` (#133/#140) — automations; same turn path.
- `docs/ops/codex-not-used.md` (#119) — why we avoid cxg.
- B12 #146 — relaxed a dead handler; not the blocker (kept, harmless).
