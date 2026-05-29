# Cursor Agent Context

> Updated: 2026-05-29T00:00:00Z
> Branch: feat/automations-pr2
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **PR2 of productized automations** — the management surface on top of the
PR1 scheduler (already merged in `main`: `automations` table, in-process scheduler,
run via `processTurn`). PR2 = **CRUD API + management UI** so a tenant can create,
list, enable/disable, edit, and delete scheduled automations, and see last-run /
next-run / last-error.

**DO NOT** build (deferred to PR3): the ready-made catalog of pre-built automations,
and multi-replica safety (`SELECT ... FOR UPDATE SKIP LOCKED` / leader election —
`replicaCount` is still 1).

## Source of truth

`docs/productized-automations-spec.md` — read "Locked decisions (eng review)",
"Corrected flow (PR1)", and "NOT in scope (PR1)". PR2 builds the API+UI that PR1
deferred. The `automations` table + scheduler already exist — reuse them.

## What already exists (PR1 — reuse, do NOT rebuild)

- `internal/db/automations.go`: `Automation` struct, `CreateAutomation`,
  `GetAutomation`, `DeleteAutomation`, `ScanDueAutomations`, `MarkAutomationRun`,
  `ComputeNextRun` (robfig/cron/v3, supports `@every`/`@daily`/5-field).
- `internal/server/automation_scheduler.go`: `StartAutomationScheduler` ticker.
- Migration `046_automations.sql`: table + partial index `(next_run_at) WHERE enabled`.
- Automation `Config` is JSONB; the scheduler reads `{channel_id, workspace_id,
  wechat_user_id, prompt}` from it to fire a turn.

## Files in Scope

DB layer — ADD to `internal/db/automations.go`:
- `ListAutomations(ctx, workspaceID string) ([]Automation, error)` — `WHERE workspace_id = $1 ORDER BY created_at`.
- `UpdateAutomation(ctx, a *Automation) error` — update name, skill_ref, cron, channel_id,
  config, enabled; recompute `next_run_at` via `ComputeNextRun` when cron or enabled changes
  (enabling a row with NULL next_run must set it; disabling may leave it). `updated_at = NOW()`.

API handlers — NEW `internal/server/automation_handlers.go`:
- `POST   /api/workspaces/{id}/automations`        → create (validate cron via ComputeNextRun; set next_run_at; 400 on bad cron)
- `GET    /api/workspaces/{id}/automations`        → list
- `GET    /api/workspaces/{id}/automations/{aid}`  → get one
- `PATCH  /api/workspaces/{id}/automations/{aid}`  → update (enable toggle, cron, config, name)
- `DELETE /api/workspaces/{id}/automations/{aid}`  → delete
- Guard every handler with `s.requireWorkspaceRole(w, r, workspaceID, "owner", "maintainer", "developer")`
  (see `internal/server/server.go:1184` + existing members/invites handlers for the exact pattern).
- Validate the `channel_id` belongs to the workspace (reuse `workspace_im_channels` lookup).
- Register routes in `internal/server/server.go` next to the other `/api/workspaces/{id}/...`
  routes (~line 500-513).

Frontend — NEW `web/src/components/Automations.tsx` (+ wire route + sidebar):
- Route `/automations` in `web/src/App.tsx` (mirror the `/playground` route + the
  workspace-sidebar entry added in #135 — see `web/src/components/TopBar.tsx` / sidebar).
- List automations: name, cron, channel, enabled toggle, last_run / next_run / last_error badge.
- Create/edit form: name, cron (text + hint, e.g. `0 8 * * 1-5` or `@daily`), channel picker
  (workspace IM channels), skill_ref (optional), config JSON (the `{channel_id, workspace_id,
  wechat_user_id, prompt}` payload — prefill channel_id/workspace_id from selection).
- Enable/disable toggle calls PATCH. Delete with confirm.
- API client methods in `web/src/lib/api.ts` (mirror existing workspace CRUD calls).

OpenAPI + docs:
- After adding handlers, run `make openapi` and `make api-docs` (CI has drift checks that
  WILL fail otherwise — this bit us before).

Tests — NEW `internal/server/automation_handlers_test.go`:
- create / list / get / patch(enable toggle) / delete happy paths.
- 403 when caller lacks workspace role.
- 400 on malformed cron at create.
- channel not in workspace → rejected.
- Reuse fake patterns from `codex_im_inbound_test.go` / existing handler tests;
  DB-backed tests gate on `TEST_DATABASE_URL` (skip if unset).

## Constraints

- No force-push. No new top-level deps.
- Reuse PR1's DB layer + scheduler — do NOT touch the scheduler logic or `processTurn`.
- `replicaCount: 1` → NO SKIP LOCKED / leader election (PR3).
- NO ready-made catalog seed (PR3).
- Build tag `goolm` in all Go commands; integration tests need `TEST_DATABASE_URL`.
- Run `make openapi` + `make api-docs` before pushing (CI drift checks).
- Do NOT commit `web/dist/`. Every change ships as a PR — open ONE PR for PR2.

## Next Action

Read `docs/productized-automations-spec.md` (locked-decisions + NOT-in-scope) and
`internal/db/automations.go`, then add `ListAutomations` + `UpdateAutomation` to the DB layer.

## Done When

- `go build -tags goolm ./...` + `go vet -tags goolm ./...` pass.
- CRUD endpoints work, all guarded by workspace role; bad cron → 400; foreign channel → rejected.
- `/automations` UI lists + creates + toggles + edits + deletes; shows last_run/next_run/last_error.
- `make openapi` + `make api-docs` run (no CI drift).
- Handler tests pass (CRUD + 403 + 400 + foreign-channel).
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-29T00:00:00Z STARTED — orchestrator reseeded context for automations PR2
  (PR1 #134 merged; this builds the CRUD API + management UI on top)
