# Soul + Skills Playground — Design Doc

> **Status:** design, pre-implementation. Three PRs to ship MVP
> (~1550 LOC). All decisions in this doc are output of a structured
> brainstorm (see commit history: this doc + parent discussion).
> Sign-off on this design unblocks PR #14.

## 1. Goal

Give skill authors a **web-based playground** to create + iterate on
`soul.md` (persona) and skill bundles, then **install** chosen
combinations into sandbox environments. Same skills + different souls
= different agents that share capability but differ in identity.

In production: a tenant selects 1 soul + N skills + per-skill config →
the sandbox boots with the LLM trained on that persona, hooked into
those tools. Skill authors iterate in the playground; promote-to-prod
opens a PR on the `agentserver` repo; merge + helm upgrade rolls the
new version out.

## 2. Concepts

```
soul       = identity definition  (who the agent IS)
skill      = capability bundle    (what the agent CAN DO)
composition = soul ⊕ skills[] ⊕ config   (what a sandbox runs)
```

| Concept | Where it lives in prod | Where it lives in draft |
|---|---|---|
| Soul | `deploy/helm/agentserver/souls/<name>/soul.md` (git) | `soul_drafts` row (Postgres) |
| Skill | `deploy/helm/agentserver/skills/<name>/` (git) | `skill_drafts` row (Postgres) |
| Composition | `sandbox_compositions` row | N/A — drafts referenced by `draft:<uuid>` |

## 3. Decisions made (sign-off matrix)

| # | Question | Decision | Why |
|---|---|---|---|
| 1 | MVP storage shape | Promote (DB drafts → git production) | Fast iteration in playground; production stability via git review |
| 2 | Template refinement | Snapshot default + opt-in track upstream | SaaS B2B prefers predictability; upstream tracking explicit per composition |
| 3 | Soul granularity | Structured frontmatter + body | Enables UI form editor + schema validation + lint rules |
| 4 | Tenant scope | Global system templates only (MVP) | Simpler. Tenant scoping is a v2 problem |
| 5 | Test mode | Hybrid — dry-run default, sandbox on-demand | Dry-run is instant + cheap; full sandbox costs cluster resources |
| 6 | Soul scope | Separate entity, composed by ref | 1 soul reusable across N sandboxes; identity ≠ capability |
| 7 | Promote auth | `maintainer` + `owner` roles | Devs iterate freely on drafts; promotion is gated |

## 4. Schema

### 4.1 Migration 032

```sql
-- internal/db/migrations/032_playground.sql

-- Skill drafts (multi-file via JSONB blob — keys are relative paths).
CREATE TABLE IF NOT EXISTS skill_drafts (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name            TEXT NOT NULL,
    description     TEXT,
    author_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    files           JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- {"index.mjs": "...", "prompt.md": "...", "references/leads.json": "..."}
    status          TEXT NOT NULL DEFAULT 'draft',
    -- draft | promoting | promoted | archived
    promoted_pr_url TEXT,
    promoted_commit TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (author_user_id, name)
);
CREATE INDEX idx_skill_drafts_status ON skill_drafts(status);
CREATE INDEX idx_skill_drafts_author ON skill_drafts(author_user_id);

-- Soul drafts (frontmatter + body kept separate for schema validation).
CREATE TABLE IF NOT EXISTS soul_drafts (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name            TEXT NOT NULL,
    description     TEXT,
    author_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    frontmatter     JSONB NOT NULL DEFAULT '{}'::jsonb,
    body            TEXT NOT NULL DEFAULT '',
    schema_version  TEXT NOT NULL DEFAULT 'v1',
    status          TEXT NOT NULL DEFAULT 'draft',
    promoted_pr_url TEXT,
    promoted_commit TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (author_user_id, name)
);
CREATE INDEX idx_soul_drafts_status ON soul_drafts(status);

-- Composition table: 1 row per sandbox carrying refs.
CREATE TABLE IF NOT EXISTS sandbox_compositions (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    soul_ref        TEXT,
    -- "git:<name>@<sha>" | "draft:<uuid>" | NULL (no soul, plain sandbox)
    skill_refs      TEXT[] NOT NULL DEFAULT '{}',
    -- ["git:cobranca@a3f2c", "draft:f3a2b..."]
    skill_config    JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- {"cobranca": {"creditor_name": "Acme"}, ...}
    track_upstream  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ephemeral test sandboxes — quota table.
CREATE TABLE IF NOT EXISTS playground_test_sandboxes (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    author_user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_playground_test_sandboxes_expires ON playground_test_sandboxes(expires_at);
CREATE INDEX idx_playground_test_sandboxes_author ON playground_test_sandboxes(author_user_id);
```

