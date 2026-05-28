# Documentation Organization Backlog

> **Created:** 2026-05-28
> **Sources:** full audit of `docs/` tree, git log PRs #57–#83, `improvements.md` (complete), `playground-marketplace-v2-backlog.md`
> **Goal:** Bring docs into sync with code reality. Every shipped feature has a doc that reflects it; every pending backlog item is clearly labeled; orphaned / superseded docs are retired or archived.

---

## Baseline — current docs state

The doc tree has grown across 5 sprints without a consolidation pass. Common problems:

| Pattern | Examples |
|---------|----------|
| **Shipped but unmarked** | `cursor-handoffs/README.md` still shows B01/B07 as pending — both merged (PRs #71, #72) |
| **Superseded but alive** | `workspace-auth-pendencies.md` lists F1-F6 cleanup items some of which are done |
| **Feature exists, no API ref** | Playground/marketplace routes have no entry in `docs/api/reference/` |
| **Ops scripts undocumented** | `scripts/deploy-dev.sh` and `scripts/apply-seed-cobrana.sh` exist but no runbook links them |
| **Stale backlog as canonical** | `playground-marketplace-v2-backlog.md` Tier A all shipped; doc still reads as future work |
| **Reference material unlabeled** | `docs/superpowers/` (50+ upstream plans/specs) has no README explaining provenance |
| **Multiple docs, one topic** | workspace-auth spread across 6 docs: `workspace-auth.md`, `workspace-auth-design.md`, `workspace-auth-pendencies.md`, `workspace-session-auth.md`, `pr-57-workspace-subdomain-auth-status.md`, `plans/cursor_workspace-subdomain-auth.md` |

---

## North-star

A new contributor can:
1. Read one doc and understand how to run the dev cluster.
2. Find all open backlog items in one place, with ship status.
3. Find the API surface for any feature in `docs/api/reference/`.
4. Know immediately which `docs/superpowers/` plans are upstream reference vs. internal roadmap.

---

## Tier A — quick wins, high signal (≤ 30 min each)

| ID | Title | Problem | Action | Est. (human / CC) |
|----|-------|---------|--------|-------------------|
| **A1** | Mark B01 + B07 shipped in cursor-handoffs | `cursor-handoffs/README.md` shows all 10 as pending; B01 (invites, PR #71) and B07 (workspace audit, PR #72) are merged | Add ✅ column to README table; mark B01 + B07 with PR # | 10 min / ~1 min |
| **A2** | Update `playground-marketplace-v2-backlog.md` baseline | Tier A (A1–A6) all shipped in PRs #64–#70; doc reads as future work | Promote entire Tier A to "Baseline — what already ships" table | 15 min / ~2 min |
| **A3** | Archive F1–F6 in `workspace-auth-pendencies.md` | F1 (PR #56 merge) done; F2 (design doc update) done post PR #57; F3 (branch cleanup) done | Mark each F-item with ✅ + PR # or "resolved"; move resolved block under `## Resolved` | 20 min / ~3 min |
| **A4** | Add `docs/superpowers/README.md` | 50+ upstream plans/specs with no context; new contributors confuse them for actionable internal roadmap | One-page README: "These are architecture plans from the upstream Superpowers AI project. They describe the fork parent's roadmap. Not all items are in scope for this fork." | 15 min / ~2 min |
| **A5** | Update `improvements.md` index table | Index still shows #18 as "shipped" inline note but migration note is only in the header — inconsistent style | Add ✅ column to index table for all 20 items; remove inline "**shipped**" markers | 10 min / ~1 min |

**Suggested order:** A1 → A2 → A3 → A5 → A4

---

## Tier B — medium effort, high value

| ID | Title | Problem | Action | Est. (human / CC) | PR shape |
|----|-------|---------|--------|-------------------|----------|
| **B1** | `docs/api/reference/playground.md` | Playground + marketplace API routes exist in `playground_handlers.go` + `playground_marketplace.go` but have zero entries in `docs/api/reference/` | Document all `/api/playground/*` and `/api/marketplace/*` routes: method, path, auth, body, response, error codes. Mirror format of `docs/api/reference/sandboxes.md` | ~3 h / ~15 min | `docs: playground and marketplace API reference` |
| **B2** | `docs/ops/runbook.md` — deploy + seed | `scripts/deploy-dev.sh` and `scripts/apply-seed-cobrana.sh` are undocumented; `docs/dev-eks-deploy.md` was written before these scripts existed | New runbook: prerequisites, step-by-step for first deploy, re-deploy, seed templates, rollback. Reference the scripts. Note node taint workaround (internal-workers toleration). | ~1.5 h / ~8 min | `docs: ops runbook for dev cluster deploy and seed` |
| **B3** | Consolidate workspace-auth into 2 docs | 6 workspace-auth docs with overlapping content. New contributors hit 6 cross-links before understanding the feature. | Keep: (1) `workspace-auth.md` → canonical design (Options A/B/C, implementation). (2) New `workspace-auth-ops.md` → open pendencies, staging checklist, cursor handoffs index. Archive or redirect the 4 PR-status docs under `docs/archive/`. | ~2 h / ~10 min | `docs: consolidate workspace-auth — canonical + ops` |
| **B4** | `docs/ops/cobranca-admin-setup.md` | Steps 1–4 of cobrança wedge are code; Step 5 (admin: create workspace, invite sister, set quota, bind WhatsApp) is undocumented | Writeup for operators: (a) create workspace via admin panel, (b) set maxSandboxes ≥ 1, (c) invite sister's email, (d) WhatsApp BSP phone_number_id + access_token via API, (e) bind sandbox to channel | ~1 h / ~5 min | `docs: cobrança wedge — admin setup guide` |
| **B5** | Update cursor-handoffs for remaining B02–B10 | B02 (delete workspace), B03 (delete user/LGPD), B06 (email subdomain URLs — B01 shipped, unblocked), B09 (choose workspace apex), B10 (reserved slugs) are all still open — but status column is missing from handoff docs | Add `## Status` section to each open handoff: `OPEN — not started`, dependencies, estimated PR size. For B06: note B01 shipped, mark B06 as unblocked. | ~45 min / ~5 min | `docs: cursor-handoffs status refresh B02–B10` |
| **B6** | `saas-multitenancy-roadmap.md` — close shipped gaps | Roadmap lists gaps; many were closed in PRs #43 (audit log), #64–#70 (marketplace), #71 (invites), #72 (workspace audit), #73–#74 (playground tier B). Roadmap doesn't reflect this. | Add a `## Closed gaps` section mapping each closed item to the PR that closed it. Update `## Remaining gaps` to only show what's still open. | ~45 min / ~5 min | `docs: saas-multitenancy-roadmap — mark closed gaps` |

---

## Tier C — lower priority, longer payoff

| ID | Title | Problem | Action | Est. (human / CC) |
|----|-------|---------|--------|-------------------|
| **C1** | `docs/getting-started.md` — local dev | No single doc explains how to run agentserver locally from a fresh checkout. Dev onboarding requires reading 3 READMEs + `Makefile`. | New doc: clone, `make dev`, env vars, seed DB, test with curl. Covers Go backend + React frontend + Docker Compose alternative. | ~2 h / ~10 min |
| **C2** | OpenAPI — playground + marketplace routes | `docs/api/openapi.yaml` was auto-generated pre-playground. Routes from PR #32+ are missing. | Run `make openapi:gen` (or equivalent), verify playground + marketplace routes appear, commit updated spec. Add CI check: `make openapi:gen && git diff --exit-code docs/api/openapi.yaml`. | ~1 h / ~5 min |
| **C3** | ADR (Architecture Decision Records) for key decisions | Decisions locked in eng specs (D1–D6 in cobrana-wedge-eng-spec.md, similar in other specs) are buried in feature docs. Hard to find reasoning post-merge. | Create `docs/decisions/` with ADR-lite format. Seed with 5 decisions: (1) delete-then-create deploy, (2) isDevMode via role, (3) fork-self constraint (D2), (4) internal-workers toleration pattern, (5) shared visibility vs. workspace-scoped marketplace. | ~2 h / ~10 min |
| **C4** | Archive old plan docs | `docs/plans/2026-03-05-*`, `docs/plans/2026-03-09-*`, `docs/plans/2026-03-10-*` are pre-implementation plans that were superseded by implementation. They clutter `ls docs/`. | Move to `docs/archive/plans/` with an index. No content change — just relocation + one-line redirect comment at old path. | ~30 min / ~3 min |
| **C5** | `docs/lessons-learned.md` — add post-Sprint 4/5 entries | Last entry is pre-playground sprints. Lessons from: plugin-sdk symlink (initContainer path `/app`), ConfigMap `/` key restriction, node taint toleration pattern, `where not exists` idempotent seed pattern — none captured. | Add 4–5 rows to the table for Sprint 4/5 lessons. | ~30 min / ~3 min |

---

## Explicitly out of scope

- Rewriting the `docs/superpowers/` plans/specs — they are upstream material, reference only.
- Generating full SDK docs — tracked in C2 (OpenAPI); SDK codegen is a future item.
- Migrating docs to a doc site (GitBook, Docusaurus) — product decision, not in this sprint.

---

## Suggested 1-week sprint (docs cleanup)

**Day 1 — quick pass**
- A1, A2, A3, A5, A4 (all under 20 min each — can batch in 1 PR)

**Day 2 — ops and API**
- B2 Ops runbook + B4 Cobrança admin setup → one PR `docs: ops guides`
- B1 Playground API reference → PR `docs: playground + marketplace API reference`

**Day 3 — consolidation**
- B3 Workspace-auth consolidation
- B5 Cursor-handoffs status refresh
- B6 Multitenancy roadmap gaps

**Day 4 — lower priority**
- C5 Lessons learned (quick)
- C1 Getting started (if time)

**Exit criteria**
- `git ls-files docs/ | wc -l` same or fewer (no new orphans).
- Every shipped feature has ≥ 1 API reference entry.
- `cursor-handoffs/README.md` shows correct ship status for all 10 items.
- One ops runbook covers deploy → seed → rollback end-to-end.

---

## Quick reference — affected files

| File / Dir | Action |
|-----------|--------|
| `docs/cursor-handoffs/README.md` | Mark B01 ✅ PR #71, B07 ✅ PR #72 (A1) |
| `docs/playground-marketplace-v2-backlog.md` | Promote Tier A to baseline (A2) |
| `docs/workspace-auth-pendencies.md` | Archive F1–F6 resolved items (A3) |
| `docs/improvements.md` | Add ✅ column to index table (A5) |
| `docs/superpowers/README.md` | Create — upstream provenance note (A4) |
| `docs/api/reference/playground.md` | Create — full playground + marketplace API (B1) |
| `docs/ops/runbook.md` | Create — deploy, seed, rollback (B2) |
| `docs/ops/cobranca-admin-setup.md` | Create — admin setup guide (B4) |
| `docs/workspace-auth.md` | Keep canonical; absorb pendencies (B3) |
| `docs/workspace-auth-pendencies.md` | Convert to ops guide (B3) |
| `docs/archive/` | Move: 4 pr-status workspace-auth docs + old plans (B3, C4) |
| `docs/cursor-handoffs/B*.md` | Add `## Status` to open items (B5) |
| `docs/saas-multitenancy-roadmap.md` | Mark closed gaps with PR refs (B6) |
| `docs/decisions/` | Create — 5 ADR-lite entries (C3) |
| `docs/lessons-learned.md` | Add Sprint 4/5 rows (C5) |
| `docs/getting-started.md` | Create — local dev guide (C1) |
| `docs/api/openapi.yaml` | Regen + CI drift check (C2) |
