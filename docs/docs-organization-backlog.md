# Documentation Organization Backlog

> **Created:** 2026-05-28  
> **Revised:** 2026-05-28 — applied gstack mindset review (Boil the Lake, decisions surfaced, critical path, open concerns)  
> **Sources:** full audit of `docs/` tree, git log PRs #57–#85, `improvements.md` (complete), `playground-marketplace-v2-backlog.md`  
> **Goal:** Bring docs into sync with code reality. Every shipped feature has a doc that reflects it; every pending backlog item is clearly labeled; orphaned / superseded docs are retired or archived.

---

## Baseline — current docs state

The doc tree has grown across 5 sprints without a consolidation pass. Common problems:

| Pattern | Examples |
|---------|----------|
| **Shipped but unmarked** | `cursor-handoffs/README.md` still shows B01/B07 as pending — both merged (PRs #71, #72) |
| **Superseded but alive** | `workspace-auth-pendencies.md` lists F1-F6 cleanup items some of which are done |
| **Feature exists, no API ref** | Playground/marketplace routes have zero entries in `docs/api/reference/` |
| **Ops scripts undocumented** | `scripts/deploy-dev.sh` and `scripts/apply-seed-cobrana.sh` exist but no runbook links them |
| **Stale backlog as canonical** | `playground-marketplace-v2-backlog.md` Tier A all shipped; doc still reads as future work |
| **Reference material unlabeled** | `docs/superpowers/` (50+ upstream plans/specs) has no README explaining provenance |
| **Multiple docs, one topic** | workspace-auth spread across 6 docs — all with overlapping content |

---

## North-star

A new contributor can:
1. Read one doc and understand how to run the dev cluster.
2. Find all open backlog items in one place, with ship status.
3. Find the API surface for any feature in `docs/api/reference/`.
4. Know immediately which `docs/superpowers/` plans are upstream reference vs. internal roadmap.

---

## Decisions Required Before Work Starts

These are implicit choices inside the backlog items. Lock them before executing or the implementation will drift.

| # | Question | Options | Recommendation |
|---|----------|---------|----------------|
| **DD1** | workspace-auth: merge to 1 doc or 2? | A) 1 canonical doc — design + pendencies + ops in sections. B) 2 docs — canonical design + separate ops guide. | **A** — Boil the Lake; with CC the cost of merging 6→1 is the same as 6→2. Single source of truth avoids the exact problem we're fixing. |
| **DD2** | B1 playground API coverage: all routes or only new ones? | A) All `/api/playground/*` + `/api/marketplace/*` routes (complete). B) Only routes added post-PR #32 (missing from existing ref). | **A** — partial coverage creates a false sense of completeness; any consumer checking the doc will hit missing routes. |
| **DD3** | C3 ADRs: new `docs/decisions/` dir or `## Decisions Locked` sections in existing docs? | A) New `docs/decisions/` directory with ADR-lite files. B) Add `## Decisions Locked` to existing canonical feature docs (same format as eng specs). | **B** — decisions already live in eng specs; a separate dir creates a 3rd place to check. Append to existing docs. |
| **DD4** | OpenAPI regen: manual or CI-gated? | A) Regen once, commit, no CI check. B) Regen + add CI step: `make openapi:gen && git diff --exit-code`. | **B** — without CI gate the spec drifts again within 2 PRs. |

---

## Tier A0 — prerequisite (do first, unblocks B1)