### 4.2 Soul.md schema (frontmatter v1)

```yaml
---
schema: v1
id: julia-cobranca
version: 1.2.0
description: Atendente de cobrança pt-BR para Acme

voice:
  language: pt-BR                              # required
  formality: high | medium | low               # required
  tone_examples: [paciente, técnica, discreta] # optional, free-form labels

constraints:
  max_turns: 20                                # optional, default 50
  refuse_patterns:                             # optional
    - legal-threat
    - pii-disclosure-request
  handoff_to_human_if:                         # optional
    - out-of-scope
    - hostile-user

compatible_skills:                             # optional hint, not enforced
  - cobranca
  - escalation-handoff
---

# Júlia — atendente de cobrança Acme

[body markdown — persona descritiva, livre]
```

JSON schema validator runs on every PATCH/POST. Errors → 400 with
structured detail (field path + reason).

### 4.3 Composition ref grammar

```
ref = "git:" <name> "@" <sha> | "draft:" <uuid>
```

`git:` refs hit the on-disk skill/soul directory loaded at chart deploy
(read-only at runtime). `draft:` refs hit the DB blob.

`track_upstream=true` allows branch refs: `git:cobranca@main` → resolves
at sandbox boot to whatever sha `origin/main` points to. **Default false.**

## 5. API surface

### 5.1 Playground (drafts)

```
GET    /api/playground/skills                    role: developer+
       → [{id, name, description, status, updated_at}]

POST   /api/playground/skills                    role: developer+
Body:  {name, description?}
       → {id, ...}

GET    /api/playground/skills/{id}               role: developer+
       → {id, name, files: {path: content}, status, ...}

PATCH  /api/playground/skills/{id}               role: developer+
Body:  {files?, description?}
       → 200 {updated_at}
       409 on size violation; 422 on schema invalid

DELETE /api/playground/skills/{id}               role: developer+
       Soft-delete: status='archived'

POST   /api/playground/skills/{id}/dry-run       role: developer+
Body:  {soul_ref?, user_message, history?}
       → {assistant_message, tool_calls[], system_prompt_preview}
       Calls llmproxy with composed prompt + mock tools.

POST   /api/playground/skills/{id}/test-sandbox  role: developer+
Body:  {soul_ref?, sandbox_type: openclaw|hermes, config?}
       → {sandbox_id, websocket_url, expires_at}
       409 on quota exhausted (3 simultaneous per user)

POST   /api/playground/skills/{id}/promote       role: maintainer+
Body:  {target_name?, commit_message?}
       → {pr_url, branch}
       409 on lock (another promote in-flight for same name)

# Identical surface for /api/playground/souls
GET    /api/playground/souls
POST   /api/playground/souls
GET    /api/playground/souls/{id}
PATCH  /api/playground/souls/{id}
DELETE /api/playground/souls/{id}
POST   /api/playground/souls/{id}/promote
```

### 5.2 Catalog (read-only — production git + drafts mixed)

```
GET /api/templates/skills
    → [{ref: "git:cobranca@a3f2c", name, description, version, source: "git"},
       {ref: "draft:f3a2b...",    name, description, status, source: "draft"}]

GET /api/templates/souls
    → similar

GET /api/templates/skills/{ref}
    → full content for inspection (read-only)
```

### 5.3 Composition (sandbox create extension)

`POST /api/workspaces/{wid}/sandboxes` body gains an optional
`composition` field. Backward-compatible: omit → no composition, behaves
as today.

```json
{
  "name": "ws-cobranca-1",
  "type": "openclaw",
  "cpu": 1000,
  "memory": 2147483648,
  "composition": {
    "soul": "git:julia-cobranca@a3f2c",
    "skills": ["git:cobranca@1.0", "draft:f3a2b..."],
    "config": {
      "cobranca": {"creditor_name": "Acme Telecom"}
    },
    "track_upstream": false
  }
}
```

Response unchanged shape; composition stored in `sandbox_compositions`.

## 6. Resolution at sandbox boot

In `internal/sandbox/manager.go`, before
`StartContainerWithIP`:

