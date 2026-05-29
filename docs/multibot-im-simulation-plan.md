# Multi-bot IM simulation — sales / collections / SAC (test plan)

> How to simulate real teams of agent bots contacting a human over IM, with
> Claude Code role-playing the customer. Status: PLAN. Companion to
> `docs/productized-automations-spec.md` (the proactive-outreach engine this reuses).

## Goal

Three scenarios, each a **team of 5 bots** running **different skills**, talking to a
real person:

- **Case 1 — Sales team:** 5 sales bots proactively reach out and try to sell; the
  customer (Claude Code) replies interested / not interested and negotiates.
- **Case 2 — Collections team:** 5 cobrança bots proactively reach out about a debt;
  the customer (Claude Code) responds (pays, disputes, asks to reschedule).
- **Case 3 — Customer → SAC:** Claude Code (as customer) initiates contact with a
  support/SAC bot and runs a support conversation.

Ideal end state is 5 WhatsApp numbers → `+55 27 99607-3736`, but no spare numbers
exist yet. **Use Telegram instead** for the simulation.

## Why Telegram (not WhatsApp) for the sim

- **Free, instant bot tokens** via @BotFather — one per bot, no SIM/phone per bot
  (WhatsApp Cloud needs a number + Meta app per sender).
- **Telegram Web is automatable** — Claude Code can drive `web.telegram.org` via
  claude-in-chrome to read bot messages and type customer replies (the "customer" side).
- agentserver already has a **Telegram provider** — `internal/imbridge/telegram_api.go`
  (`TelegramGetMe`/`GetUpdates`/`SendMessage`/`SendPhoto`/…) + bind endpoint
  `POST /api/workspaces/{id}/im/telegram/configure {bot_token}`.

## Architecture

```
@BotFather ──(5 bot tokens per team)──┐
                                       ▼
agentserver workspace
  ├─ Telegram channel #1  ─ bound to ─ sandbox #1 (OpenClaw + skill: sales-A / cobranca-A)
  ├─ Telegram channel #2  ─ bound to ─ sandbox #2 (skill: sales-B …)
  ├─ … #3 #4 #5
  │
  ├─ Automations (PR1–3) — one per bot, proactive outreach:
  │     cron/one-shot → ClaimDueAutomations → fire → run a turn → deliver opening msg
  │
  └─ imbridge (telegram provider) ── getUpdates/sendMessage ──► Telegram
                                                                   ▲
                                                                   │  reads bot msgs,
Claude Code ── claude-in-chrome ── web.telegram.org ───────────────┘  types replies
   (role-plays the customer: interested? negotiate? dispute? pay?)
```

**Inbound (customer → bot):** Telegram → imbridge `getUpdates` → agentserver turn →
skill responds → `sendMessage`. **Outbound/proactive (bot → customer):** an Automation
fires the opening message — this is exactly what `docs/productized-automations-spec.md`
PR1–3 built (`@every 1m` or a one-shot cron, prompt = "open the conversation and pitch X").

## What exists vs what to build

| Piece | State |
|-------|-------|
| Telegram provider + bind endpoint | ✅ `telegram_api.go`, `/im/telegram/configure` |
| Per-channel sandbox running a skill | ✅ composition (`draft:`/`git:` refs) |
| Proactive outreach (bot initiates) | ✅ Automations PR1–3 (#134/#138/#143) |
| Customer side automation (Claude as buyer) | ✅ claude-in-chrome on Telegram Web |
| **5 distinct sales skills** | ⚠️ build — PR #142 ships only test personas (`p3-asistente-ventas`) |
| **5 distinct collection skills** | ⚠️ build — PR #142 has `p6-cobranca-acento` + the `cobranca` template as a base |
| **1 SAC/support skill** | ⚠️ build — `p1-atendente-formal` / `p4-cjk-support` personas are a base |

PR #142 (`test(marketplace): template fixtures matrix`) is **test fixtures**, not a
production 5-bot roster — reuse its `p3-asistente-ventas` and `p6-cobranca-acento`
personas + the `cobranca` skill as starting points, then author 5 variants per team
(differ by product, tone, offer ladder, objection handling).

## Setup (per team)

1. **Create 5 Telegram bots** in @BotFather → collect 5 tokens + bot @handles.
2. **agentserver:** create a workspace (e.g. "Sales sim" / "Cobrança sim").
3. For each bot: `POST /api/workspaces/{wid}/im/telegram/configure {bot_token}`
   (or via the IM tab in the UI) → binds a Telegram channel + spawns its sandbox.
4. **Author/compose 5 skills**, one per channel (sales-A..E or cobranca-A..E), via the
   Playground (edit → publish) and bind each to its channel's sandbox composition.
5. **Proactive outreach:** create 1 Automation per bot (Automations tab → New automation
   or Add from catalog) on that bot's channel, cron `@every 1m` (or a one-shot near-future
   cron), prompt = the opening pitch. The scheduler fires → bot DMs the customer.
6. **Customer side:** open each bot in Telegram (`t.me/<bot>` → /start). Claude Code drives
   `web.telegram.org` via claude-in-chrome: reads each bot's message, decides
   interested/not, and replies — running 5 parallel conversations.

**Case 3 (customer → SAC):** skip the outreach automation; Claude Code sends the first
message to the support bot and runs the support flow.

## Caveats / gaps to verify before running

- **Telegram-on-OpenClaw vs nanoclaw:** the sandbox-scoped configure path
  (`internal/imbridgesvc/handlers.go:406`) gates Telegram binding to *nanoclaw* sandboxes.
  Confirm the **workspace-scoped** path (`/api/workspaces/{id}/im/telegram/configure`,
  `im_routes.go:111`) binds Telegram to an **OpenClaw** sandbox running a skill — if it
  also gates to nanoclaw, that's a fix to make first (skills run on OpenClaw).
- **Webhook vs polling:** confirm whether the Telegram provider uses `getUpdates` polling
  or a webhook in dev (webhook needs a public URL; the dev ALB is internal). Polling is
  simpler for the sim.
- **5 distinct skills per team** must be authored — without them the "5 different skills"
  premise collapses to one skill ×5.
- **Quotas:** each bot = a sandbox; per-workspace `workspaceMaxSandboxes` is 5 — exactly
  fits one team of 5. A second team needs a second workspace (or a raised quota).
- **Rate limits:** Telegram bot API rate-limits sends; 5 bots opening at once is fine,
  but tight `@every 1m` loops across many bots could trip limits — stagger or use one-shot.

## Suggested first slice (de-risk)

Stand up **ONE** sales bot end-to-end on Telegram first (1 BotFather token → 1 channel →
1 skill → 1 outreach automation → Claude replies via Telegram Web). Prove the loop, then
fan out to 5 and add the collections + SAC teams.
