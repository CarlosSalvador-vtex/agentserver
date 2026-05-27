# Playground + Marketplace v2 — Mini Backlog

> **Created:** 2026-05-27  
> **Sources:** `docs/playground-design.md` (§5, §14), `docs/improvements.md` (#6–#10, #14, #17–#18, Tier 5), codebase audit  
> **Goal:** Prioritized next steps to improve author UX (playground) and discovery/sharing (marketplace) after MVP + Sprint 5.

---

## Baseline — what already ships

Use this table before picking work; several `improvements.md` items are **done** but still read like backlog in the design doc body.

| Area | Status | Where |
|------|--------|--------|
| Draft CRUD + promote + dry-run (skill) | Shipped | `playground_handlers.go`, `PlaygroundSkillEditor.tsx` |
| Soul dry-run | Shipped | `handleSoulDraftDryRun`, `PlaygroundSoulEditor.tsx` |
| Diff vs last promoted (skill) | Shipped | `PromotedDiff.tsx`, skill editor tab |
| Promote PR state polling | Shipped | `playground_promote_poll.go`, `promoted_pr_state` column |
| Draft audit log | Shipped | migration 034, `DraftAuditTimeline` (skill editor only) |
| Tenant-scoped catalog | Shipped | `workspace_id` on drafts, scope filter in `Playground.tsx` |
| Marketplace list + fork | Shipped | `playground_marketplace.go`, `Marketplace.tsx`, migration 036 |
| Admin visibility API | Shipped (API only) | `PATCH /api/admin/playground/{skills,souls}/{id}/visibility` |
| Playground Prometheus metrics | Shipped | `playground_metrics.go`, `deploy/grafana/playground-dashboard.json` |
| Composition picker in sandbox create | Shipped | Tier 1 #1 |

**Thin surfaces today:** marketplace is a flat list + Fork; playground editors use `<textarea>`; soul editor lacks diff/audit parity with skill editor; no in-app admin moderation UI.

---

## North-star outcomes (v2)

1. **Authors iterate faster** — preview persona/skill, see what changed since promote, test in sandbox without context switching.
2. **Tenants discover reuse** — find shared templates, trust provenance, fork into workspace in one flow.
3. **Operators stay safe** — visibility/moderation and audit visible in UI, metrics drive what to build next.

---

## Tier A — high impact, low–medium cost

| ID | Title | Problem | Proposal | Est. | PR shape |
|----|-------|---------|------------|------|----------|
| **A1** | Marketplace → editor handoff | After fork, user only sees a toast; no link to the new draft | Return forked draft `id` from API (if not already); redirect or CTA "Open in Playground" | ~40 LOC | `feat(ui): marketplace fork opens draft editor` |
| **A2** | Share to marketplace (author UI) | Visibility toggle is admin-only API; authors can't opt-in to `shared` | Workspace `owner`/`maintainer` can set `private` ↔ `shared` on own drafts; admin retains override + revoke | ~80 BE + ~60 FE | `feat(playground): author-controlled marketplace visibility` |
| **A3** | Soul editor parity | Skill has Diff + Audit tabs; soul doesn't | Reuse `PromotedDiff` (body/frontmatter) + `DraftAuditTimeline` on `PlaygroundSoulEditor.tsx` | ~60 FE | `feat(ui): soul editor diff and audit tabs` |
| **A4** | Marketplace metadata | List shows name/description/date only | Expose `author_workspace_id`, `updated_at`, optional `compatible_skills` / tags in API + cards ("from workspace X", "forked N times" later) | ~40 BE + ~50 FE | `feat(marketplace): richer listing metadata` |
| **A5** | Search / filter marketplace | Unusable at >20 entries | Client-side search by name/description; tabs Skills/Souls already exist — add sort (updated, name) | ~50 FE | `feat(ui): marketplace search and sort` |
| **A6** | Dry-run model picker | Single model from workspace default | Optional `model` in dry-run body (improvements.md Tier 5); dropdown in skill + soul test panels | ~40 BE + ~40 FE | `feat(playground): multi-model dry-run picker` |

**Suggested order:** A1 → A2 → A3 → A4 → A5 → A6

---

## Tier B — author experience

| ID | Title | Problem | Proposal | Est. | Ref |
|----|-------|---------|------------|------|-----|
| **B1** | Monaco / syntax highlighting | Large skill files in plain textarea | Monaco or CodeMirror for `index.mjs`, `prompt.md`, soul body (improvements.md Tier 5) | ~150 FE | bundle ~50 KB |
| **B2** | Marketplace preview (read-only) | Fork blind — no sample prompt or soul excerpt | Detail drawer/modal: description, first N lines of soul body or skill `prompt.md` (redact secrets via lint) | ~100 FE + ~40 BE | new `GET /api/marketplace/skills/{id}/preview` |
| **B3** | Test sandbox from editor | Dry-run ≠ real OpenClaw/Hermes tools | Prominent "Open test sandbox" in skill editor wired to existing test-sandbox endpoint; show pod status + link | ~80 FE | `playground-design.md` §5 |
| **B4** | Hot-reload test sandbox | Save draft → manually recreate sandbox | On save, if test sandbox attached, trigger rolling restart or composition refresh hook | ~80 FE + ~80 BE | improvements.md Tier 5 |
| **B5** | Promote feedback loop | PR state exists but soul editor may not surface banner | Unify promote banner component (open/merged/closed) on **both** editors | ~40 FE | #8 shipped backend |
| **B6** | Workspace picker on create | `POST` drafts default to first workspace silently | Require explicit workspace on create when user has >1 workspace | ~50 FE | complements #17 |

---

## Tier C — marketplace growth & trust

| ID | Title | Problem | Proposal | Est. | Ref |
|----|-------|---------|------------|------|-----|
| **C1** | Ratings / helpful votes | No signal on shared templates | `marketplace_votes` table; `:+1:` per user per draft; sort by score (improvements.md Tier 5) | ~100 LOC | `playground-design.md` §14 |
| **C2** | Admin moderation UI | Admins use curl for visibility | Admin page: list `shared`, revoke to `private`, audit who shared | ~120 FE | extends A2 |
| **C3** | Fork attribution | Fork copies lose lineage | Store `forked_from_id` on draft; show "Fork of cobranca (ws-…)" in playground | ~60 BE + ~30 FE | optional migration |
| **C4** | Featured / curated row | Marketplace is flat | Admin `featured` flag or static "VTEX templates" section at top | ~80 LOC | product decision |
| **C5** | Semver on promote | Version bumps manual | `version` in frontmatter; promote suggests patch bump (improvements.md Tier 5) | ~60 LOC | |

---

## Tier D — platform & ops (supports v2)

| ID | Title | Problem | Proposal | Est. | Ref |
|----|-------|---------|------------|------|-----|
| **D1** | Per-workspace metrics dashboard | Global playground dashboard only | Grafana dashboard variables on `workspace_id` label (if emitted) or fork #6 dashboard | ~30 JSON | improvements.md Tier 5 |
| **D2** | Composition versioning | Edit composition = recreate sandbox | Migration helpers or "apply new draft to running sandbox" (design §14 v2) | large | defer until A/B/C stable |
| **D3** | OpenAPI + CI for playground routes | Hand-maintained `api.ts` | Ensure `make openapi` covers marketplace + playground; CI drift check | ~30 LOC | improvements.md Tier 5 |

---

## Explicitly out of scope (for now)

From `playground-design.md` §14 and `improvements.md` "deliberately leave out":

- Custom OpenClaw image per tenant (use initContainer symlink #16 — **done**)
- Behavior-tree YAML souls
- Soul as workspace-only metadata (breaks composition model)
- A/B persona routing at platform level
- Full public internet marketplace (stay authenticated, `shared` among tenants)

---

## Suggested 2-week sprint (playground + marketplace v2)

**Week 1 — discovery & sharing**

1. A1 Marketplace fork handoff  
2. A2 Author share toggle (+ C2 admin page if same PR)  
3. A4 + A5 Marketplace metadata + search  

**Week 2 — author loop**

4. A3 Soul editor parity (diff + audit)  
5. B5 Unified promote banners  
6. A6 or B3 Dry-run model picker **or** test-sandbox CTA (pick one based on user interviews)

**Exit criteria**

- Author can share a draft to marketplace without admin curl.  
- Fork → open editor in ≤2 clicks.  
- Soul and skill editors have equivalent promote/audit/diff story.  
- Marketplace usable with 50+ entries (search + sort).

---

## Quick reference — source docs

| Document | Use for |
|----------|---------|
| [`playground-design.md`](./playground-design.md) | Concepts, API surface, marketplace §5.1a, v2 §14 |
| [`improvements.md`](./improvements.md) | Historical tier items #6–#10, #14, #17–#18; Tier 5 nice-to-haves |
| [`lessons-learned.md`](./lessons-learned.md) | Plugin loader, composition race, deploy pitfalls |

## Code entry points

| Layer | Path |
|-------|------|
| Playground UI | `web/src/components/Playground*.tsx`, `PromotedDiff.tsx`, `DraftAuditTimeline.tsx` |
| Marketplace UI | `web/src/components/Marketplace.tsx` |
| API | `internal/server/playground_*.go`, `playground_marketplace.go` |
| DB | `internal/db/playground.go`, migrations `032`, `034`, `036` |
| Metrics | `internal/server/playground_metrics.go`, `deploy/grafana/playground-dashboard.json` |
