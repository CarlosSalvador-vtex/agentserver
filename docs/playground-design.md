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
