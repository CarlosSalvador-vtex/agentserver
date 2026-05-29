# Cursor Agent Context

> Updated: 2026-05-29T00:00:00Z
> Branch: feat/automations-pr1
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **PR1 of productized automations** (proactive scheduled skills) as locked
by the eng review in `docs/productized-automations-spec.md`. PR1 = an in-process cron
scheduler + an `automations` table + ONE automation running end-to-end (cron fires →
synthesize a turn → reuse the imbridge run+deliver path → message lands in the channel).
Do NOT build the catalog, the management UI, or multi-replica safety — those are
explicitly deferred (see "NOT in scope (PR1)" in the spec).

## Source of truth

`docs/productized-automations-spec.md` — read these sections, in order:
1. **"Locked decisions (eng review 2026-05-29)"** — scope + architecture (authoritative)
2. **"Corrected flow (PR1)"** — the real flow (IGNORE "Proposed flow", marked superseded)
3. **"What already exists (reused, not rebuilt)"** — reuse, do not rebuild
4. **"Failure modes (PR1)"** table — this IS your test checklist
5. **"NOT in scope (PR1)"** — do not build these

## Files in Scope

Read first (the seams to reuse — do NOT duplicate their logic):
- `internal/server/codex_im_inbound.go` — `processTurn(ctx, codexInboundRequest)` ALREADY
  does run + deliver (resolve session → run turn → POST output to imbridge send → provider).
  The scheduler's "fire" = build a `codexInboundRequest{ChannelID, WorkspaceID, Text}` and
  call `processTurn` in-process (same binary). This is the run+deliver reuse the review
  locked — do NOT re-implement exec or delivery. Confirm exact struct fields here.
- `internal/server/playground_test_sandbox.go` → `StartPlaygroundReaper(ctx)` (~line 153)
  and `internal/server/playground_cm_reaper.go` → `StartConfigMapReaper(ctx)` — copy this
  in-process ticker pattern (ticker + `select { case <-ctx.Done(): return ... }`).
- `internal/db/im_channels.go` → `GetSandboxForChannel(channelID)` — how a channel resolves
  to a live sandbox (relevant to the no-live-sandbox fallback case).
- `cmd/serve.go` ~lines 340-349 — where `StartPlaygroundReaper`/`StartConfigMapReaper` are
  wired into `healthCtx`. Wire the new scheduler here the same way.

Create:
- `internal/db/migrations/046_automations.sql` — NEW. `automations` table:
  `id uuid pk, workspace_id uuid not null, name text, skill_ref text, cron text not null,
  channel_id text not null, config jsonb, enabled bool not null default true,
  last_run_at timestamptz, next_run_at timestamptz, last_error text,
  created_at timestamptz default now(), updated_at timestamptz default now()`.
  Add partial index: `CREATE INDEX ... ON automations (next_run_at) WHERE enabled;`
  (review decision, Issue 4).
- `internal/db/automations.go` — NEW. `ScanDue(ctx, now)` (`WHERE enabled AND next_run_at <= now()`),
  `ComputeNextRun(cron, from)` (robfig/cron/v3 parser — check go.mod, add only if missing),
  CRUD + `MarkRun(id, ranAt, nextRun, lastErr)`.
- `internal/server/automation_scheduler.go` — NEW.
  `(s *Server) StartAutomationScheduler(ctx)`: ~1min ticker → `ScanDue` → for each due row:
  build `codexInboundRequest` and call `processTurn`; on ANY error set `last_error` via
  `MarkRun` (never silent); always advance `next_run_at` via `ComputeNextRun`; a malformed
  cron skips that row without killing the loop.

Tests:
- `internal/db/automations_test.go` — `ScanDue` (due / none / db-error),
  `ComputeNextRun` (valid / malformed cron).
- `internal/server/automation_scheduler_test.go` — ONE integration test: cron fires → fire →
  fake-deliver, asserting delivery happened AND `last_run_at`/`last_error` set correctly.
  Reuse the fake patterns in `codex_im_inbound_test.go` (codexCaller / sessionStore fakes).

## Constraints

- No force-push. No new top-level deps beyond robfig/cron/v3 IF not already in go.mod.
- `replicaCount: 1` in dev → do NOT add `SELECT ... FOR UPDATE SKIP LOCKED` / leader election yet.
- Reuse `processTurn` for run+deliver — do NOT write a second exec/deliver path.
- A scheduled run that fails MUST set `automations.last_error` (critical-gap guard).
- Migration number is 046 (045 is latest existing). Do not renumber existing migrations.
- Build tag `goolm` in all Go commands; integration tests need `TEST_DATABASE_URL`.
- Do NOT commit `web/dist/`. Every change ships as a PR — open ONE PR for PR1.

## Next Action

Read the `docs/productized-automations-spec.md` sections listed above, then
`internal/server/codex_im_inbound.go` (`processTurn` + `codexInboundRequest` struct) to
confirm exact field names, then write migration `046_automations.sql`.

## Done When

- `go build -tags goolm ./...` and `go vet -tags goolm ./...` pass.
- `automations` table migration applies cleanly; partial index present.
- `StartAutomationScheduler` wired in `cmd/serve.go` next to the other reapers.
- Unit tests (ScanDue, ComputeNextRun) + ONE integration test (fire→deliver, asserts
  delivery + last_run/last_error) pass.
- A scheduled run reuses `processTurn` (no duplicated exec/deliver code).
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + add PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-29T00:00:00Z STARTED — orchestrator reseeded context for automations PR1
  (prior task B10/PR #117 closed out)
