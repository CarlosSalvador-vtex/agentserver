# Improvement Roadmap — agentserver fork

> **All 20 items shipped + SaaS webhook isolation (2026-05-27).** The backlog is complete.
> Migrations go up to 038. Current image tag: `saas-webhook`.
> cobranca skill now uses native plugin-sdk imports (definePluginEntry); soul persona
> delivered natively by OpenClaw workspace bootstrap from SOUL.md — no in-plugin
> injection needed.
> This document is now a historical record; use it for rationale on past decisions.

> 20 prioritized improvements derived from the multi-channel routing,
> WhatsApp, and playground sprints. Each entry carries rationale,
> scope estimate, dependencies, and a suggested PR shape. Tiers go
> from "high-impact, low-cost" (1) to "would be nice" (5).
>
> Use this as a planning backlog. Items are independent unless
> "Depends on:" calls out a prerequisite.

## Index

| Tier | # | Title | Status | Est. LOC | Type |
|---|---|---|---|---|---|
| 1 | 1 | Composition picker in sandbox create modal | ✅ Shipped | 150 | feat (UI) |
| 1 | 2 | Magic strings → typed constants | ✅ Shipped | 80 | refactor |
| 1 | 3 | Rate limit dry-run + test-sandbox | ✅ Shipped | 50 | feat (security) |
| 1 | 4 | Integration tests for composition resolution | ✅ Shipped | 100 | test |
| 1 | 5 | OpenClaw SOUL.md equivalent | ✅ Shipped | 80 | feat |
| 2 | 6 | Prometheus metrics for playground | ✅ Shipped | 120 | feat (observability) |
| 2 | 7 | Diff view: draft vs last promoted | ✅ Shipped | 80 | feat (UI) |
| 2 | 8 | Promote PR status polling | ✅ Shipped | 60 | feat (UI) |
| 2 | 9 | Soul standalone dry-run | ✅ Shipped | 80 | feat |
| 2 | 10 | Manager.go refactor (split StartContainerWithIP) | ✅ Shipped | 300 | refactor |
| 3 | 11 | CI/CD automation (GitHub Actions) | ✅ Shipped | 200 | infra |
| 3 | 12 | Ephemeral ConfigMap orphan reaper | ✅ Shipped | 50 | feat (ops) |
| 3 | 13 | WhatsApp HMAC enforced mode | ✅ Shipped | 10 | feat (security) |
| 3 | 14 | Drafts audit log | ✅ Shipped | 100 | feat |
| 3 | 15 | Staging cluster | ✅ Shipped | 200 | infra |
| 4 | 16 | OpenClaw plugin-sdk initContainer symlink | ✅ Shipped | 80 | feat |
| 4 | 17 | Tenant-scoped catalog | ✅ Shipped | 120 | feat |
| 4 | 18 | Soul/skill marketplace (cross-tenant sharing) | ✅ Shipped | 250 | feat |
| 4 | 19 | LLM proxy token resolution (workspace_id in body) | ✅ Shipped | 20 | fix |
| 4 | 20 | Drop legacy `sandboxes.im_channel_id` FK | ✅ Shipped | 30 | chore |

---

## Tier 1 — high-impact, low-cost

### 1. Composition picker in sandbox create modal

**Problem.** Today the only way to attach a composition (soul + skills) to a sandbox is via raw API call (curl, or the playground UI fires a hand-built JSON). The standard "Create Sandbox" modal in WorkspaceDetail.tsx has no composition picker, so the average user can't bind their drafts to a new pod without leaving the UI.

**Solution.**
- Extend `CreateSandboxModal.tsx` with a collapsible "Composition (optional)" panel:
  - Soul dropdown: list `git:<name>@<sha>` from `/api/templates/souls` + `draft:<id>` from `/api/playground/souls`.
  - Skill multi-select: same dual catalog.
  - Per-skill config inputs rendered from each skill's `configSchema` (fetch via GET `/api/playground/skills/{id}` to grab manifest).
  - Track-upstream toggle (default off).