```go
1. Read sandbox_compositions WHERE sandbox_id = ?
2. For each ref in (soul_ref + skill_refs):
     switch refKind(ref) {
     case "git":
       // Files come from the chart-mounted ConfigMap (already present
       // via skills-configmap.yaml + the new souls-configmap.yaml).
       // No extra fetch — just record which ConfigMap items to mount.
     case "draft":
       // Load FROM {skill,soul}_drafts. Materialize into an ephemeral
       // per-sandbox ConfigMap "agentserver-draft-<sandboxID>-<name>".
       // Same path semantics as the chart ConfigMap.
     }
3. Materialize:
   - Soul → ConfigMap key "soul.md", mount at:
     * Hermes:   /opt/agent/soul.md
     * OpenClaw: /home/agent/.openclaw/soul.md
   - Skills → existing pattern from PR #2 / PR #13 (extensions/<name>/).
4. Inject:
   - Hermes: appendSystemPrompt(soul.body) into config.yaml
   - OpenClaw: __OPENCLAW_INJECT_CFG gains agent.systemPrompt += soul.body
   - skill_config[<name>] values into the corresponding plugin's
     configSchema input.
5. On pod deletion: ephemeral draft ConfigMaps cascade-delete via
   ownerReference.
```

## 7. Promote flow (draft → PR)

```
POST /api/playground/skills/{id}/promote

[server] validate:
  - openclaw.plugin.json present + id field set
  - SKILL.md present + parseable frontmatter
  - No PII heuristics in references/*.json (CPF/email/phone regex scan)
  - Schema validators pass
  - Acquire advisory lock by (kind, name)

[server] git operations (via go-git or shelling out gh CLI):
  - git checkout -b playground/<name>-<id>
  - For each file in skill_drafts.files:
      write to deploy/helm/agentserver/skills/<name>/<path>
  - git add deploy/helm/agentserver/skills/<name>/
  - git commit -m "feat(skill): <name> v<version> (promoted from playground)"
  - git push origin playground/<name>-<id>
  - gh pr create --title ... --body "Promoted from playground draft <id>"

[server] update DB:
  - status = 'promoting' during git ops
  - on success: status='promoted', promoted_pr_url=<url>, promoted_commit=<sha>
  - on failure: status='draft', error logged

[server] release lock
```

After PR merge → next `helm upgrade` materializes the new git skill +
sandboxes can `git:<name>@<sha>` from then on. Sandboxes still using
`draft:<id>` keep working until recreated.

## 8. Frontend (`web/`)

### 8.1 Routes
```
/playground                       → catalog + create draft buttons
/playground/skills/{id}           → editor (Monaco + file tree + test panel)
/playground/souls/{id}            → editor (form for frontmatter + Monaco for body)
```

### 8.2 Skill editor layout
```
┌──────────────────────────────────────────────────────────────┐
│  cobranca-v2 (draft) — author: carlos.salvador     [● saved] │
│  [Save] [Test ▾] [Promote → PR]                              │
├──────────────────┬───────────────────────────────────────────┤
│ Files            │ EDITOR (current: prompt.md)               │
│ ├ SKILL.md       │ ┌──────────────────────────────────────┐  │
│ ├ prompt.md  ◀   │ │ # cobranca skill                     │  │
│ ├ index.mjs      │ │ ...                                  │  │
│ ├ openclaw.*     │ └──────────────────────────────────────┘  │
│ └ references/    │                                            │
│   └ leads.json   │ ▾ TEST                                    │
│ [+ Add file]     │ Mode: ● Dry-run  ○ Ephemeral sandbox      │
│                  │ Soul: [julia-cobranca v]   Config: {…}    │
│                  │ ┌──────────────────────────────────────┐  │
│                  │ │ > /cobranca                          │  │
│                  │ │ < Júlia: Olá, sou a Júlia, falo com? │  │
│                  │ │ > meu cpf termina em 111             │  │
│                  │ │ ⓘ tool lookup_debt("111") → {…}      │  │
│                  │ └──────────────────────────────────────┘  │
└──────────────────┴───────────────────────────────────────────┘
```