| ID | Title | Problem | Action | Est. (human / CC) | PR shape |
|----|-------|---------|--------|-------------------|----------|
| **A0** | OpenAPI regen + CI drift check | `docs/api/openapi.yaml` is pre-playground (pre-PR #32). Any API reference doc written against a stale spec is wrong before it ships. | Run `make openapi:gen`, commit updated spec, add CI check `git diff --exit-code docs/api/openapi.yaml`. Locks DD4-B. | ~1 h / ~5 min | `chore: regen OpenAPI + add CI drift check` |

---

## Tier A — quick wins, high signal (≤ 30 min each)

All five can batch into one PR. Suggested order within: A1 → A2 → A3 → A5 → A4.

| ID | Title | Problem | Action | Est. (human / CC) |
|----|-------|---------|--------|-------------------|
| **A1** | Mark B01 + B07 shipped in cursor-handoffs | `cursor-handoffs/README.md` shows all 10 as pending; B01 (invites, PR #71) and B07 (workspace audit, PR #72) are merged | Add ✅ column to README table; mark B01 + B07 with PR # | 10 min / ~1 min |
| **A2** | Update `playground-marketplace-v2-backlog.md` baseline | Tier A (A1–A6) all shipped in PRs #64–#70; doc reads as future work | Promote entire Tier A to "Baseline — what already ships" table | 15 min / ~2 min |
| **A3** | Archive F1–F6 in `workspace-auth-pendencies.md` | F1 (PR #56 merge) done; F2 (design doc update) done post PR #57; F3 (branch cleanup) done | Mark each F-item with ✅ + PR # or "resolved"; move resolved block under `## Resolved` | 20 min / ~3 min |
| **A4** | Add `docs/superpowers/README.md` | 50+ upstream plans/specs with no context; new contributors confuse them for actionable internal roadmap | One-page README: "These are architecture plans from the upstream Superpowers AI project. Not all items are in scope for this fork." | 15 min / ~2 min |
| **A5** | Update `improvements.md` index table | Index shows #18 as "shipped" inline note but inconsistent with header style | Add ✅ column to index table for all 20 items; remove inline `**shipped**` markers | 10 min / ~1 min |

---

## Tier B — medium effort, high value

| ID | Title | Problem | Action | Est. (human / CC) | PR shape |
|----|-------|---------|--------|-------------------|----------|
| **B1** | `docs/api/reference/playground.md` | Playground + marketplace API routes exist in `playground_handlers.go` + `playground_marketplace.go` but have zero entries in `docs/api/reference/`. Requires A0 first (spec must be current). | Document **all** `/api/playground/*` and `/api/marketplace/*` routes (DD2-A): method, path, auth, body, response, error codes. Mirror format of `docs/api/reference/sandboxes.md`. | ~3 h / ~15 min | `docs: playground and marketplace API reference` |
| **B2** | `docs/ops/runbook.md` — deploy + seed | `scripts/deploy-dev.sh` and `scripts/apply-seed-cobrana.sh` are undocumented; `docs/dev-eks-deploy.md` was written before these scripts existed | New runbook: prerequisites, first deploy, re-deploy, seed templates, rollback. Note: internal-workers toleration required (PR #83). | ~1.5 h / ~8 min | `docs: ops runbook for dev cluster deploy and seed` |
| **B3** | Collapse 6 workspace-auth docs → 1 | 6 overlapping docs create navigation hell. New contributors follow 6 cross-links before understanding the feature. With CC the merge cost is the same as keeping 2. (DD1-A) | Merge into single `workspace-auth.md`: keep design section (Options A/B/C, decision), add implementation section (post-PR #57/58), add pendencies section (P1–P4 blockers + open B02-B10). Archive the 5 redundant docs to `docs/archive/`. | ~2 h / ~10 min | `docs: collapse workspace-auth into single canonical doc` |
| **B4** | `docs/ops/cobranca-admin-setup.md` | Steps 1–4 of cobrança wedge are code; Step 5 (admin: create workspace, invite, quota, WhatsApp) is undocumented | Writeup for operators: (a) create workspace via admin panel, (b) set maxSandboxes ≥ 1, (c) invite email, (d) WhatsApp BSP phone_number_id + access_token via API, (e) bind sandbox to channel | ~1 h / ~5 min | `docs: cobrança wedge — admin setup guide` |
| **B5** | Update cursor-handoffs for remaining B02–B10 | B02 (delete workspace), B03 (delete user/LGPD), B06 (email subdomain URLs — B01 shipped, unblocked), B09 (choose workspace apex), B10 (reserved slugs) open but no status markers | Add `## Status` section to each open handoff: `OPEN — not started`, dependencies, estimated PR size. For B06: note B01 shipped, mark as unblocked. | ~45 min / ~5 min | `docs: cursor-handoffs status refresh B02–B10` |
| **B6** | `saas-multitenancy-roadmap.md` — close shipped gaps | Roadmap lists gaps; many closed in PRs #43, #64–#74. Doc doesn't reflect this. | Add `## Closed gaps` section mapping each item to its PR. Update `## Remaining gaps` to only show what's open. | ~45 min / ~5 min | `docs: saas-multitenancy-roadmap — mark closed gaps` |

---

## Tier C — lower priority, longer payoff

| ID | Title | Problem | Action | Est. (human / CC) |
|----|-------|---------|--------|-------------------|
| **C1** | `docs/getting-started.md` — local dev | No single doc explains how to run agentserver locally from a fresh checkout. Dev onboarding requires reading 3 READMEs + `Makefile`. | New doc: clone, `make dev`, env vars, seed DB, test with curl. Go backend + React frontend. | ~2 h / ~10 min |
| **C3** | Add `## Decisions Locked` to canonical feature docs | Key decisions (D1–D6 per spec) are buried inside eng spec files, not in the canonical feature doc. Hard to find post-merge. (DD3-B) | Append `## Decisions Locked` section to `workspace-auth.md` (B3 output), `playground-design.md`, and `docs/ops/cobranca-admin-setup.md` (B4 output). Seed with decisions from corresponding eng specs. | ~2 h / ~10 min |
| **C4** | Archive old plan docs | `docs/plans/2026-03-05-*`, `2026-03-09-*`, `2026-03-10-*` are superseded pre-implementation plans. Clutter `ls docs/plans/`. | Move to `docs/archive/plans/` with an index. No content change. | ~30 min / ~3 min |
| **C5** | `docs/lessons-learned.md` — add post-Sprint 4/5 entries | Last entry is pre-playground sprints. Missing: plugin-sdk symlink (`/app` not `/usr/local`), ConfigMap `/` key restriction, internal-workers toleration pattern, `WHERE NOT EXISTS` idempotent seed pattern. | Add 4–5 rows to the table. | ~30 min / ~3 min |

---

## Pending Test Activities — features shipped without full coverage

Features desenvolvidas recentemente que não foram testadas ou só foram testadas parcialmente. Cada item inclui o que existe hoje, o que falta, e o bloqueador (se houver).

### T1 — Cobrança wedge UI (PRs #81 + #82)

| Layer | O que foi testado | O que falta | Bloqueador |
|-------|-------------------|-------------|------------|
| `deployAgent()` unit | ✅ 6 vitest tests (`deploy.test.ts`): list failure, delete 404, quota_exceeded, re-throw | — | — |
| `isDevMode` / simplified editor | ❌ Sem teste automatizado. Prop `isDevMode=false` esconde 5 controles dev — nunca validado via browser ou componente test | Vitest component test ou Playwright smoke | Nenhum |
| Fork-from-marketplace flow | ❌ `handleForkCobrana` em `Playground.tsx` — nunca executado contra cluster real | Smoke manual: logar como sister, clicar "Usar modelo de cobrança", verificar redirect `/playground/souls/:id?firstTime=1` | Requer workspace da sister + quota ≥ 1 |
| Deploy button E2E | ❌ Apenas unit test de `deployAgent`. Botão "Publicar agente" em `PlaygroundSoulEditor.tsx` nunca clicado contra cluster real | Smoke manual: clicar Publicar → verificar sandbox criado via `GET /api/workspaces/{id}/sandboxes` | Requer workspace + quota |
| Manual smoke (9 steps) | ❌ Nenhum passo executado. Definido em `docs/specs/cobrana-wedge-eng-spec.md:226` | Executar os 9 passos antes do demo da sister | Requer: workspace, invite aceito pela sister, WhatsApp BSP |

**Bloqueador raiz de T1:** admin não executou os pré-requisitos operacionais (workspace, maxSandboxes, invite). Ver B4.

---

### T2 — Workspace invites (PR #71)

| Layer | O que foi testado | O que falta |
|-------|-------------------|-------------|
| DB layer | ✅ 8 testes em `internal/db/invites_test.go` (create, get, expired, accepted, duplicate, revoke, list) | — |
| HTTP handler layer | ❌ Zero testes em `internal/server/` para rotas `/api/workspaces/{id}/invites`. Handlers foram escritos, não cobertos. | Testes de handler: POST create, GET list, DELETE revoke, POST accept — contra DB real |
| Email delivery | ❌ `DevMailer` só loga para stdout. Sem teste de que o template renderiza corretamente | Teste de template + asserção de link de convite no corpo |
| Frontend invite modal | ❌ Sem teste de componente para `InviteModal.tsx` e `AcceptInvite.tsx` | — |

---

### T3 — Workspace audit log (PR #72)

| Layer | O que foi testado | O que falta |
|-------|-------------------|-------------|
| DB layer | Implícito nos handlers (migration 041 roda nos testes de integração) | — |
| HTTP handler layer | ❌ Zero testes em `internal/server/` para `GET /api/workspaces/{id}/audit-log` | Teste de handler: asserções de paginação, filtro por workspace_id, autenticação |
| Evento de login via slug | Presença de código em `internal/auth/*` validado via PR review | Smoke manual: logar via `slug.base-domain`, verificar evento em audit log |

---

### T4 — CI skip list (`.github/workflows/build.yml`)

12 testes na skip list marcados como "pre-existing, out of Sprint 2 scope". Nunca reavaliados.

```
TestCodexThreadIDRoundTrip | TestAgentSessionTUIFields | TestActiveTurnCAS |
TestAttachResponder | TestListSessionsByChannel | TestAgentRegister_TypeValidation_Integration |
TestHandleVerifyCodexToken_HappyPath | TestCredentialBindings_ResponseNeverContainsAuthBlob |
TestDeviceUserCode_ReturnsPendingRow | TestWorkspaceBinding_PostListDelete |
TestBridge_TwoConcurrentBridgesShareInbound | TestBridge_StreamIdCollisionEvictsFirst
```

Ação: auditar cada um — fix ou documentar por que skip é permanente. **Não adicionar novos skips sem issue.**

---

### T5 — AdminPanel.tsx cast `(item as any).visibility`

`AdminPanel.tsx:344` usa `(item as any).visibility === 'shared'` para contornar `PlaygroundDraftStatus` que não tem variant `'shared'`. Cast apaga type safety; sem teste cobrindo o branch.

Ação: corrigir o tipo (`PlaygroundDraftStatus` ou tipo union separado) + adicionar vitest para o branch `visibility === 'shared'`.

---

### T6 — Seed idempotency após fix de toleration (PR #83)

Seed foi executado uma vez com sucesso. Idempotência (`WHERE NOT EXISTS`) foi validada pela ausência de duplicate key error. Mas:
- Re-rodar `apply-seed-cobrana.sh` após atualização de conteúdo (novo `prompt.md`) não foi testado
- Comportamento quando `soul_drafts` já existe mas `skill_drafts` não (parcialmente seeded) não foi testado

Ação: documentar no runbook (B2) que re-seed requer `DELETE FROM soul_drafts WHERE name = 'Agente de Cobrança' AND workspace_id IS NULL` antes de rodar novamente.

---

### Resumo — prioridade de teste

| ID | Feature | Risco se não testado | Prioridade |
|----|---------|---------------------|------------|
| T1 | Cobrança wedge E2E | Demo da sister falha ao vivo | 🔴 Alta |
| T2 | Invite handler | Convite funciona no DB, pode quebrar no HTTP layer | 🟡 Média |
| T3 | Audit log handler | Dados existem mas API pode retornar 500 inesperado | 🟡 Média |
| T4 | CI skip list | Regressões silenciosas em 12 testes | 🟡 Média |
| T5 | AdminPanel cast | Type safety furada, bug silencioso se `visibility` mudar | 🟡 Média |
| T6 | Seed idempotency | Re-seed após mudança de conteúdo pode falhar silenciosamente | 🟢 Baixa |

---

## Explicitly out of scope

- Rewriting `docs/superpowers/` plans/specs — upstream material, reference only.
- Generating full SDK docs — tracked in A0 (OpenAPI); SDK codegen is a future item.
- Migrating docs to a doc site (GitBook, Docusaurus) — product decision, not in this sprint.
- Partial API coverage — DD2 locked to full coverage; no partial B1 allowed.

---

## Critical Path

```
A0 (OpenAPI regen) ──────────────────────────────> B1 (Playground API ref)
                                                          │
A1+A2+A3+A4+A5 (batch PR) ──> B3 (workspace-auth) ──────┤
                               B4 (cobrança ops)         │
                               B5 (handoffs)             │
                               B6 (multitenancy)         ▼
                                                    C3 (Decisions Locked)
                                                    C4 (archive plans)
                                                    C5 (lessons learned)
                                                    C1 (getting started)
```

B3 depends on A3 (F1–F6 already archived before merging the 6 docs into 1).  
B1 depends on A0 (spec must be current before writing against it).  
C3 depends on B3 + B4 (needs the canonical docs to exist first).

---

## Suggested 1-week sprint

**Day 1**
- A0 OpenAPI regen (prerequisite, unblocks B1)
- A1 + A2 + A3 + A4 + A5 → batch into 1 PR `docs: sync ship status`

**Day 2**
- B2 Ops runbook + B4 Cobrança admin setup → 1 PR `docs: ops guides`

**Day 3**
- B3 workspace-auth collapse 6→1 → 1 PR

**Day 4**
- B1 Playground API reference (now A0 is done)
- B5 Cursor-handoffs status

**Day 5**
- B6 Multitenancy roadmap gaps
- C5 Lessons learned (quick)

**Exit criteria**
- `git ls-files docs/ | wc -l` same or fewer (no new orphans).
- Every shipped feature has ≥ 1 API reference entry.
- `cursor-handoffs/README.md` shows correct ship status for all 10 items.
- Single `workspace-auth.md` covers design + implementation + pendencies.
- One ops runbook covers deploy → seed → rollback end-to-end.
- OpenAPI spec current; CI blocks drift.

---

## REVIEW REPORT

**Status: READY — decisions locked, critical path explicit**

### Critical path (implement in order)
1. **A0 first** — OpenAPI regen unblocks B1; do not write API ref against stale spec
2. **A-batch before B3** — F1–F6 must be archived before merging the 6 workspace-auth docs
3. **B3 + B4 before C3** — Decisions Locked sections need canonical docs to exist

### Decisions locked this review
- DD1: workspace-auth → 1 doc (not 2)
- DD2: B1 → full route coverage (not partial)
- DD3: ADRs → `## Decisions Locked` in existing docs (not new dir)
- DD4: OpenAPI → CI-gated regen (not one-shot)

### Open concerns (not blockers)
- `make openapi:gen` target may not exist yet — verify before A0; may need to write the target
- B3 archive step: `docs/archive/` does not exist yet — create dir in same PR
- C1 (getting started) omitted from sprint — low CI/onboarding risk short-term, add if a new contributor joins

### What was cut vs. original backlog
- C2 (OpenAPI) promoted to **A0** — was mistiered as low priority
- B3 scope expanded from 6→2 to **6→1** — Boil the Lake applied
- C3 (ADR dir) replaced by **`## Decisions Locked` sections** — avoids creating a 3rd place to look for decisions

---

## Quick reference — affected files

| File / Dir | Action | Item |
|-----------|--------|------|
| `docs/api/openapi.yaml` | Regen + CI drift check | A0 |
| `docs/cursor-handoffs/README.md` | Mark B01 ✅ PR #71, B07 ✅ PR #72 | A1 |
| `docs/playground-marketplace-v2-backlog.md` | Promote Tier A to baseline | A2 |
| `docs/workspace-auth-pendencies.md` | Archive F1–F6 resolved items | A3 |
| `docs/superpowers/README.md` | Create — upstream provenance note | A4 |
| `docs/improvements.md` | Add ✅ column to index table | A5 |
| `docs/api/reference/playground.md` | Create — full playground + marketplace API | B1 |
| `docs/ops/runbook.md` | Create — deploy, seed, rollback | B2 |
| `docs/workspace-auth.md` | Merge all 6 workspace-auth docs here | B3 |
| `docs/archive/` | Create + receive 5 retired workspace-auth docs + old plans | B3, C4 |
| `docs/ops/cobranca-admin-setup.md` | Create — admin setup guide | B4 |
| `docs/cursor-handoffs/B*.md` | Add `## Status` to open items | B5 |
| `docs/saas-multitenancy-roadmap.md` | Mark closed gaps with PR refs | B6 |
| `docs/lessons-learned.md` | Add Sprint 4/5 rows | C5 |
| `docs/getting-started.md` | Create — local dev guide | C1 |
