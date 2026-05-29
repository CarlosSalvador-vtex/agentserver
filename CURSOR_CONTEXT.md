# Cursor Agent Context

> Updated: 2026-05-29T00:00:00Z
> Branch: feat/automations-pr3
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **PR3 of productized automations** — the two pieces deferred from PR1/PR2:
**(A) multi-replica safety** for the scheduler, and **(B) a ready-made automation
catalog**. PR1 (#134: table + scheduler + run via processTurn) and PR2 (#138: CRUD API +
Automations UI tab) + fix #139 are merged & deployed. Build PR3 on top — reuse, don't
rebuild.

## Source of truth

`docs/productized-automations-spec.md` — read "Locked decisions", the
"Implementation status & pending verification" section (PR1/PR2/#139 done, PR3 = this),
and "NOT in scope (PR1)" (catalog + multi-replica were the deferred items = PR3).

## What already exists (reuse, do NOT rebuild)

- `internal/db/automations.go`: `Automation`, `CreateAutomation`, `GetAutomation`,
  `UpdateAutomation`, `ListAutomations`, `DeleteAutomation`, `ScanDueAutomations`,
  `MarkAutomationRun`, `ComputeNextRun` (robfig/cron/v3).
- `internal/server/automation_scheduler.go`: `StartAutomationScheduler` ticker →
  `runDueAutomations` → `ScanDueAutomations` → `fireAutomation` (per row) → `MarkAutomationRun`.
- `internal/server/automation_handlers.go`: CRUD handlers, `validateCron`,
  `validateAutomationChannel`.
- `web/src/components/WorkspaceAutomationsTab.tsx` + `web/src/lib/api.ts`: management UI + client.
- Migration `046_automations.sql`: table + partial index `(next_run_at) WHERE enabled`.

## Part A — Multi-replica safety (claim with SKIP LOCKED + lease)

Today `ScanDueAutomations` is a plain SELECT and `replicaCount: 1`, so two replicas
would double-fire. Make the scan an atomic **claim** so only one replica fires each due row.

- Migration `047_automation_lock.sql` — NEW: `ALTER TABLE automations ADD COLUMN
  locked_until TIMESTAMPTZ;` (nullable). Optional index if helpful.
- `internal/db/automations.go` — ADD `ClaimDueAutomations(ctx, lease time.Duration, limit int)
  ([]Automation, error)`:
  - One statement: `UPDATE automations SET locked_until = NOW() + $lease
    WHERE id IN (SELECT id FROM automations WHERE enabled AND next_run_at IS NOT NULL
    AND next_run_at <= NOW() AND (locked_until IS NULL OR locked_until < NOW())
    ORDER BY next_run_at ASC FOR UPDATE SKIP LOCKED LIMIT $limit) RETURNING <all cols>`.
  - Atomically claims + leases the rows; concurrent replicas skip locked rows.
  - ComputeNextRun is Go-side (cron parse), so next_run is advanced later by MarkAutomationRun —
    the `locked_until` lease prevents re-claim in the gap between claim and MarkRun.
- `internal/db/automations.go` — `MarkAutomationRun` MUST also clear the lock
  (`locked_until = NULL`) when it sets next_run_at/last_error, so the row is claimable next cycle.
- `internal/server/automation_scheduler.go` — `runDueAutomations` calls
  `ClaimDueAutomations` instead of `ScanDueAutomations` (keep `ScanDueAutomations` if still
  used by tests, or migrate callers). Pick a sane lease (e.g. 5 min) and limit (e.g. 50).
- Keep `replicaCount: 1` as-is — this just makes >1 SAFE; do not change the chart.

## Part B — Ready-made catalog

Pre-built automation templates a tenant can enable in one click (parity with openLEO
briefs/triage/reports — see `docs/openleo-competitive-analysis.md`).

- Define templates in code (NOT a DB seed): `internal/server/automation_catalog.go` —
  a slice of `{key, title, description, suggested_cron, prompt_template, skill_ref?}`.
  Seed 3: e.g. `daily-followup` (`0 9 * * 1-5`), `weekly-report` (`0 8 * * 1`),
  `lead-triage` (`@hourly`). Keep prompts generic/benign.
- API: `GET /api/automations/catalog` → returns the template list (no workspace scope
  needed; it's static). Register in `server.go`.
- UI (`WorkspaceAutomationsTab.tsx`): an "Add from catalog" affordance listing templates;
  clicking one opens the existing New-automation form **prefilled** (name, cron, prompt
  from the template) so the user just picks a channel + saves. Reuse the existing create flow.
- `web/src/lib/api.ts`: add `getAutomationCatalog()`.

## Constraints

- No force-push. No new top-level deps.
- Reuse PR1/PR2 DB + handlers + UI — extend, don't duplicate.
- Migration number is 047 (046 is latest). Don't renumber existing migrations.
- A claimed run that fails MUST still set `last_error` AND clear `locked_until` (no stuck locks).
- Build tag `goolm` in all Go commands; integration tests need `TEST_DATABASE_URL`.
- Run `make openapi` + `make api-docs` before pushing (CI drift checks WILL fail otherwise).
- Do NOT commit `web/dist/`. One PR for PR3.

## Tests

- `internal/db/automations_test.go` — `ClaimDueAutomations`: claims due rows, sets
  `locked_until`; a second concurrent claim returns nothing (rows locked); MarkAutomationRun
  clears the lock + advances next_run. (DB-gated on `TEST_DATABASE_URL`.)
- `internal/server/automation_handlers_test.go` — `GET /automations/catalog` returns the
  templates; "create from template" produces a valid automation (cron/prompt prefilled).
- Keep existing PR1/PR2 tests green.

## Next Action

Read `docs/productized-automations-spec.md` + `internal/db/automations.go` +
`internal/server/automation_scheduler.go`, then write migration `047_automation_lock.sql`
and `ClaimDueAutomations`.

## Done When

- `go build -tags goolm ./...` + `go vet -tags goolm ./...` pass.
- Scheduler uses `ClaimDueAutomations` (SKIP LOCKED + lease); MarkRun clears the lock.
- `GET /api/automations/catalog` returns ≥3 templates; UI lets a user enable one (prefilled form).
- `make openapi` + `make api-docs` run (no CI drift).
- New tests pass (claim concurrency + catalog); PR1/PR2 tests stay green.
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-29T00:00:00Z STARTED — orchestrator reseeded context for automations PR3
  (PR1 #134 + PR2 #138 + fix #139 merged; this adds multi-replica safety + ready-made catalog)