### 8.3 Soul editor — form-driven frontmatter
```
┌──────────────────────────────────────────────────────────────┐
│  julia-cobranca (draft)                    [Save] [Promote]  │
├──────────────────────────────────────────────────────────────┤
│ ID:           [julia-cobranca           ]                    │
│ Version:      [1.2.0                    ]                    │
│ Description:  [Atendente de cobrança...]                     │
│                                                              │
│ Voice                                                        │
│   Language:   [pt-BR ▾]                                      │
│   Formality:  ● High  ○ Medium  ○ Low                        │
│   Tone exs:   [paciente] [técnica] [+ add]                   │
│                                                              │
│ Constraints                                                  │
│   Max turns:  [20    ]                                       │
│   Refuse:     [☑ legal-threat] [☑ pii-disclosure] [+ add]    │
│   Handoff:    [☑ out-of-scope] [☑ hostile-user] [+ add]      │
│                                                              │
│ Compatible skills (hint):                                    │
│   [cobranca ✕] [escalation-handoff ✕] [+ add]                │
├──────────────────────────────────────────────────────────────┤
│ BODY (Monaco markdown)                                       │
│ ┌──────────────────────────────────────────────────────────┐ │
│ │ # Júlia — atendente de cobrança Acme                     │ │
│ │ Você é a Júlia, voz da cobrança...                       │ │
│ └──────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

### 8.4 Composition picker (extends sandbox create modal)
```
┌──────────────────────────────────────────────────────────────┐
│  Create sandbox                                              │
├──────────────────────────────────────────────────────────────┤
│ Name:    [ws-cobranca-1     ]    Type: ○ opencode ● openclaw │
│ CPU:     [1000 mc]              Mem:  [2 GiB]                │
│                                                              │
│ ▾ Composition (optional)                                     │
│   Soul:    [git:julia-cobranca@1.2 ▾]                        │
│   Skills:  [git:cobranca@1.0  ✕] [+ add]                     │
│   Config:  cobranca → creditor_name: [Acme Telecom         ] │
│   Track upstream: ○ Pin sha (snapshot)  ● Follow @main       │
│                                                              │
│                                              [Cancel] [Create]│
└──────────────────────────────────────────────────────────────┘
```

## 9. Lint rules (soul vs skill discipline)

Server-side heuristics that run on PATCH (warn only, don't block):

| Rule | Detects | Suggests |
|---|---|---|
| `skill.prompt.md` has heading `# Você é` / `# You are` | Identity content leaked into skill | Move to `soul.md` body |
| `soul.body` mentions `lookup_debt` / specific tool names | Capability leaked into identity | Move to `skill.prompt.md` |
| `soul.body` > 2 KiB | Bloated identity | Split into voice (frontmatter) + persona (short body) |
| `skill/openclaw.plugin.json` missing `configSchema` | Plugin loader rejects | Add at minimum `{type: object, properties: {}}` |
| `skill/index.mjs` `export default` is an `AsyncFunction` | OpenClaw silently drops async register | Switch to sync `{id, register(api)}` literal |

UI shows yellow warning bar with link to relevant doc section.

## 10. Quotas + ephemeral test cleanup

| Resource | Limit | Where enforced |
|---|---|---|
| Drafts per user | 50 (skills) + 50 (souls) | `POST /api/playground/{kind}` checks `COUNT` |
| File size per file | 256 KiB | `PATCH` rejects with 413 |
| Total draft size | 1 MiB | `PATCH` rejects with 413 |
| Ephemeral test sandboxes per user | 3 simultaneous | `POST /test-sandbox` checks count of unexpired |
| Ephemeral test TTL | 10 minutes | Background goroutine `playground_test_sandbox_reaper` runs every minute |

Reaper goroutine:
```go
for range time.Tick(time.Minute) {
    rows := db.Query(`SELECT sandbox_id FROM playground_test_sandboxes WHERE expires_at < NOW()`)
    for _, sandbox_id := range rows {
        s.Sandboxes.Delete(sandbox_id)  // triggers k8s pod delete + cascade
    }
}
```

## 11. Tool name namespacing

When a sandbox boots with multiple skills, the LLM tool registry gets
namespaced names automatically:

```
skill cobranca     defines lookup_debt
skill escalation   defines lookup_debt
                   ↓
LLM sees:          cobranca.lookup_debt
                   escalation.lookup_debt
```

Resolver in composition validation: if 2 skills define the same name,
the resolver auto-namespaces both. Skill authors don't need to prefix —
the platform handles it.

## 12. Schema versioning (forward-compat)

`soul_drafts.schema_version` starts at `v1`. When v2 ships:
- Frontmatter readers default to v1; v2 readers check `schema:` field
- Promote validator targets the schema in `schema:` field, falls back to v1
- v2 frontmatter for newly-created drafts; v1 drafts can be edited
  in-place and stay v1 forever
- Eventual cleanup migration: v1→v2 batch transform

Same pattern for skill `openclaw.plugin.json` if it gains fields.