- Pass the resulting `composition` field to the `POST /api/workspaces/{wid}/sandboxes` body (already accepted server-side, PR #17).

**Scope.** ~150 LOC React. No backend changes.

**Dependencies.** None — server endpoints exist.

**Why prioritize.** The biggest UX gap. The playground exists but operators can't compose without API knowledge.

**PR shape.** `feat(ui): composition picker in sandbox create modal`. Test plan: create sandbox via UI with `git:cobranca@<sha>` + `draft:<id>` composition, verify pod boots with both mounts.

---

### 2. Magic strings → typed constants

**Problem.** `"openclaw"`, `"hermes"`, `"opencode"`, `"draft:"`, `"git:"` appear as bare string literals across `internal/sandbox/manager.go`, `internal/sandbox/composition.go`, `internal/server/server.go`, `internal/server/playground_*.go`, frontend `api.ts`. A typo (`"openclap"`) compiles + silently no-ops at runtime. The OpenClaw SOUL.md sprint hit this: we passed `"openclaw"` vs `"openclaw"` in multiple sites, hoping order didn't matter.

**Solution.** Create `internal/sandbox/types.go` (or extend existing `state.go`) with:

```go
type SandboxType string

const (
    SandboxTypeOpencode   SandboxType = "opencode"
    SandboxTypeOpenclaw   SandboxType = "openclaw"
    SandboxTypeNanoclaw   SandboxType = "nanoclaw"
    SandboxTypeClaudeCode SandboxType = "claudecode"
    SandboxTypeJupyter    SandboxType = "jupyter"
    SandboxTypeHermes     SandboxType = "hermes"
)

func (s SandboxType) Valid() bool { ... }

type RefKind string

const (
    RefKindGit   RefKind = "git"
    RefKindDraft RefKind = "draft"
)
```

Switch sites to use constants. Provider names (`"weixin"`, `"telegram"`, `"matrix"`, `"whatsapp"`) get the same treatment in `internal/imbridge/`.

**Scope.** ~80 LOC across ~15 files. Safe automated find/replace.

**Dependencies.** None.

**Why prioritize.** Cheap safety net. Every future feature touching sandbox types or ref kinds benefits.

**PR shape.** `refactor: typed constants for sandbox + provider + ref kinds`. Test plan: `go build -tags goolm ./...` clean; existing tests pass.

---

### 3. Rate limit dry-run + test-sandbox

**Problem.** No throttling on `POST /api/playground/skills/{id}/dry-run` (each call costs an LLM round-trip via llmproxy) or `POST /api/playground/skills/{id}/test-sandbox` (each call spawns a real pod). A buggy frontend loop or a malicious user can drain LLM quota / cluster CPU in seconds.

**Solution.**
- Add middleware in `internal/server/playground_handlers.go` using `golang.org/x/time/rate`:
  - Dry-run: 10 req/min/user, burst 3
  - Test-sandbox: 3 req/min/user, burst 1 (also enforced by the existing 3-concurrent quota, but rate-limit catches spam-and-cancel patterns)
- Track via in-memory `map[userID]*rate.Limiter` with TTL eviction.
- 429 response with `Retry-After` header.

**Scope.** ~50 LOC + a small `playground_ratelimit.go` file.

**Dependencies.** None.

**Why prioritize.** Cheap, prevents both cost overruns and abuse. Should ship before any tenant onboarding.

**PR shape.** `feat(playground): per-user rate limit on dry-run + test-sandbox`. Test: hit endpoint 11× in one second → 11th returns 429.

---

### 4. Integration tests for composition resolution

**Problem.** PR #24 caught a composition race (DB write after goroutine spawn) only via E2E smoke against dev EKS. Unit tests in `composition_test.go` cover ref parsing + frontmatter extraction but not the actual `ResolveComposition` → ConfigMap → pod mount path. A regression in that pipeline would land in production unnoticed.

**Solution.**
- New `composition_integration_test.go` in `internal/sandbox/`:
  - Uses `k8s.io/client-go/kubernetes/fake` for in-memory K8s client
  - Uses `github.com/lib/pq` against a transient postgres (or `sqlmock` for the simpler queries)
  - Test: create draft skill + soul → write composition row → call ResolveComposition → assert returned `EphemeralConfigMaps` count, `ExtraVolumes` paths, `SoulBody` contents
  - Test: race repro — composition not yet written → ResolveComposition returns empty (no panic)
- New `server/playground_create_integration_test.go`:
  - Verifies `provisionSandbox` writes composition row **before** spawning the goroutine

**Scope.** ~100 LOC test code. Test deps already in go.mod.

**Dependencies.** None.

**Why prioritize.** The race we caught manually will resurface as the codebase grows.

**PR shape.** `test(sandbox): integration coverage for composition resolution`. CI runs them.

---

### 5. OpenClaw SOUL.md equivalent

**Problem (original).** Hermes auto-loads `$HERMES_HOME/SOUL.md` (see `docs/lessons-learned.md`). OpenClaw had no documented equivalent — our soul mount was dead weight until something read it.

**What shipped (pivot from planning options).** Image dive found OpenClaw **already** loads `~/.openclaw/workspace/SOUL.md` on bootstrap (same convention as bundled auth-profiles). We fixed the mount path and plugin wiring instead of injecting soul from the skill plugin:

| PR | Change |
|---|---|
| #45 / #16 | initContainer symlink so skills can `import "openclaw/plugin-sdk/core"` |
| #47 (S4-PR1) | Mount composition soul at `/home/agent/.openclaw/workspace/SOUL.md` (`internal/sandbox/composition.go`) |
| #49 (S4-PR2) | `cobranca/index.mjs` uses native `definePluginEntry`; workspace soul left to OpenClaw bootstrap — **no in-plugin soul read/inject** |
| #55 (S4-PR4) | `before_prompt_build` injects **skill** persona from `prompt.md` + registers mock tools — separate from workspace `SOUL.md` |

**Planning options (historical — not what we shipped).**

- **Option A** (originally recommended): skill `index.mjs` reads soul at boot and prepends via plugin-sdk. **Not implemented** — native bootstrap made this redundant; skill hook is for skill prompt, not workspace soul.
- **Option B** (interim): prompt instructs agent to read soul path manually. Superseded by native loader.
- **Option C**: `OPENCLAW_SOUL_FILE` env probe. Partially present in `manager_config.go` (legacy path `/home/agent/.openclaw/soul.md`); runtime uses `workspace/SOUL.md` mount instead.

**Acceptance (met).** OpenClaw sandboxes with a composition soul get persona from the mounted `SOUL.md` without openclaw.json hacks or root-level `agent.systemPrompt` (see `docs/lessons-learned.md` PR #25 row).

**Follow-up (optional housekeeping).** Align or remove stale `OPENCLAW_SOUL_FILE` in `internal/sandbox/manager_config.go` if nothing in the image reads it.

---

## Tier 2 — medium impact

### 6. Prometheus metrics for playground

**Problem.** Playground actions emit nothing. Can't answer "how many drafts created this week?", "what's the dry-run latency P95?", "which skills get promoted?". Operating blind.

**Solution.**
- Add `github.com/prometheus/client_golang/prometheus` to go.mod (or use existing if vendored).
- Register metrics in `internal/server/playground_metrics.go`:
  - `playground_drafts_total{kind="skill|soul", action="created|patched|archived|promoted"}` counter
  - `playground_dryrun_duration_seconds{result="ok|llm_error|validation_error"}` histogram (buckets: 0.5s, 1s, 2.5s, 5s, 10s, 30s)
  - `playground_promote_total{kind, result="ok|failed_validation|failed_github"}` counter
  - `playground_test_sandbox_active` gauge (read from DB on scrape)
  - `sandbox_composition_resolve_duration_seconds` histogram
  - `sandbox_composition_active{has_soul, skill_count}` gauge
- Expose `/metrics` endpoint (probably already exists for the runtime — extend it).
- Grafana dashboard JSON in `deploy/grafana/playground-dashboard.json` for instant import.

**Scope.** ~120 LOC + JSON dashboard.

**Dependencies.** Existing `/metrics` endpoint or Prometheus client lib import.

**Why prioritize.** Without metrics, every Tier 1 improvement ships blind.

**PR shape.** `feat(observability): Prometheus metrics for playground + composition`.

---

### 7. Diff view: draft vs last promoted

**Problem.** When a skill has been promoted (`status='promoted'`), the draft can be edited again. The current UI shows only the live draft files — no way to see "what changed since last promote". Diff is essential for review-before-second-promote workflows.

**Solution.**
- When `draft.status === 'promoted'`, frontend lazy-loads files at `draft.promoted_commit` from the agentserver repo via GitHub API.
- Render side-by-side diff using a small library (`diff-match-patch` or `react-diff-viewer-continued`).
- Diff lives in a tab next to "Files" in `PlaygroundSkillEditor.tsx`.

**Scope.** ~80 LOC frontend + 1 dep (~30 KB gzipped).

**Dependencies.** Promote PR must store `promoted_commit` (already does, PR #16).

**Why prioritize.** Without diff, the second promote becomes "fingers crossed, hope I remember what changed".

**PR shape.** `feat(ui): diff view for promoted-then-edited drafts`.

---

### 8. Promote PR status polling

**Problem.** Today `Promote → PR` opens the PR in a new tab. The draft's `status` stays `promoted` even after the PR is merged or closed — no in-app feedback. User has to manually check the PR.

**Solution.**
- Background poller in `internal/server/playground_promote_poll.go`: every 5 min, for each `status='promoted'` draft, hit GitHub API `GET /repos/{owner}/{repo}/pulls/<number>` → update local cache field `promoted_pr_state` (open/merged/closed).
- Catalog row badges: `[promoted-open]` / `[promoted-merged]` / `[promoted-closed]` instead of generic `[promoted]`.
- Editor banner: "PR #29 merged into main on 2026-05-26" with link.

**Scope.** ~60 LOC.

**Dependencies.** `promoted_pr_url` column already exists. Add `promoted_pr_state` text column via migration 033.

**Why prioritize.** Closes the feedback loop between promote → real production landing.

**PR shape.** `feat(playground): background poll for PR merge status`.

---

### 9. Soul standalone dry-run

**Problem.** `POST /api/playground/skills/{id}/dry-run` exists. Soul has no dry-run. To preview a soul, user must create a throwaway skill, attach the soul, dry-run. Friction.

**Solution.**
- `POST /api/playground/souls/{id}/dry-run` mirrors the skill endpoint:
  - Body: `{user_message, history?}` (no skill_ref needed)
  - Composes system prompt from soul body only
  - Calls llmproxy with workspace proxy token
  - Returns `{system_prompt, completion, completion_model, completion_error}`
- Frontend: same "Run dry-run" button on the soul editor page.

**Scope.** ~80 LOC backend + 30 LOC frontend.

**Dependencies.** None.

**Why prioritize.** Symmetry. Soul authoring is a first-class step; deserves the same feedback loop.

**PR shape.** `feat(playground): dry-run endpoint for soul drafts`.

---

### 10. Manager.go refactor: split StartContainerWithIP

**Problem.** `internal/sandbox/manager.go::StartContainerWithIP` is ~600 LOC of inline switch-on-type + mount assembly + env injection + goroutine spawn. The composition race (PR #24) and the SOUL.md path mismatch (PR #29) were both made harder to debug by the function's sprawl.

**Solution.** Extract per-step helpers:

```go
func (m *Manager) buildBasePodSpec(opts process.StartOptions) (*corev1.Pod, error)
func (m *Manager) applyHermesConfig(spec *corev1.Pod, opts ...) error
func (m *Manager) applyOpenclawConfig(spec *corev1.Pod, opts ..., composition *ResolvedComposition) error
func (m *Manager) applyWorkspaceVolumes(spec *corev1.Pod, opts ...) error
func (m *Manager) applyCompositionMounts(spec *corev1.Pod, composition *ResolvedComposition) error
func (m *Manager) applySkillMounts(spec *corev1.Pod, opts ...) error
```

`StartContainerWithIP` becomes a 30-line orchestrator. Each helper testable in isolation.

**Scope.** ~300 LOC refactor (mostly move/rename). Risk: regressions in mount ordering. Mitigated by #4 (integration tests landing first).

**Dependencies.** Land #4 first so regressions surface in CI.

**Why prioritize.** Every future provider/skill mount feature lands on the back of this function. Pay the refactor cost once.

**PR shape.** `refactor(sandbox): split StartContainerWithIP into composable apply helpers`.

---

## Tier 3 — operational hardening

### 11. CI/CD automation (GitHub Actions)

**Problem.** Every deploy in this session was manual: `docker build` → `docker push` → bump `values-dev-eks.yaml` → `helm upgrade`. 8 cycles in one sprint, each ~5 min, each a chance for human error (forgot the values bump, wrong tag, etc.).

**Solution.** Three workflow files in `.github/workflows/`:

1. `ci.yml` — on every PR:
   - `go build -tags goolm ./...`
   - `go vet -tags goolm ./...`
   - `go test -tags goolm ./...`
   - `cd web && pnpm openapi:gen && pnpm build`
   - Required check for merge.

2. `image-build.yml` — on push to `main`:
   - For each of the 12 image keys (using `scripts/build/build-one.sh`):
     - Build with `--platform linux/amd64`
     - Push as `<image>:sha-<short>` AND `<image>:main`
   - Output ECR sha256 digests for traceability.

3. `deploy-dev.yml` — manual dispatch with `image_tag` input:
   - Updates `values-dev-eks.yaml` with the chosen tag
   - Opens a PR with the bump (auto-approve via bot if author is `github-actions[bot]`)
   - On merge: triggers `helm upgrade` via OIDC role into EKS

**Scope.** ~200 LOC YAML across 3 files + IAM role for OIDC.

**Dependencies.** `scripts/build/` (already done). AWS OIDC trust setup for the GitHub Actions ID provider in the AWS account (one-time IAM change).

**Why prioritize.** Removes 80% of the operational tax. Frees the developer to iterate on features instead of plumbing.

**PR shape.** `infra: GitHub Actions for build + image push + dev deploy`.

---

### 12. Ephemeral ConfigMap orphan reaper

**Problem.** Ephemeral skill/soul ConfigMaps cascade-delete via the sandbox row's `ON DELETE CASCADE` FK. But: sandbox CRD deleted out-of-band (`kubectl delete agentsandbox <name>` directly, no API call), sandbox row stays + ConfigMap orphans. Over time the workspace namespace accumulates dead `agentserver-draft-*` ConfigMaps.

**Solution.** Add to `internal/server/playground_test_sandbox.go::StartPlaygroundReaper` (or new goroutine):

- Every 5 min, list ConfigMaps in all `agent-ws-*` namespaces with label `agentserver.io/ephemeral=true`.
- For each, parse `agentserver.io/sandbox-id` label.
- If no matching `sandboxes` row exists → delete ConfigMap.

**Scope.** ~50 LOC.

**Dependencies.** Labels already emitted by `composition.go` (PR #18).

**Why prioritize.** Operational hygiene. Without it, namespaces grow forever.

**PR shape.** `feat(ops): reaper for orphaned ephemeral ConfigMaps`.

---

### 13. WhatsApp HMAC enforced mode

**Problem.** PR #10 added HMAC `X-Hub-Signature-256` verification but it's opt-in (env `WHATSAPP_APP_SECRET` empty = skip). Production should refuse to boot without the secret set — silent skip is dangerous.

**Solution.** Add `whatsapp.hmacRequired: true` to `deploy/helm/agentserver/values.yaml` (default `false` for backward compat). When true:

- `handleWhatsAppWebhookInbound` returns 503 if `WHATSAPP_APP_SECRET` env is empty (refuse to process).
- Startup log line: `WhatsApp HMAC verification: REQUIRED` or `OPTIONAL (dev mode)`.

Per-env override in `values-prod.yaml` (when prod values exist) sets `hmacRequired: true`.

**Scope.** ~10 LOC.

**Dependencies.** None.

**Why prioritize.** Security gap that costs nothing to close.

**PR shape.** `fix(whatsapp): enforced HMAC mode via values flag`.

---

### 14. Drafts audit log

**Problem.** `skill_drafts` + `soul_drafts` have only `updated_at`. Can't answer "who edited this draft and when?". When a draft breaks production via promote, no trail to root-cause.

**Solution.** Migration 034:

```sql
CREATE TABLE draft_audit_events (
    id SERIAL PRIMARY KEY,
    draft_kind TEXT NOT NULL,
    draft_id TEXT NOT NULL,
    actor_user_id TEXT NOT NULL REFERENCES users(id),
    action TEXT NOT NULL,       -- created | patched | archived | promoted | dry-run | test-sandbox
    payload_diff JSONB,         -- for patches: { files: { "path": "added|modified|removed" } }
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_draft_audit_kind_id ON draft_audit_events(draft_kind, draft_id);
CREATE INDEX idx_draft_audit_actor ON draft_audit_events(actor_user_id);
```

Each playground handler appends an event. Frontend renders a timeline tab.

**Scope.** ~100 LOC backend + ~50 LOC frontend timeline.

**Dependencies.** None.

**Why prioritize.** Compliance + debug. Cheap to add now, hard to retrofit later.

**PR shape.** `feat(playground): draft audit log`.

---

### 15. Staging cluster

**Shipped implementation.** Namespace `agentserver-staging` created on the existing dev EKS cluster (`dev-ti-eks-analytics-platform`) to avoid a full cluster bootstrap. `values-staging-eks.yaml` added at repo root, mirroring prod-like config. Image tag at time of ship: `sprint5-final`.

**Problem.** Today: dev EKS (`dev-ti-eks-analytics-platform`) → ??? → prod. No middle environment. First prod deploy ever will also be first "non-dev" deploy.

**Solution.** New EKS cluster `staging-ti-eks-analytics-platform` (or share namespace `agentserver-staging` on dev cluster if budget tight). `values-staging-eks.yaml` mirrors prod config (HMAC required, real WhatsApp creds, etc.) but with synthetic data only. CI workflow promotes from dev → staging after smoke pass.

**Scope.** Infra-heavy; mostly Pulumi/Terraform diff. ~200 lines of new chart config + cluster bootstrap.

**Dependencies.** #11 (CI/CD).

**Why prioritize.** Production readiness gate.

**PR shape.** `infra: staging cluster + values-staging-eks.yaml + CI promotion`.

---

## Tier 4 — strategic

### 16. OpenClaw plugin-sdk initContainer symlink

**Problem.** Documented in `docs/openclaw-skill-slash-research.md` (Option B). Skills today can't use the OpenClaw plugin-sdk (`openclaw/plugin-sdk/core`) because node_modules don't resolve from `/home/agent/.openclaw/extensions/<skill>/`. Slash command native, typed tools registration, all blocked.

**Solution.** Per the research doc:

1. Add EmptyDir volume `openclaw-sdk-links` in the openclaw pod spec.
2. initContainer `link-sdk` (busybox): for each `/home/agent/.openclaw/extensions/<skill>/`, create `<skill>/node_modules/openclaw` → symlink to `/app/node_modules/openclaw` (the image-baked SDK).
3. Mount EmptyDir into the agent container at `/home/agent/.openclaw/extensions/<skill>/node_modules/`.

After this, skill `index.mjs` can `import { definePluginEntry, createChatChannelPlugin } from "openclaw/plugin-sdk/core"` natively.

**Scope.** ~80 LOC in `internal/sandbox/manager.go` (build initContainer + EmptyDir).

**Dependencies.** Pin openclaw image by digest (already done in `values-dev-eks.yaml`). Smoke test against image bumps.

**Why prioritize.** Unlocks **real** slash commands + tool registration for OpenClaw skills. The biggest functionality gap in the OpenClaw side of playground.

**Risk.** Upstream openclaw image relocates `node_modules` between releases → symlink breaks. Mitigation: smoke test in CI.

**PR shape.** `feat(openclaw): initContainer symlinks plugin-sdk into skill extensions/`.

---

### 17. Tenant-scoped catalog

**Problem.** Playground MVP is global. Tenant A's drafts visible to Tenant B. Promote PR opens against a single repo regardless of tenant. Not multi-tenant safe.

**Solution.** Migration 035:

```sql
ALTER TABLE skill_drafts ADD COLUMN workspace_id TEXT REFERENCES workspaces(id);
ALTER TABLE soul_drafts ADD COLUMN workspace_id TEXT REFERENCES workspaces(id);
-- NULL = system template (visible to all)
```

Handlers:
- `GET /api/playground/{skills,souls}` filters by `workspace_id IS NULL OR workspace_id IN (caller's workspaces)`
- `POST` requires `workspace_id` in body (or defaults to caller's first workspace)
- Promote PR generator uses per-workspace branch prefix (`playground/ws-<wid>/skill-<name>-<id>`)

UI: catalog page gets a scope filter ("System" / current workspace).

**Scope.** ~120 LOC + migration + frontend filter.

**Dependencies.** Probably want #14 (audit log) live first for accountability.

**Why prioritize.** Required before opening playground to multiple tenants. Without it, the platform is single-tenant by accident.

**PR shape.** `feat(playground): tenant-scoped catalog`.

---

### 18. Soul/skill marketplace (cross-tenant sharing)

**Shipped implementation.**
- Migration 036 adds `visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','shared'))` to both `skill_drafts` and `soul_drafts`.
- New read endpoints (any authenticated user): `GET /api/marketplace/skills`, `GET /api/marketplace/souls`.
- Fork endpoints: `POST /api/marketplace/skills/{id}/fork`, `POST /api/marketplace/souls/{id}/fork` — copies the source draft into the caller's workspace as `private`.
- Admin-only visibility toggle: `PATCH /api/admin/playground/skills/{id}/visibility`, `PATCH /api/admin/playground/souls/{id}/visibility`.
- Frontend `/marketplace` page with Fork button per entry.

**Problem.** After #17, tenants are isolated. Useful skills (cobranca-like patterns) get reinvented per tenant. Lost network effect.

**Solution.** Add `visibility` column to drafts: `private` (default) | `shared` (visible to all tenants but not editable). Marketplace page lists `shared` templates from all workspaces. Forking copies to current workspace as `private`.

Moderation: platform admin role (already exists via `users.role='admin'`) can flag/remove shared templates.

**Scope.** ~250 LOC including UI marketplace page + admin moderation tools.

**Dependencies.** #17 (scope) + #14 (audit) live.

**Why prioritize.** Network effect when tenant count > 5. Below that, premature.

**PR shape.** `feat(playground): marketplace + cross-tenant template sharing`.

---

### 19. LLM proxy token resolution

**Problem.** `callLLMProxyForDryRunForUser` (PR #23) picks "user's first workspace" to mint the proxy token. If user has 5 workspaces with different LLM quotas/BYOK configs, this is arbitrary.

**Solution.** Accept `workspace_id` in the dry-run body:

```json
{
  "soul_ref": "draft:xxx",
  "user_message": "...",
  "workspace_id": "ws-uuid"
}
```

If absent → fall back to first workspace (legacy behavior). Frontend dry-run panel gets a workspace dropdown when user is in >1 workspace.

**Scope.** ~20 LOC backend + 30 LOC frontend dropdown.

**Dependencies.** None.

**Why prioritize.** Sharp edge in production. Cheap to fix.

**PR shape.** `feat(playground): workspace_id in dry-run body for explicit LLM scope`.

---

### 20. Drop legacy `sandboxes.im_channel_id` FK

**Shipped implementation.**
- Migration 037 drops `sandboxes.im_channel_id`.
- `BindSandboxToChannel` and `UnbindSandboxFromChannel`: dual-write to the column removed.
- `GetSandboxForChannel` and `GetIMChannelForSandbox`: FK-fallback read paths removed; all routing now goes exclusively through `sandbox_channel_bindings`.

**Problem.** PR #3 introduced the N:M junction table. The FK lived for backward compat + dual-write. Has been dual-written for weeks of dev EKS time + zero data loss observed.

**Solution.** Migration 037 (renumbered from 036 — see #18):

```sql
-- After confirming all readers use junction first (manual audit):
ALTER TABLE sandboxes DROP COLUMN im_channel_id;
```

Update `internal/db/im_channels.go`:
- `GetSandboxForChannel` drops the fallback branch (junction-only)
- `BindSandboxToChannel` drops the FK write
- `UnbindSandboxFromChannel` drops the FK clear

**Scope.** ~30 LOC removal + migration.

**Dependencies.** Production runtime for N weeks (per design doc §16). Currently we have ~weeks of dev runtime only. Wait for prod evidence.

**Why prioritize.** Tech debt cleanup. Defer until production.

**PR shape.** `chore(db): drop legacy sandboxes.im_channel_id FK`.

---

## Tier 5 — would be nice

Bullet form — these aren't priority enough for a detailed entry yet.

- **Monaco / CodeMirror editor in playground** — syntax highlighting beats textarea. ~150 LOC + ~50 KB bundle.
- **OpenAPI spec auto-gen for playground endpoints** — `swag init` annotations + CI check. ~30 LOC swag comments.
- **Hot-reload in playground editor** — save = restart attached test sandbox. ~80 LOC frontend + backend hook.
- **Multi-model dry-run picker** — let user pick Sonnet/Opus/Haiku per dry-run. ~40 LOC.
- **Skill template gallery / fork** — UI "fork this template" button that copies a system template into a tenant draft. ~80 LOC after #17.
- **Per-tenant Prometheus dashboard** — fork the #6 dashboard with workspace labels. ~30 lines JSON.
- **Soul/skill versioning with semver auto-bump** — semantic version field on drafts, promote bumps minor automatically. ~60 LOC.
- **WhatsApp media messages** — image/audio/document inbound (downloads media via Graph API). ~150 LOC.
- **WhatsApp status events** — delivered/read/failed → metrics. ~40 LOC.
- **Skill marketplace ratings** — :+1: count on shared templates. ~100 LOC.

---

## Suggested 1-sprint (1 week) plan

Land Tier 1 in this order:

1. #2 magic strings → constants (refactor safety net)
2. #4 integration tests (regression safety)
3. #1 composition picker (visible UX)
4. #3 rate limits (security)
5. #5 OpenClaw SOUL.md equivalent — **done** (native `workspace/SOUL.md` bootstrap + plugin-sdk #49)

**Total ~460 LOC**, 5 PRs, deploys per PR.

After sprint 1, telemetry (#6) gives visibility into what users actually do. That data shapes Sprint 2.

## Suggested 1-month plan

Sprint 1 (above) + Tier 2 full (#6 → #10) + Tier 3 #11 (CI) + #12 (orphan reaper).

After 1 month: playground is feature-complete for v1 production. Tenant scope (#17) becomes the next milestone, gating a multi-tenant beta.

---

## What we deliberately leave out

These came up in design but aren't going to ship from this list:

- **Bake skills into a custom OpenClaw image** — operationally impossible at multi-tenant scale (per-tenant images). Use #16 (initContainer symlink) instead.
- **Behavior-tree YAML for soul body** — wrong abstraction for LLM agents. Prose persona stays.
- **Soul as workspace-level metadata** — collapses identity into capability; loses the composition reuse value. Per design Appendix A.6.

---

## References

- `docs/playground-design.md` — original design + Appendix A (alternatives considered)
- `docs/lessons-learned.md` — iterative discoveries from sprints
- `docs/openclaw-skill-slash-research.md` — Option A-D analysis for plugin-sdk
- `docs/multi-channel-routing.md` — N:M schema + routing
- `docs/whatsapp-cloud-integration.md` — Meta Cloud webhook + HMAC
- `RELEASE.md` — multi-channel routing release notes
- `skills/agentserver-helper/SKILL.md` — repo guide for future agents
