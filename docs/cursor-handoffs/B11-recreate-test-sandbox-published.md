# B11 — "Recreate test sandbox" 404s on a published skill

> Bug handoff. Surfaced 2026-05-29 while verifying #129 (OpenClaw clean boot).
> GitHub Issues are disabled on this fork, so the backlog lives in `docs/cursor-handoffs/`.

## Summary

Clicking **Recreate test sandbox** / **Open test sandbox** on a **published** skill
returns HTTP 404. The test-sandbox panel shows `HTTP 404` and no new sandbox spawns.
Works fine on a **draft** skill.

## Repro

1. Open a **published** skill in the playground (`/playground/skills/<id>` — header shows `(published)`).
2. Click **Recreate test sandbox**.
3. Panel shows `HTTP 404`; no fresh sandbox is provisioned.

(Confirmed working: create a fresh **draft**, click Open test sandbox → spawns clean —
verified pod `agent-sandbox-80a411f6` provisioned this way.)

## Root cause

`POST /api/playground/skills/{id}/test-sandbox` → `handleSkillDraftTestSandbox`
(`internal/server/playground_test_sandbox.go:55`) calls `s.DB.GetSkillDraft(id)` (line 59).
`GetSkillDraft` queries `skill_drafts WHERE id = $1` (`internal/db/playground.go:81`) with
no status filter — but the `{id}` for a **published** skill is the published skill id,
which is not a row in `skill_drafts`. Result: `(nil, nil)` → handler returns
`404 "draft not found"` (lines 60-62).

Server log confirms: `POST .../api/playground/skills/<published-id>/test-sandbox ... - 404 16B`.

Likely a side effect of the publish-without-git flow (draft id → published id divergence).

## Options (pick one in impl)

- **(a)** Resolve the published skill to its backing draft/composition and spawn from that.
- **(b)** Hide/disable "Recreate test sandbox" when the skill is published, with a clear
  message ("publish a draft to test, or open the draft").
- **(c)** Accept a `published:<id>` composition ref in the test-sandbox handler
  (mirror the `draft:`/`git:` ref pattern in composition resolution).

Recommendation: (a) if a published skill still has a resolvable composition; otherwise (b)
as the minimal correct UX.

## Acceptance

- Test-sandbox on a published skill either spawns a working sandbox (a/c) or the button is
  clearly unavailable with guidance (b) — no silent 404.
- Draft test-sandbox path unchanged (regression-guarded).
- Unit/integration test covering the published-skill test-sandbox path.

## Scope note

Separate from #129 (OpenClaw clean boot, verified). Found during that verification.