## 13. Risks + mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Ephemeral sandbox abuse — devs spam create | High | Cluster resource exhaustion | Per-user quota (3 concurrent) + 10min TTL + rate limit `POST /test-sandbox` to 5/min/user |
| Draft DB bloat | Medium | Disk pressure | File + total size cap; archive policy 90 days no-promote-no-edit |
| Promote race condition | Low | Two PRs for same name | Advisory lock by `(kind, name)`; second PROMOTE returns 409 |
| Soul + skill prompt overlap leading to LLM confusion | Medium | Bad output quality | Lint warnings; doc convention; smoke tests against fixtures |
| Schema migration of frontmatter | Low | Drafts unreadable | `schema_version` column; readers per-version; cleanup migration |
| Tool name collision between 2 skills | Medium | LLM picks wrong tool | Auto-namespace at compose time |
| Author abandons draft, blocks name | Low | Promote impossible due to UNIQUE | Reaper for archived+stale drafts after 90d |
| Ephemeral sandbox leak (pod survives DB row delete) | Low | Orphaned pod | Reaper double-checks by querying k8s; cleanup orphans |
| Tenant A's draft data exfiltrated via promote PR | Medium | Data leak | Promote PR review is the gate; system templates only (MVP), no tenant-scoped catalog |

## 14. Out of scope (v2)

- **Tenant-scoped catalog** — only global system templates in MVP.
- **Marketplace / public sharing** — depends on tenant scoping.
- **Slash command nativo via plugin-sdk** — see `docs/openclaw-skill-slash-research.md`. Still path-based prompting in v1; native slash needs initContainer symlink (Option B from that doc).
- **Composition versioning** — sandbox composition is immutable after create; edit = recreate sandbox. v2 may add migration helpers.
- **Skill marketplace ratings / reviews**.
- **Multi-language soul authoring** — soul.md is single-language; v2 may bind per-locale variants.
- **A/B testing of personas** — per-sandbox composition only; A/B routing layer is v2.

## 15. Implementation roadmap

### PR #14 — backend (target ~700 LOC)
- `internal/db/migrations/032_playground.sql` (~100 LOC)
- `internal/db/playground.go` — CRUD helpers (~250 LOC)
- `internal/server/playground_handlers.go` — REST handlers (~300 LOC)
- `internal/sandbox/composition.go` — resolution at boot (~150 LOC)
- Extend `manager.go::buildPodSpec` to load + mount soul ConfigMap (~50 LOC)
- Extend `BuildOpenclawConfig` / `BuildHermesConfig` to append soul body to system prompt (~30 LOC)
- Dry-run endpoint hits llmproxy with composed prompt (~80 LOC)
- Unit tests: composition resolution, schema validation, lint rules (~200 LOC test)

### PR #15 — frontend (target ~500 LOC)
- `web/src/routes/Playground.tsx` — catalog page (~80 LOC)
- `web/src/routes/PlaygroundSkillEditor.tsx` — Monaco + file tree + test panel (~250 LOC)
- `web/src/routes/PlaygroundSoulEditor.tsx` — form + Monaco body (~150 LOC)
- `web/src/components/CompositionPicker.tsx` — used in sandbox create modal (~120 LOC)
- `web/src/lib/api.ts` — `playgroundListSkills`, `playgroundCreateSkill`, `playgroundPatchSkill`, `playgroundDryRun`, `playgroundTestSandbox`, `playgroundPromote` (~100 LOC)
- TypeScript types from updated OpenAPI

### PR #16 — promote + lifecycle (target ~350 LOC)
- `internal/server/playground_promote.go` — git ops + PR generator (~200 LOC)
- PII heuristics + schema validator chain (~80 LOC)
- Ephemeral test sandbox reaper goroutine (~50 LOC)
- Quota enforcement middleware (~50 LOC)
- Integration test: full draft → dry-run → ephemeral test → promote → PR (~150 LOC test)

## 16. Verification plan

### 16.1 Unit (per PR)
- Composition refs parse correctly
- Soul frontmatter schema validates correctly (positive + negative cases)
- Tool name collision triggers namespace
- Lint rules fire on staged drafts
- Quota enforcement rejects over-limit

### 16.2 Integration (PR #16 + dev EKS)
```
1. Create skill draft via UI → PATCH adds 5 files → save OK
2. Create soul draft → frontmatter form fills → save OK
3. Dry-run with soul + skill → llmproxy returns Júlia greeting + tool call
4. "Run in sandbox" → ephemeral pod boots → chat works → 10min TTL kills it
5. Promote skill → PR opens in CarlosSalvador-vtex/agentserver
6. Merge PR → helm upgrade → production sandbox creates with `git:<name>@<sha>`
7. Sandbox composition with both git refs + draft refs → both materialize
```

