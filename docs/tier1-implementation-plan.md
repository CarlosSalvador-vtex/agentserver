# Tier 1 Implementation Plan

> Actionable PR-by-PR breakdown for the 5 high-impact-low-cost items
> in [`docs/improvements.md`](improvements.md). Target: 1 sprint
> (~460 LOC, 5 PRs), deploys per PR. Each PR section carries scope,
> file list, code stubs, acceptance criteria, risks, and ordering
> rationale.
>
> Order matters here — items #2 + #4 land FIRST as safety nets, then
> the user-visible features go on top. See "Dependency graph" below.

## Headline goals

By end of sprint, the agentserver fork has:
1. **Visible** playground entry from sandbox create flow (composition picker).
2. **Safe** refactors — magic strings replaced by typed constants without regressions.
3. **Guarded** LLM cost / cluster pressure — per-user rate limits on dry-run + test-sandbox.
4. **Regression-proof** composition resolution — integration tests against fake k8s + DB.
5. **No dead mount** — OpenClaw soul.md actually reachable by the agent.

Acceptance for the sprint: dev EKS smoke shows a brand-new user
land on Sandboxes → click "Create sandbox" → pick a draft soul + skill
from a dropdown → pod boots with both mounted + reachable by the LLM,
all without touching curl. Plus: 5 new integration tests + 0 rate-limit
incidents in dry-run logs.

---

## Dependency graph

```
Sprint day 1-2          day 3-4           day 5
─────────────────       ───────           ─────
PR-A #2 constants ────► PR-B #4 tests ───► PR-D #1 composition picker
       (refactor)              (CI gate)         (UI feature)
                                                       │
PR-C #3 rate limits ─────────────────────────────► (parallel)
       (middleware)                                    │
                                                       ▼
                                                PR-E #5 OpenClaw SOUL
                                                       (closes loop)
```

- PR-A first: every later PR touches the same magic strings.
- PR-B second: future regressions in PR-D + PR-E land on a tested base.
- PR-C parallel: independent surface (middleware), no overlap.
- PR-D before PR-E: composition picker exercises the OpenClaw mount path PR-E fixes.

---

## PR-A — Typed constants (Tier 1 #2)

**Title:** `refactor: typed constants for sandbox + provider + ref kinds`

**Why first.** Every later PR touches the same magic strings. Landing constants early avoids merge conflicts + lets reviewers focus on logic, not string churn.

**Files (~80 LOC):**
- `internal/sandbox/types.go` (new) — `SandboxType`, `RefKind`, `ProviderKind` enums + Valid() helpers.
- `internal/sandbox/manager.go` — replace `case "openclaw":`, `case "hermes":`, etc.
- `internal/sandbox/composition.go` — replace `"git"`, `"draft"`, `"openclaw"`, `"hermes"`.
- `internal/server/server.go::handleCreateSandbox` — validate via `SandboxType.Valid()`.
- `internal/server/playground_test_sandbox.go` — same.
- `internal/imbridge/*_provider.go` — `Name()` returns const.

**Code stub:**
```go
// internal/sandbox/types.go
package sandbox

type SandboxType string

const (
    SandboxTypeOpencode   SandboxType = "opencode"
    SandboxTypeOpenclaw   SandboxType = "openclaw"
    SandboxTypeNanoclaw   SandboxType = "nanoclaw"
    SandboxTypeClaudeCode SandboxType = "claudecode"
    SandboxTypeJupyter    SandboxType = "jupyter"
    SandboxTypeHermes     SandboxType = "hermes"
)

func (s SandboxType) Valid() bool {
    switch s {
    case SandboxTypeOpencode, SandboxTypeOpenclaw, SandboxTypeNanoclaw,
         SandboxTypeClaudeCode, SandboxTypeJupyter, SandboxTypeHermes:
        return true
    }
    return false
}

type RefKind string

const (
    RefKindGit   RefKind = "git"
    RefKindDraft RefKind = "draft"
)

type ProviderKind string

const (
    ProviderWeixin   ProviderKind = "weixin"
    ProviderTelegram ProviderKind = "telegram"
    ProviderMatrix   ProviderKind = "matrix"
    ProviderWhatsApp ProviderKind = "whatsapp"
)
```

