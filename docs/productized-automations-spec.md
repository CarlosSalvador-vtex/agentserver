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

> **Superseded by "Locked decisions (eng review)" below.** The original draft routed
> the run through the agent task queue; the eng review changed it to reuse the imbridge
> reactive path. Kept here for history — see "Corrected flow (PR1)".

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
wired cron → run a turn → IM send. Proves the proactive seam before any catalog/UI.

## Locked decisions (eng review 2026-05-29)

Reviewed via /plan-eng-review. Scope reduced to PR1 + the seam corrected:

- **PR1 scope (reduced):** in-process scheduler (mirrors the existing
  `StartPlaygroundReaper` ticker — `replicaCount: 1`, so **no SKIP LOCKED / leader
  election** yet) + an `automations` table + ONE automation running end-to-end.
  **Defer** the catalog, the management UI, and multi-replica safety to later PRs.
- **Run path (corrects the "task queue" framing above):** the proactive run **reuses
  the imbridge reactive path**, not the agent task queue. The reactive path already
  does channel → `GetSandboxForChannel` → `ExecSimple` (run a turn in the pod) →
  deliver via provider. The scheduler synthesizes a turn through that path.
- **Where it lives:** automations **table + cron ticker in agentserver** (with
  workspaces/skills); the run+deliver is done by calling an **internal imbridge
  endpoint** so exec+deliver is never duplicated (DRY).
- **Sandbox lifecycle:** reuse the channel's live sandbox (warm, has memory); **fall
  back** to resume/spawn an ephemeral one if none is alive. Never silently skip.
- **Scheduler query:** partial index `(next_run) WHERE enabled`; scanDue does
  `WHERE enabled AND next_run <= now()` — indexed, scales.
- **Tests:** unit on scheduler branches (scanDue due/none/error, computeNextRun
  valid/malformed, fire sandbox-live/fallback/exec-error/deliver-error) + **one
  integration** simulating cron → fire → fake-deliver, asserting delivery + last_run/
  error status. Silent-failure of a scheduled run is the thing tests must catch.

### Corrected flow (PR1)

```
cron ticker (agentserver, ~1min)
  → scanDue: WHERE enabled AND next_run <= now()   [partial index]
  → for each due automation:
      → call internal imbridge run endpoint { channel, skill_ref/prompt }
          → GetSandboxForChannel (reuse live sandbox; else resume/spawn ephemeral)
          → ExecSimple: run the turn in the pod (no human)
          → deliver output via the channel provider
      → update last_run_at / next_run_at / last_error
```

## NOT in scope (PR1)

- Catalog of ready-made automations + enable/configure UI — follow-up PR.
- Multi-replica safety (SKIP LOCKED / leader election) — only when replicaCount > 1.
- Quiet hours, retry/backoff policy, per-automation concurrency caps — later.
- Per-run cost caps beyond reusing the warm channel sandbox — later.

## What already exists (reused, not rebuilt)

- imbridge reactive path: `GetSandboxForChannel` + `ExecSimple` + provider deliver.
- In-process ticker precedent: `StartPlaygroundReaper`, `idlewatcher`.
- Composition (`draft:`/`git:`), `workspace_im_channels`, sandbox Manager.

## Failure modes (PR1)

| Path | Failure | Test? | Error handling? | Visible? |
|------|---------|-------|-----------------|----------|
| fire → resolve sandbox | channel has no live sandbox | yes (fallback) | resume/spawn ephemeral | last_run ok |
| fire → ExecSimple | exec/turn errors | yes | mark last_error, next run continues | last_error set |
| fire → deliver | imbridge/provider send fails | yes | mark last_error | last_error set |
| scanDue | malformed cron | yes | skip that row, don't kill the loop | last_error set |

Critical-gap guard: a scheduled run that fails must set `last_error` (never silent).

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | CLEAR | 4 issues, 0 critical gaps |

- **SCOPE:** reduced — PR1 = in-process scheduler + 1 automation end-to-end; catalog/UI/multi-replica deferred.
- **DECISIONS:** run reuses imbridge reactive path (not task queue); data+schedule in agentserver, run+deliver via internal imbridge call (DRY); reuse warm channel sandbox + ephemeral fallback; partial index on (next_run) WHERE enabled; unit + 1 integration covering fire→deliver + failure modes.
- **UNRESOLVED:** none.
- **VERDICT:** ENG CLEARED — ready to implement PR1.