### 16.3 Manual evals (post-MVP, ongoing)
- 5-turn dialogue against 3 fixture personas — judge persona adherence
- Tool invocation accuracy with 2 skills bound (cobranca + escalation)
- Refuse pattern enforcement (try `legal-threat`, should hand off)

## 17. References

- Brainstorm thread → this doc (single session, 2026-05-26)
- `docs/skills-system.md` — current skill distribution architecture
- `docs/openclaw-skill-slash-research.md` — why slash command nativo is out of scope for v1
- `docs/multi-channel-routing.md` — workspace routing strategy (composition picker integrates with this)
- PR #2 — cobranca skill MVP (skill bundle shape)
- PR #13 — cobranca discoverability fix (mount path + manifest shape)
- Upstream OpenClaw image: `ghcr.io/agentserver/openclaw-agent@sha256:1c03752715d7739093a764e5d4fea097f970ad315201ce6c9b4d7e903ada6a5d`

---

**Sign-off needed before PR #14:**
- [ ] Schema (section 4) approved
- [ ] API surface (section 5) approved
- [ ] Quotas (section 10) approved
- [ ] Out-of-scope (section 14) accepted

After sign-off, PR #14 can start.

---

## Appendix A — Alternatives considered

Each subsection captures the options weighed for a single decision in
section 3, the trade-offs, and what we'd lose by reverting. Use this
when someone six months from now asks "why didn't you do X?" — the
answer lives here instead of triggering a re-debate.

### A.1 MVP storage shape

**Decision:** Promote (DB drafts → git production via PR).

**Rejected — Option Iceberg (everything in git, UI as thin shell):**

| ✅ | ❌ |
|---|---|
| Zero new storage | Latency: every save = commit + helm upgrade |
| Audit + history grátis via git log | Gate-keepers (PR review) for every iteration |
| No DB schema to maintain | Web IDE writing to a live repo is operationally risky |
| Production = source of truth always | Frustrating draft experience (lose work on conflict) |

Why-not: kills the iteration loop the playground exists to enable.
A skill author wants to try a phrase, see the response, tweak, try
again — Iceberg forces a git round-trip per attempt. Useful only if
the playground is occasional + senior-only.

Revert if: usage stays low (<5 authors total), DB pressure becomes
real, or compliance demands every change be git-tracked from second
zero.

**Rejected — Option Live (DB only, no git promotion):**

| ✅ | ❌ |
|---|---|
| Fastest iteration | Production reads from DB, not git |
| Single source of truth (DB) | No code review on skill prompts |
| Trivial multi-tenant scoping later | No audit trail outside DB |
| | Backup + migration story heavier |

Why-not: production-grade skills (LGPD-sensitive prompts, regulated
content) need code review. Living entirely in DB means a single PATCH
endpoint with the right role can push a malicious prompt into every
sandbox. Promote gate exists to prevent that.

Revert if: skills become per-tenant configuration (i.e. cobranca-acme
vs cobranca-bcd-bank), making global-git review the wrong granularity.
Then Live makes more sense; tenant ownership replaces git review.

### A.2 Refinement model

**Decision:** Snapshot default + opt-in `track_upstream` boolean.

**Rejected — Patch by default (track upstream):**

| ✅ | ❌ |
|---|---|
| Upstream improvements flow free to all envs | Surprise breakage when upstream ships incompatible change |
| Storage cheap (one canonical version + N diffs) | 3-way merge conflicts when upstream + local both edit same line |
| Always on the latest security/legal disclaimer text | Per-tenant testing burden grows with template velocity |

Why-not: SaaS B2B customers expect static behavior between explicit
upgrades. Auto-flowing prompt edits violates that contract. Worse, a
LGPD-relevant edit upstream could change refuse_patterns in a tenant's
production agent without their consent.

Revert if: a regulatory body mandates a centrally-updated disclaimer
that must propagate to every tenant within hours of publish. Then
patch model becomes a feature, not a footgun.

**Rejected — Snapshot only (no track at all):**

| ✅ | ❌ |
|---|---|
| Maximum predictability | Stuck on stale templates forever unless manually re-snapshot |
| Simplest mental model | Big skill catalog rot over time |
| | Wastes upstream improvements |

Why-not: opt-in `track_upstream=true` adds <10 LOC and gives the same
predictability default + an escape hatch for sandboxes that explicitly
want to follow upstream. No reason to omit it.