**Acceptance:**
- [ ] `go build -tags goolm ./...` clean.
- [ ] `go vet -tags goolm ./...` clean.
- [ ] Existing `composition_test.go` + `playground_promote_test.go` pass without modification.
- [ ] `grep -nE '"openclaw"|"hermes"|"opencode"|"nanoclaw"' internal/ | wc -l` → 0 in `.go` files (only in JSON test fixtures or migrations).

**Risks:**
- Hermes / OpenClaw image labels in YAML aren't affected (string match in K8s API stays as string literal). OK to leave those.
- Frontend keeps strings. TS doesn't share Go constants — out of scope for this PR.

**Estimated effort:** 0.5 day. Bulk is find/replace + 1 new file.

---

## PR-B — Integration tests for composition resolution (Tier 1 #4)

**Title:** `test(sandbox): integration coverage for composition resolution + provision race`

**Why second.** Locks in the contract before PR-D + PR-E touch the same code paths. Race repro (PR #24) lives forever as a test instead of tribal memory.

**Files (~120 LOC tests + helpers):**
- `internal/sandbox/composition_integration_test.go` (new)
- `internal/server/playground_provision_integration_test.go` (new)
- `internal/db/testhelper_test.go` (new or extend existing) — `setupTestDB(t)` helper using `pgx` against `postgres://postgres@localhost/test` OR `sqlmock` for the simpler queries.

**Test cases:**

```go
// composition_integration_test.go
func TestResolveComposition_DraftSoulAndSkill(t *testing.T) {
    // Setup: insert soul_drafts + skill_drafts rows + sandbox_compositions row
    // Call: m.ResolveComposition(ctx, "test-sbx", "test-ns", "openclaw")
    // Assert:
    //   - 2 EphemeralConfigMaps (soul + skill)
    //   - SoulBody == soul body content
    //   - ExtraMounts includes /home/agent/.openclaw/soul.md
    //   - ExtraMounts includes /home/agent/.openclaw/extensions/<skill>/prompt.md
    //   - EnabledSkillNames == [skill name]
}

func TestResolveComposition_GitRefsAreNoop(t *testing.T) {
    // Verify git: refs don't create ephemeral CMs (chart path)
}

func TestResolveComposition_MissingSandboxComposition(t *testing.T) {
    // Empty resolution when no row exists
}

// playground_provision_integration_test.go
func TestProvisionSandbox_CompositionPersistedBeforeGoroutine(t *testing.T) {
    // PR #24 race repro:
    // 1. Call provisionSandbox with Composition field
    // 2. Block goroutine via injected channel
    // 3. Query sandbox_compositions BEFORE unblocking goroutine
    // 4. Assert row exists
}
```

**Acceptance:**
- [ ] All 4 tests pass.
- [ ] `go test -tags goolm -race ./internal/sandbox/ ./internal/server/` clean.
- [ ] CI workflow (when #11 lands) picks up these tests.

**Risks:**
- Postgres dep adds CI complexity. Mitigate: prefer `sqlmock` for queries that don't need real Postgres semantics (JSONB ops do; advisory locks don't matter here).
- Fake k8s client doesn't validate label selectors the same way real K8s does. Acceptable trade-off — we test the call path, not selector parsing.

**Estimated effort:** 1 day. Most work is fixture setup; the tests themselves are short.

---

## PR-C — Rate limit dry-run + test-sandbox (Tier 1 #3)

**Title:** `feat(playground): per-user rate limit on dry-run + test-sandbox`

**Why parallel.** Touches only middleware — no overlap with the constants refactor or composition resolution. Can land any time after PR-A.

**Files (~50 LOC):**
- `internal/server/playground_ratelimit.go` (new) — limiter map + middleware.
- `internal/server/server.go` — wrap handlers in middleware.

**Code stub:**
```go
// internal/server/playground_ratelimit.go
package server

import (
    "net/http"
    "strconv"
    "sync"
    "time"

    "golang.org/x/time/rate"

    "github.com/agentserver/agentserver/internal/auth"
)

type rateLimitBucket struct {
    limiter *rate.Limiter
    lastUse time.Time
}

type playgroundRateLimiter struct {
    mu      sync.Mutex
    buckets map[string]*rateLimitBucket
    rps     rate.Limit
    burst   int
}

func newPlaygroundRateLimiter(rps rate.Limit, burst int) *playgroundRateLimiter {
    rl := &playgroundRateLimiter{
        buckets: make(map[string]*rateLimitBucket),
        rps:     rps,
        burst:   burst,
    }
    go rl.evictLoop()
    return rl
}

func (rl *playgroundRateLimiter) allow(userID string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    b, ok := rl.buckets[userID]
    if !ok {
        b = &rateLimitBucket{limiter: rate.NewLimiter(rl.rps, rl.burst)}
        rl.buckets[userID] = b
    }
    b.lastUse = time.Now()
    return b.limiter.Allow()
}

func (rl *playgroundRateLimiter) evictLoop() {
    t := time.NewTicker(5 * time.Minute)
    defer t.Stop()
    for range t.C {
        rl.mu.Lock()
        cutoff := time.Now().Add(-10 * time.Minute)
        for uid, b := range rl.buckets {
            if b.lastUse.Before(cutoff) {
                delete(rl.buckets, uid)
            }
        }
        rl.mu.Unlock()
    }
}

func (rl *playgroundRateLimiter) middleware(retryAfterSec int, next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        userID := auth.UserIDFromContext(r.Context())
        if !rl.allow(userID) {
            w.Header().Set("Retry-After", strconv.Itoa(retryAfterSec))
            http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
            return
        }
        next(w, r)
    }
}
```

**Wiring in server.go:**
```go
// Initialise in Server struct init
s.dryRunLimiter = newPlaygroundRateLimiter(rate.Every(6*time.Second), 3)  // 10/min, burst 3
s.testSandboxLimiter = newPlaygroundRateLimiter(rate.Every(20*time.Second), 1)  // 3/min, burst 1

// In route registration
r.Post("/api/playground/skills/{id}/dry-run", s.dryRunLimiter.middleware(6, s.handleSkillDraftDryRun))
r.Post("/api/playground/skills/{id}/test-sandbox", s.testSandboxLimiter.middleware(20, s.handleSkillDraftTestSandbox))
```

**Acceptance:**
- [ ] 11 consecutive `POST /dry-run` calls in <60s → 11th returns 429 with `Retry-After: 6` header.
- [ ] 4 consecutive `POST /test-sandbox` calls in <60s → 4th returns 429 with `Retry-After: 20`.
- [ ] Quota burst regenerates after window (test with `time.Sleep`).
- [ ] Other endpoints unaffected (e.g. PATCH skill draft still unlimited).

**Risks:**
- `rate.Limiter` is in-memory only — multi-replica agentserver lets a user burst per-replica. Acceptable trade-off for MVP; revisit with Redis bucket if scale-out demands it.
- Eviction loop adds 1 goroutine. Negligible.

**Estimated effort:** 0.5 day.

---

## PR-D — Composition picker in sandbox create modal (Tier 1 #1)

**Title:** `feat(ui): composition picker in sandbox create modal`

**Why this order.** Lands after PR-A (constants) so the modal can import `SandboxType` from a generated TS file (stretch — see "Stretch goal" below). Lands before PR-E so the QA flow has a UI to exercise.

**Files (~150 LOC):**
- `web/src/components/CreateSandboxModal.tsx` — extend with a collapsible "Composition" section.
- `web/src/components/CompositionPicker.tsx` (new) — reusable picker component.
- `web/src/lib/api.ts` — add `listAllPlaygroundSkillsAndSouls()` helper that merges drafts + git templates.

**Component shape:**
```tsx
// web/src/components/CompositionPicker.tsx
interface CompositionPickerProps {
  value: SandboxComposition | null
  onChange: (next: SandboxComposition | null) => void
}

interface SandboxComposition {
  soul?: string                    // "git:name@sha" | "draft:uuid"
  skills?: string[]                // same grammar
  config?: Record<string, Record<string, unknown>>
  track_upstream?: boolean
}

export function CompositionPicker({ value, onChange }: CompositionPickerProps) {
  const [open, setOpen] = useState(false)
  const [soulOptions, setSoulOptions] = useState<RefOption[]>([])
  const [skillOptions, setSkillOptions] = useState<RefOption[]>([])

  useEffect(() => {
    if (!open) return
    Promise.all([
      listPlaygroundSouls().then(asRefOptions),
      listPlaygroundSkills().then(asRefOptions),
      // TODO: listGitTemplates() when chart-side endpoint lands
    ]).then(([s, sk]) => { setSoulOptions(s); setSkillOptions(sk) })
  }, [open])

  return (
    <div className="...">
      <button onClick={() => setOpen(!open)}>Composition (optional) ▾</button>
      {open && (
        <>
          <SoulSelect value={value?.soul} options={soulOptions} onChange={...} />
          <SkillMultiSelect value={value?.skills} options={skillOptions} onChange={...} />
          <TrackUpstreamToggle value={value?.track_upstream} onChange={...} />
        </>
      )}
    </div>
  )
}
```

**Modal integration:**
```tsx
// CreateSandboxModal.tsx
const [composition, setComposition] = useState<SandboxComposition | null>(null)

const handleCreate = async () => {
  await createSandbox(workspaceId, {
    name, type, cpu, memory,
    composition: composition ?? undefined,
  })
}

return (
  <Modal>
    {/* existing fields */}
    <CompositionPicker value={composition} onChange={setComposition} />
    <button onClick={handleCreate}>Create</button>
  </Modal>
)
```

**Acceptance:**
- [ ] Create sandbox via UI with no composition → behaves as today.
- [ ] Create with `draft:<soul-uuid>` + `draft:<skill-uuid>` → DB row in `sandbox_compositions` exists post-create.
- [ ] Pod boots with `/opt/data/SOUL.md` (hermes) or `/home/agent/.openclaw/extensions/<skill>/` populated.
- [ ] `pnpm openapi:gen && pnpm build` clean.

**Risks:**
- Per-skill config inputs (`configSchema` rendering) are complex — for first PR, ship without them (config sent as `{}`). Note in PR description as follow-up.

**Stretch goal:**
- Code-gen TS constants from Go `SandboxType` enum so PR-A's constants are shared. Skip if it adds >30 min — landed as future PR.

**Estimated effort:** 1.5 days.

---

## PR-E — OpenClaw SOUL.md equivalent (Tier 1 #5)

**Title:** `feat(openclaw): wire soul.md into agent system prompt`

**Why last.** Needs PR-D to exercise the path (creating an openclaw sandbox with composition via UI). Option C (env probe) is the simplest path to ship; Options A + B are docs-only deferrals.

**Files (~30-80 LOC depending on option):**

**Option C (recommended, ~30 LOC):**
- `internal/sandbox/manager.go` — emit `OPENCLAW_SOUL_FILE=/home/agent/.openclaw/soul.md` env var on openclaw sandboxes when composition has a SoulBody.
- Probe the running pod for any OpenClaw config knob that reads it.
- If found: document in `docs/lessons-learned.md` (analogous to the Hermes SOUL.md discovery).
- If not found: document the gap + recommend Option A (skill index.mjs reads the file at register) in `docs/lessons-learned.md`.

**Option A (~80 LOC, blocked on Tier 4 #16):**
- Skill template's `index.mjs` reads `/home/agent/.openclaw/soul.md` at module init (via `fs.readFileSync`).
- Prepends contents to the system prompt via the plugin-sdk API (requires #16 to be landed).
- Update `deploy/helm/agentserver/skills/cobranca/index.mjs` as the reference.

**Code stub (Option C):**
```go
// manager.go, openclaw branch
if composition.SoulBody != "" {
    containerEnv = append(containerEnv, corev1.EnvVar{
        Name:  "OPENCLAW_SOUL_FILE",
        Value: "/home/agent/.openclaw/soul.md",
    })
    containerEnv = append(containerEnv, corev1.EnvVar{
        Name:  "AGENTSERVER_SOUL_BODY",
        Value: composition.SoulBody,
    })
    // Fallback: ship body via env in case OpenClaw doesn't read the file
}
```

**Verification probe (manual, in dev EKS):**
```bash
NS=agent-ws-7afe5449
SBX=agent-sandbox-<id>
kubectl exec -n "$NS" "$SBX" -c agent -- bash -c '
  echo "=== mount ===" ; cat /home/agent/.openclaw/soul.md | head -5 ;
  echo "=== env ===" ; env | grep -i soul ;
  echo "=== loader log ===" ; grep -i soul /tmp/openclaw/openclaw-*.log | head ;
  echo "=== probe response ===" ; openclaw agent --to +5527996073736 -m "quem é você?" --thinking off 2>&1 | tail -10'
```

**Acceptance:**
- [ ] Openclaw sandbox booted with draft soul has soul.md mounted (already works post-PR #18).
- [ ] EITHER the agent picks up the persona (Option C if env knob exists) OR `docs/lessons-learned.md` carries the next-step recommendation (Option A path).
- [ ] No more "dead mount" — the mounted file is either read by the runtime or there's a documented plan to make it readable.

**Risks:**
- OpenClaw upstream may not have any SOUL.md equivalent. Then Option C is no-op + we document the gap and call this PR "scoping" rather than "fix".
- Tier 4 #16 (initContainer symlink) is the real fix. This PR is a placeholder + investigation.

**Estimated effort:** 0.5-1 day depending on probe outcome.

---

## Cross-PR concerns

### Deploy cadence

Each PR deploys to dev EKS via the existing flow:
1. PR merged
2. `./scripts/build/build-one.sh agentserver tier1-prN`
3. Bump `values-dev-eks.yaml` tag
4. `helm upgrade ... -f values-dev-eks.yaml`
5. Smoke per PR's acceptance criteria

After PR-D + PR-E land, deploy one consolidated tag (`tier1-final`) and run the full sprint acceptance scenario.

### CI integration

If Tier 3 #11 (CI/CD) lands during this sprint, PR-B's tests run automatically on every PR. Without it, run locally before merge.

### Documentation updates

After each PR lands:
- Update `docs/lessons-learned.md` with any new sharp edges found.
- Update `RELEASE.md` (if maintained) with the user-visible changes.
- Update `skills/agentserver-helper/SKILL.md` if API surface changed.

### Rollback plan per PR

- **PR-A**: pure refactor, no schema. Revert PR if grep finds residual magic strings; redo.
- **PR-B**: tests only, no runtime impact.
- **PR-C**: middleware. Revert by removing the wrap call in `server.go` — handlers stay intact.
- **PR-D**: feature flag-able via UI condition. If frontend bugs, revert the modal change without DB rollback.
- **PR-E**: env var only. Unset to revert behavior.

### What we deliberately leave out of Tier 1

- Composition picker per-skill config schema rendering (defer to PR after Tier 1).
- TS constants shared with Go (stretch goal, may slip).
- Frontend tests (no test infra in `web/` yet — deferred).
- Workspace-aware soul ref (already in playground-design.md backlog).

---

## Sprint timeline (5 working days)

| Day | PR | Owner notes |
|---|---|---|
| Mon | PR-A — typed constants | Pair-friendly. Half day. |
| Mon-Tue | PR-B — integration tests | Postgres dep setup is the unknown. Block on a real DB if `sqlmock` proves insufficient. |
| Tue | PR-C — rate limits | Independent, can ship in parallel with PR-B. |
| Wed-Thu | PR-D — composition picker | UI-heavy. 1.5 days reasonable. |
| Thu-Fri | PR-E — OpenClaw SOUL.md | Probe first (1h), then commit to Option C or document Option A. |
| Fri | Sprint acceptance + deploy | Run the full E2E: brand-new user creates draft → attaches via picker → pod boots → persona answers. |

Slip risk: PR-B's Postgres dep + PR-D's `configSchema` rendering. Mitigate by deferring `configSchema` to a follow-up + using `sqlmock` aggressively in PR-B.

---

## Definition of done

End of sprint, all five PRs merged + dev EKS rev shows:
- Catalog page reachable via TopBar shortcut (already in place post-PR #30).
- Create Sandbox modal has Composition picker; `composition` field round-trips DB.
- New openclaw + hermes pods built via picker boot with persona active (hermes confirmed via `hermes -z`, openclaw via Option C probe or A path documented).
- 11 dry-runs in 60s → 11th 429s with proper header.
- `go test ./internal/sandbox/...` runs ≥4 new integration tests, all green.
- `grep -rE '"openclaw"|"hermes"' internal/*.go` → 0 matches outside string literals in test fixtures.

---

## References

- `docs/improvements.md` — source roadmap (full 20-item backlog)
- `docs/playground-design.md` — design doc + sign-off matrix
- `docs/lessons-learned.md` — sharp edges to avoid re-discovering
- `docs/openclaw-skill-slash-research.md` — context for PR-E Option A path
- `skills/agentserver-helper/SKILL.md` — repo guide
- PRs #15-29 — playground series this sprint extends
