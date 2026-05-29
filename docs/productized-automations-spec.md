# Productized automations — design proposal

> Turn agentserver from a reactive (IM-triggered) agent into a **proactive** one:
> ready-made + author-able automations that run a skill on a schedule and deliver to
> a channel. Inspired by openLEO's briefs/inbox-triage/reports (see
> `docs/openleo-competitive-analysis.md`). Status: PROPOSAL.

## Problem

agentserver agents only act when a user messages them (IM-triggered). openLEO ships
"productized automations" — morning briefs, inbox triage, follow-up reminders, weekly
reports — where the agent **wakes on a schedule**, runs a skill, and posts the result
to a channel, with no prompt. That recurring, proactive value is what makes the
product sticky. agentserver has the pieces but no scheduler + no proactive run.

## What an automation is

A bundle the tenant **enables and lightly configures** (no code):

```
Automation = { skill_ref, cron, channel, config }
  skill_ref : draft:<id> | git:<name>@<sha>   (the logic — reuses composition)
  cron      : schedule (e.g. "0 8 * * 1-5")    (when to run)
  channel   : which IM channel + recipient     (where the result lands)
  config    : per-skill params (configSchema)   (the light, no-code knobs)
```

## What exists vs the gap

| Building block | State |
|----------------|-------|
| Skill logic | ✅ skills + composition (`draft:`/`git:` refs) |
| Channel delivery | ✅ IM bridge (WhatsApp / weixin / telegram / matrix) |
| Agent task queue | ✅ `POST /api/agent/tasks`, `GET /api/agent/tasks/poll`, status/get |
| Persona / memory | ✅ soul + session/workspace context |
| **Scheduler (per-workspace cron)** | ❌ only internal tickers (reaper, health) — none user-facing |
| **Proactive / headless run** | ❌ sandboxes are reactive (IM-triggered); no "wake on schedule, no prompt" path |
| **Automation template + enable UX** | ❌ marketplace lists skills, but no "enable scheduled automation" + config |

## Proposed flow

```
1. Tenant enables an automation from a catalog (skill + cron + channel + config form)
2. Scheduler (per-workspace) fires at the cron time
3. Scheduler enqueues a task on the existing queue (POST /api/agent/tasks)
   payload: { skill_ref, config, channel, deliver: true }
4. A sandbox runs the skill headless (composition already supports draft:/git:),
   no user message — the task IS the prompt/intent
5. Result delivered to the configured channel via the IM bridge
6. Run recorded (audit + last-run/next-run shown in UI)
```

The **task queue is the seam** — the scheduler is just a cron-driven producer of tasks
the agent runtime already knows how to consume. That keeps the new surface small.

## Components to build

1. **Scheduler** (`internal/server/automation_scheduler.go`, new)
   - Per-workspace cron entries persisted in DB (migration: `automations` table:
     id, workspace_id, name, skill_ref, cron, channel, config jsonb, enabled,
     last_run_at, next_run_at).
   - A single in-process ticker (or robfig/cron) that, each minute, finds due
     automations and enqueues tasks. Multi-replica safe via a DB advisory lock /
     `SELECT ... FOR UPDATE SKIP LOCKED` so only one replica fires each due row.
2. **Headless run path** — extend the task consumer so a task with `deliver:true`
   spins an ephemeral sandbox with the automation's composition, runs the skill with
   the config as input, captures the output, and routes it to the channel. Reuses the
   test-sandbox / composition + IM send paths.
3. **API + UI** — `POST/GET/PATCH/DELETE /api/workspaces/{wid}/automations`; a catalog
   of ready-made automations (skill + suggested cron) + an "enable + configure" form
   (configSchema). Show last-run / next-run / enable toggle.
4. **Ready-made catalog (parity with openLEO)** — seed a few: daily follow-up, weekly
   report, inbox/lead triage. Each = a published skill + a default cron + a config form.

## Gain x effort

- **Gain: high.** Reactive chatbot → proactive recurring assistant. The recurring,
  unprompted delivery is the stickiness openLEO sells. Also unlocks B2B use cases
  (scheduled cobrança follow-ups, reports).
- **Effort: M–L.** Scheduler infra + multi-replica safety + headless run + catalog/UX
  + one migration. Reuses task queue + composition + IM bridge, so the net-new is the
  scheduler + the headless-with-delivery path.

## Where this beats openLEO

openLEO's automations look fixed (brief / triage / report). On agentserver, the same
catalog gives parity, but because of the Playground a tenant can **author its own
automation** — a custom skill on a custom schedule — not just toggle pre-built ones.
Ready-made (parity) + author-able (moat).

## Risks / open questions

- **Proactive run is real infra.** "Wake with no prompt" + multi-tenant scheduler is
  the hard part. De-risk: prototype one automation end-to-end (cron → task → ephemeral
  sandbox → IM send) before building the catalog/UX.
- **Cost:** each run spins a sandbox. Batch / reuse / short-TTL pods; cap concurrency
  per workspace (reuse the test-sandbox quota pattern).
- **Multi-replica firing:** must not double-fire — DB lock / SKIP LOCKED.
- **Channel auth:** the automation needs a bound IM channel + allowed recipient
  (reuse `workspace_im_channels`).
- **Quiet hours / failure delivery:** what happens when a run errors — notify whom?

## Suggested first step

Spike: one hardcoded automation (e.g. "every 5 min, run skill X, send to my WhatsApp")
wired cron → `POST /api/agent/tasks` → ephemeral sandbox → IM send. Proves the
proactive seam before any catalog/UI. Then run `/plan-eng-review` on the scheduler
architecture (multi-replica safety, run isolation, cost caps).