### A.3 Soul granularity

**Decision:** Structured frontmatter + body markdown.

**Rejected — Prompt puro (minimal frontmatter, body-only behavior):**

| ✅ | ❌ |
|---|---|
| Maximum author flexibility | No validation possible |
| No schema migration burden | UI must be raw editor (no form) |
| Authors who know what they want move fast | Easy to ship contradictory rules (`max_turns: 5` in body + 50 in another section) |

Why-not: structured frontmatter unlocks the form-driven UI in section
8.3, which is most of the playground's value to non-prompt-engineer
users. Pure prompt assumes every author is an expert; we want to
support marketing + ops folks editing personas, not just engineers.

Revert if: research shows form UI confuses authors who'd rather write
prose. Then frontmatter shrinks to `id + version + body` and the form
disappears. But you keep validation infrastructure for the few fields
that remain.

**Rejected — Rich schema with presets + composable voices:**

```yaml
voice:
  preset: formal-pt-br-corporate     # reference to a registry
  overrides:
    tone_examples: [paciente]
constraints:
  inherit_from: standard-cobranca
  override:
    max_turns: 30
```

| ✅ | ❌ |
|---|---|
| Drier — 1 preset, N souls reusing it | Premature abstraction without 10+ souls to compose |
| Centralized brand voice | Yet another registry to manage (voice presets, constraint presets) |
| Consistency across tenant family of souls | Authors learning curve doubles |

Why-not: presets become valuable when you have 20+ souls sharing
common voices. At MVP scale (5-10 souls total across the system) the
duplication cost is small + the abstraction cost is large.

Revert if: catalog grows past 30 souls and copy-paste-divergence
becomes a maintenance problem. Add a presets layer then; current
schema is forward-compatible (presets can be a new top-level
frontmatter field added in `schema: v2`).

### A.4 Tenant scope

**Decision:** Global system templates only for MVP.

**Rejected — Tenant-scoped + system:**

| ✅ | ❌ |
|---|---|
| Tenant A can't see Tenant B's templates | DB has tenant_id column on every draft; promote gate logic per scope |
| Per-tenant skill catalogs naturally emerge | UI needs scope picker (global vs my workspace's) |
| Production-realistic from day 1 | More LOC, more test surface |

Why-not: real multi-tenant playground is a 6-month product, not a
2-week MVP. Cutting tenant scope removes ~30% of complexity. Skills
in MVP are tools the platform builds + offers to all tenants
(cobranca for billing automation across the platform), not
tenant-authored IP.

Revert if: a tenant requests private skill authoring. Add
`tenant_id NULL = system` column on drafts, scope rules in handlers,
filter in the UI. Migration is straightforward (no data loss).

**Rejected — Tenant + share opt-in (gradual marketplace):**

| ✅ | ❌ |
|---|---|
| Best long-term shape | Most LOC, most product surface |
| Network effect: tenants share useful skills | Moderation policy, abuse review, ratings UX |
| | Legal review on cross-tenant content sharing |

Why-not: marketplace dynamics + cross-tenant content moderation are a
separate product. Build the tenant-scoped substrate first (A.4
rejected option), then evolve to share opt-in as v3.

### A.5 Test mode

**Decision:** Hybrid — dry-run via llmproxy default, ephemeral
sandbox on demand.

**Rejected — Ephemeral sandbox only:**

| ✅ | ❌ |
|---|---|
| Maximum fidelity (real pod, real tool exec) | Each test = 30-60s pod boot |
| Catches mount path bugs, plugin loader bugs | Cluster resource pressure (3 pods × N users) |
| | Iteration loop slow → playground feels heavyweight |

Why-not: 90% of skill iteration is "did the LLM say the right thing?"
which dry-run answers in 2-5s. Pod boot is overkill until you need to
validate tool execution (mark_agreement writing JSONL to disk, etc.).

Revert if: dry-run misses bugs that only show up in real pod (e.g.
configMap mount race). Dry-run is currently bug-free at the layer it
covers — if that changes, force ephemeral-only.

**Rejected — LLM-direct only (no ephemeral fallback):**

| ✅ | ❌ |
|---|---|
| Cheapest, fastest, simplest | Never validates real tool exec |
| No K8s dependency in the playground | Skill bugs surface only in production |
| | Plugin loader behavior untested |

Why-not: tool side-effects (file writes, network calls, etc.) matter.
A skill that writes to `state/agreements.log` needs to be validated
end-to-end before promote, or you ship broken tools to production.

Revert if: tools become side-effect-free transformers (read-only).
Then mock execution is sufficient + ephemeral isn't worth the
cluster cost.

### A.6 Soul scope

**Decision:** Separate entity, composed by ref into sandbox.

**Rejected — Soul as workspace metadata:**

| ✅ | ❌ |
|---|---|
| 1 workspace = 1 brand identity (simpler mental model) | Can't A/B test souls in the same workspace |
| Soul lives in `workspaces` table, no new entity | Reuse across workspaces requires copy-paste |
| | Skills carry implicit persona via prompt overlap |

Why-not: a single workspace may run cobranca-agent + suporte-agent +
vendas-agent, each with a different persona. Forcing them to share one
soul collapses three personas into a least-common-denominator
"brand voice," which loses specificity.

Revert if: tenants only ever run one agent personality. Empirically
unlikely (multi-channel routing PR #3 already supports the "N
channels, N personas" shape).

**Rejected — Soul as a file inside a "primary" skill:**

```
skill cobranca/
  soul.md             ← persona lives here
  prompt.md           ← flow lives here
  index.mjs
```

| ✅ | ❌ |
|---|---|
| Minimal new concept (skill ships its own identity) | Persona reuse across skills impossible |
| Composition simplifies (just pick the primary skill) | Soul becomes versioned with skill — change persona = bump skill version |
| | Same Júlia voice across cobranca + escalation requires duplication |

Why-not: this collapses identity into capability. The whole point of
the soul/skill split is that "Júlia" is a brand voice usable across
many tools. Putting soul inside skill defeats the value proposition.

Revert if: only ever 1 skill per sandbox in practice. Then composition
simplifies + collapsing makes sense. But the multi-channel routing
work already enables multi-skill sandboxes, so the split has runway.

### A.7 Promote authorization

**Decision:** `maintainer` + `owner` roles can promote.

**Rejected — Owner only:**

| ✅ | ❌ |
|---|---|
| Tightest gate — only top role pushes to prod | Bottleneck: 1 owner per workspace blocks all promotes |
| Audit simplest (single accountable role) | Doesn't scale beyond solo workspace owners |

Why-not: even small teams have multiple maintainers handling routine
prompt work. Routing every promote through the workspace owner
creates a queue of pending PRs.

Revert if: a workspace becomes regulated (LGPD audit, SOC2 scope) and
needs single-point accountability. Restrict role then; default
`maintainer+` is enough for development.

**Rejected — Any member promotes (git review is the gate):**

| ✅ | ❌ |
|---|---|
| Maximum dev velocity | PRs spam the repo with unreviewed promotes |
| Trusts code review fully | First-time authors don't know what's promote-worthy |
| | Easy to bypass review intent by self-merging on Github |

Why-not: the promote button creates pressure to review. If anyone can
click it, every draft becomes a PR, and reviewers drown in noise. The
in-product gate is what makes the git-review gate work — it's a funnel.

Revert if: review culture is strong + GitHub branch protection rules
enforce mandatory review. Then promote button bypasses nothing
because the merge gate catches it. But for MVP, two gates > one.

### A.8 What `soul.md` body looks like — composition style

(Not in the brainstorm directly but related; captured here so the
choice isn't lost.)

**Decision implicit:** body is freeform markdown.

**Rejected — body as structured behavior tree (YAML):**

```yaml
greeting: "Olá! Sou a {{name}}, da {{tenant.creditor}}."
clarify_identity:
  ask: "Para sua segurança, poderia confirmar o sobrenome?"
  on_refuse: handoff_to_human
debt_lookup:
  trigger_when: cpf_provided
  call: lookup_debt
```

| ✅ | ❌ |
|---|---|
| Deterministic flow | LLM agents aren't behavior trees — fights the model's strengths |
| Easy to test | Loses naturalness of prompt-driven personality |
| | 5x the schema surface to maintain |

Why-not: LLM agents work best when given a persona + tools + freedom.
Behavior trees are the wrong abstraction (they fit deterministic
state machines, not generative agents). Stick with prose.

Revert if: research shows our LLMs reliably follow YAML flows better
than prose personas. Currently the opposite is true.

---

## Appendix B — Decision review log

When a decision in this doc is revisited (someone proposes a different
shape after reading), update this table:

| Date | Decision (section) | New direction | Reason | PR/discussion |
|---|---|---|---|---|
| 2026-05-26 | All initial decisions | (initial) | Brainstorm session | This doc |
| | | | | |

