# Lessons Learned — agentserver fork

> Iterative discoveries from the multi-channel routing + WhatsApp +
> playground sprints. Each row captures a wrong assumption + how it
> manifested + the fix. Future contributors hitting the same wall
> should land here first.

## Format

| What we tried | What broke | What worked |
|---|---|---|
| The wrong assumption | The error / symptom | The fix |

Sources: PR numbers in `CarlosSalvador-vtex/agentserver`. Verify
details with `gh pr view <N> --repo CarlosSalvador-vtex/agentserver`.

---

## Hermes persona injection (the long one)

We hunted the right field for ~4 PRs. Hermes-agent has its own
canonical persona file path that bypasses config.yaml entirely.

### PR #26 — config.yaml inject as `agent.system_prompt`

**Tried**: rewrite per-sandbox config.yaml with `agent.system_prompt: |` block.

**Broke**: hermes-agent silently dropped the field. `hermes config show` rendered the default kawaii persona; `agent.max_turns` was demoted from our 80 to the upstream default 90 because the duplicate `agent:` block (last-wins) replaced the original.

**Why-not**: `agent.system_prompt` isn't in the hermes config schema. The schema's `agent.*` keys are `max_turns`, `terminal`, etc.

### PR #27 — root `personalities` dict + `personality` selector

**Tried**: emit top-level `personalities.<name>.system_prompt: |` + `personality: <name>` switch.

**Broke**: `hermes config show` still reported `Personality: kawaii`. The root `personality:` key was ignored.

**Why-not**: the personality selector lives under `display.personality`, not root. Root `personality:` is a no-op.

### PR #28 — `display.personality` selector

**Tried**: same persona dict, but switch via `display.personality: playground-soul`.

**Broke**: `hermes config show` now correctly printed `Personality: playground-soul`. But the LLM ignored the persona — `hermes -z "quem é você?"` answered as default Hermes ("Sou Hermes, agente de IA da Nous Research").

**Why-not**: `personalities.<name>` dict isn't the system_prompt source. It's only used by the `/personality` slash command for tone hints — the actual persona text comes from a different file.

### PR #29 — `/opt/data/SOUL.md` (HERMES_HOME convention)

**Tried**: drop config.yaml rewrite entirely. Mount soul body directly at `$HERMES_HOME/SOUL.md` (= `/opt/data/SOUL.md`, uppercase).

**Worked** ✅. Discovery via `grep -rE "SOUL.md" /opt/hermes/hermes_cli/*.py` in a running pod. Hermes-agent reads SOUL.md on every turn:

```
/opt/hermes/hermes_cli/doctor.py:988: SOUL.md exists but is empty — edit it to customize personality
/opt/hermes/hermes_cli/main.py:10152: Edit {profile_dir_display}/SOUL.md for different personality
```

Smoke:
```
$ hermes -z "Bom dia, quem é você?" --yolo
Bom dia! Sou a Julia, atendente de cobrança da Acme...
```

### Takeaways

- Hermes has a **literal SOUL.md convention**. Future persona injects should hit `$HERMES_HOME/SOUL.md` first.
- Stop trusting `config show` output — it prints values from the config tree, but doesn't tell you whether those values reach the LLM.
- When iterating against an opaque upstream image, **grep the image source** before guessing config fields. We burned 3 PRs on guesses; a single `grep -rE "personality|SOUL" /opt/hermes/**/*.py` would have found the answer in 60 seconds.

---

## OpenClaw plugin loader (PR #13 four-fixes-in-one + PR #25)

We also iterated through 5 wrong assumptions on the OpenClaw side.
PR #13 bundled the first four (they were caught in the same session);
PR #25 caught the fifth months later when we tried `agent.systemPrompt`
at root.

| Wrong assumption | What broke | Fix |
|---|---|---|
| Mount skill at `/home/node/.openclaw/extensions/<name>/` | OpenClaw container runs as user `agent`, scans `/home/agent/.openclaw/extensions/`. Mount landed outside the scan path. | Mount at `/home/agent/.openclaw/extensions/<name>/` (PR #13). |
| Emit `plugins.installs.<name> = {source: "path", installPath: ...}` | Loader treats `source: "path"` installs without integrity hash as "stale config entry" and skips the matching `entries.<name>` row. Plugin silently dangling. | Drop the `installs` row entirely. Auto-discovery finds the plugin via the extensions/ dir scan, the same path the bundled `openclaw-weixin` uses (PR #13). |
| Skill manifest filename = `plugin.json` with skill-style fields (name, entry, commands, tools) | Loader expects `openclaw.plugin.json` with `{id, configSchema}` shape. Pod boot failed with `manifest not found`. | Rename + reshape to `{id, version, channels, configSchema}` (PR #13). |
| `export default async function register(ctx)` | Loader logged "async registration is ignored" and silently dropped every `ctx.commands.register / ctx.tools.register` call. | Switch to a sync object literal `{id, register(api)}` matching `/app/extensions/openshell/index.ts`. Read prompt + JSON fixtures at module init via `fs.readFileSync` (PR #13). |
| Emit `agent.systemPrompt` at root of openclaw.json for soul body | Loader rejected with `<root>: Unrecognized key: "agent"`. Pod crash-looped. | Drop the inject. Soul.md mount alone is enough; agent reads it on demand (PR #25). The right field for system-prompt injection in OpenClaw is still unknown — likely needs `plugin-sdk` import to wire properly (see `openclaw-skill-slash-research.md`). |

### Takeaways

- OpenClaw plugin loader is **strict about the manifest shape**. Don't extrapolate from package.json conventions — read `/app/extensions/<bundled-plugin>/openclaw.plugin.json` for ground truth.
- Auto-discovery > installs rows. The `installs` map is for npm-published plugins with integrity hashes; for path-mounted skills, omit it.
- Sync register. Always sync. The "async registration ignored" log line is easy to miss.
- The root schema is **strict** — unknown keys break boot. Probe with a single bogus value to see the schema rejection list before adding new top-level fields.

---

## Composition race (PR #24)

**Tried**: `handleCreateSandbox` called `provisionSandbox` (which spawns the container-start goroutine) and **then** wrote `sandbox_compositions`. Assumed the goroutine wouldn't read composition until after the row was written.

**Broke**: goroutine started immediately, called `manager.ResolveComposition`, which queried `sandbox_compositions` — empty. Ephemeral ConfigMaps + soul mount silently skipped. The composition row landed after the pod was already booted with the wrong spec.

**Why-not**: Go goroutines are eager. No guaranteed happens-before between "function returns" and "caller's next statement runs" relative to "goroutine reads DB". Especially across HTTP handlers.

**Worked**: move `CreateSandboxComposition` **inside** `provisionSandbox`, before the goroutine spawn. Composition row is visible by the time the goroutine resolves it.

### Takeaway

When a goroutine reads DB state that the caller is also writing, write **before** the spawn. Better: make `provisionSandbox` accept the full input shape (including composition) and own the order. Don't sprinkle related writes across the goroutine boundary.

---

## LLM proxy auth (PR #23)

**Tried**: send `INTERNAL_API_SECRET` as `x-api-key` + `X-Internal-Secret` + `Bearer` headers to llmproxy for the dry-run path. Assumed the internal secret would be honored by llmproxy the same way it's honored by other internal endpoints.

**Broke**: llmproxy returned `401: invalid api key`. The internal secret is for agentserver-internal endpoints (`/internal/validate-proxy-token`, `/internal/workspace-token`), not for llmproxy.

**Why-not**: llmproxy validates bearers against the `workspace_tokens` catalog via `ValidateProxyToken` (see `internal/llmproxy/auth.go:23`). It doesn't accept `INTERNAL_API_SECRET`.

**Worked**: mint (or reuse) the calling user's first workspace's proxy token via `DB.GetOrCreateWorkspaceToken(workspaceID)`, send it as `x-api-key`. Fall back to a descriptive error when the user has no workspace membership.

### Takeaway

Auth is per-service. `INTERNAL_API_SECRET` is **not** a master key; it's just one of several auth modes. When a downstream service rejects with 401, find its actual validator before adding more headers. `grep -n "invalid api key" internal/<svc>/auth.go` usually surfaces it.

---

## OpenClaw soul.md (Tier 1)

OpenClaw has **no** Hermes-style auto-load of `soul.md`. The mount at `/home/agent/.openclaw/soul.md` is useless unless something reads it.

**What we ship (Tier 1):**

| Layer | Mechanism |
|---|---|
| Mount | Draft soul → ephemeral ConfigMap → `soul.md` at `/home/agent/.openclaw/soul.md` (unchanged) |
| Env (Option C) | `OPENCLAW_SOUL_FILE` + `AGENTSERVER_SOUL_BODY` when composition has a draft soul |
| Skill prompt (Option B) | Playground draft skills get a `prompt.md` preamble pointing at the soul file; chart skill `cobranca/index.mjs` reads soul at module init and prepends to prompt |

**Do not retry:** `agent.systemPrompt` at the root of `openclaw.json` — loader rejects with `<root>: Unrecognized key: "agent"` (PR #25).

**Next step (Tier 4 #16):** initContainer symlink + `plugin-sdk` import so skills can register a real system-prompt hook. See `docs/openclaw-skill-slash-research.md`.

**Verify on dev EKS:**

```bash
kubectl exec -n "$NS" "$SBX" -c agent -- bash -c '
  cat /home/agent/.openclaw/soul.md | head -5
  env | grep -i soul
'
```

---

## Soul/persona injection: native mount vs plugin-sdk (the decision rubric)

> **Supersedes the "OpenClaw soul.md (Tier 1)" section above.** That section
> assumed OpenClaw has *no* auto-load. The #47 image dive proved otherwise:
> OpenClaw **does** load `~/.openclaw/workspace/SOUL.md` on bootstrap, same
> convention as bundled auth-profiles. The legacy `/home/agent/.openclaw/soul.md`
> mount + `OPENCLAW_SOUL_FILE` env are dead unless something reads them.

We kept re-deriving "should this go through the plugin-sdk hook or just a
mounted file?" — across Hermes (#29), OpenClaw Tier 1, #47, #49, #55. The
answer is a one-line rule. Capturing it so the next persona PR doesn't re-run
the analysis.

### Rule

- **Static workspace persona → mount `SOUL.md`. Default.** The agent
  (Hermes `$HERMES_HOME/SOUL.md`, OpenClaw `~/.openclaw/workspace/SOUL.md`)
  reads it on boot. **Zero plugin code.** This is what shipped (#47, S4-PR1).
- **Dynamic behavior → plugin-sdk `before_prompt_build` hook.** Use only when
  you need templating, conditional/per-turn injection, function-calling tools,
  or first-class slash commands. This is what S4-PR4 (#55) did — for the
  **skill** persona (`prompt.md`), not the workspace soul.

### Why "Option A" (skill `index.mjs` reads soul + prepends via plugin-sdk) was dropped

`docs/improvements.md` #5 originally recommended Option A. **Not implemented**
— native bootstrap made it redundant. For a *static* file the SDK path adds
only cost, no gain:

| Axis | Native mount (shipped) | plugin-sdk inject (Option A) |
|---|---|---|
| Code | none | skill reads + prepends in hook |
| Resolvability | n/a | needs `openclaw/plugin-sdk/*` on import path → initContainer symlink (~50–100ms/pod boot) |
| Failure mode | file missing = no persona | `MODULE_NOT_FOUND` → plugin never registers, fails silently |
| ConfigMap | small | compiled SDK ~957 KiB, near the ~1 MiB etcd limit |
| Templating / tools / slash | no | yes |

### Takeaway

Arquivo estático basta? Não use SDK. SDK só quando precisa de lógica, tools,
ou comando first-class. See `docs/improvements.md` #5 and
`docs/openclaw-skill-slash-research.md` (Option A–D = the *import-resolution*
axis; orthogonal to this static-vs-dynamic axis).

**Housekeeping:** stale `OPENCLAW_SOUL_FILE` + legacy `/home/agent/.openclaw/soul.md`
path linger in `internal/sandbox/manager_config.go`. Runtime uses the
`workspace/SOUL.md` mount instead — align or remove if nothing in the image
reads the legacy path (`docs/improvements.md` #5 follow-up).

---

## Sprint 4 — Playground, marketplace, workspace auth, OpenClaw Tier 4

Sprint theme: tenant-scoped catalog, playground/marketplace waves, subdomain workspace auth (B01/B07), cobrança wedge docs, deploy hygiene.

| Area | What we tried | What broke | What worked | PR / ref |
|---|---|---|---|---|
| Tenant-scoped catalog | Filter souls/skills by workspace membership in API + UI | N/A (greenfield) | `ListSouls` / catalog handlers respect membership; sandbox tokens aligned with catalog scope | [#17](https://github.com/CarlosSalvador-vtex/agentserver/pull/17) |
| OpenClaw plugin-sdk (Tier 4 #16) | Root `agent.systemPrompt` in openclaw.json | Loader rejected unknown root key | initContainer symlink to bundled `plugin-sdk` + native imports in cobrança skill; verify mount in pod | [#47](https://github.com/CarlosSalvador-vtex/agentserver/pull/47), [#33](https://github.com/CarlosSalvador-vtex/agentserver/pull/33) |
| Playground + marketplace | Large UI surface (metrics, diff, promote polling, dry-run) | Stale image tag after merge → “deployed ≠ merged” | Ship in waves [#6](https://github.com/CarlosSalvador-vtex/agentserver/pull/6)–[#9](https://github.com/CarlosSalvador-vtex/agentserver/pull/9); epic [#64](https://github.com/CarlosSalvador-vtex/agentserver/pull/64)–[#70](https://github.com/CarlosSalvador-vtex/agentserver/pull/70) |
| Workspace auth B01/B07 | Invite links on wrong host; weak slug rules | Users landed on apex without tenant context | Subdomain login + reserved slugs + session audit on login | [#57](https://github.com/CarlosSalvador-vtex/agentserver/pull/57), [#71](https://github.com/CarlosSalvador-vtex/agentserver/pull/71) |
| Cobrança wedge | Skill lived only in chart; no runbook for admins | Operators couldn’t reproduce setup | `docs/ops/cobranca-admin-setup.md` + chart skill discoverability | [#81](https://github.com/CarlosSalvador-vtex/agentserver/pull/81), [#82](https://github.com/CarlosSalvador-vtex/agentserver/pull/82) |
| API token scope | Broad GET on workspace tokens | Over-exposed list endpoint | Narrow handler scope + tests | [#100](https://github.com/CarlosSalvador-vtex/agentserver/pull/100) |
| CI GHCR | Push failed on release workflow | Missing/registry token wiring | Documented token + workflow fix | [#101](https://github.com/CarlosSalvador-vtex/agentserver/pull/101) |

**Sprint 4 takeaway:** Multi-tenant UX and OpenClaw need **image + tag + fresh sandbox** verification every time. Catalog and auth PRs are useless on cluster until values tag bumps and sandboxes are recreated.

---

## Sprint 5 — Docs organization backlog (A0–C5)

Sprint theme: canonical docs, archive hygiene, decisions visibility, lessons capture (this file).

| Activity | What we did | Outcome | PR |
|---|---|---|---|
| **A0** | OpenAPI regen + CI drift check | `make openapi-check` gate documented and passing on main | [#89](https://github.com/CarlosSalvador-vtex/agentserver/pull/89) |
| **A1** | cursor-handoffs B01+B07 shipped | Handoff table matches merged reality | [#90](https://github.com/CarlosSalvador-vtex/agentserver/pull/90) |
| **A2** | playground-marketplace-v2-backlog baseline | Tier A items marked shipped with PR refs | [#91](https://github.com/CarlosSalvador-vtex/agentserver/pull/91) |
| **A3** | workspace-auth-pendencies archive | F1–F3 marked resolved | [#92](https://github.com/CarlosSalvador-vtex/agentserver/pull/92) |
| **A4** | superpowers README | Upstream provenance documented; no rewrite of vendored content | [#93](https://github.com/CarlosSalvador-vtex/agentserver/pull/93) |
| **A5** | improvements.md index | Status column on index table | [#94](https://github.com/CarlosSalvador-vtex/agentserver/pull/94) |
| **B2** | ops runbook | Deploy + seed steps for dev EKS | [#95](https://github.com/CarlosSalvador-vtex/agentserver/pull/95) |
| **B4** | cobrança admin setup | Single wedge setup guide under `docs/ops/` | [#96](https://github.com/CarlosSalvador-vtex/agentserver/pull/96) |
| **B3** | workspace-auth collapse | Six overlapping docs → one `workspace-auth.md` | [#97](https://github.com/CarlosSalvador-vtex/agentserver/pull/97) |
| **B1** | playground API reference | `docs/api/reference/playground.md` from OpenAPI | [#98](https://github.com/CarlosSalvador-vtex/agentserver/pull/98) |
| **B5** | cursor-handoffs B02–B10 | Status refresh for backlog honesty | [#99](https://github.com/CarlosSalvador-vtex/agentserver/pull/99) |
| **B6** | saas-multitenancy roadmap | `## Shipped / Closed Gaps` with PR table | [#102](https://github.com/CarlosSalvador-vtex/agentserver/pull/102) |
| **C3** | Decisions Locked sections | D1–D7 appended to canonical feature docs (DD3-B) | [#103](https://github.com/CarlosSalvador-vtex/agentserver/pull/103) |
| **C4** | Archive plan docs | Eight files → `docs/archive/plans/` | [#104](https://github.com/CarlosSalvador-vtex/agentserver/pull/104) |
| **C5** | lessons-learned Sprint 4/5 | This section + Sprint 5 table (docs-only sprint) | *(this PR)* |

**Sprint 5 takeaway:** One activity = one branch + PR. After each merge, pull `origin/main` before the next branch. Keep decisions in **canonical feature docs**, not a third ADR directory.

---

## Cross-cutting patterns

### Pattern: "deployed image doesn't match merged code"

Hit twice in this session (the cobranca skill discoverability sprint + the playground sprint). Symptom: PR merged + helm upgrade ran, but cluster still showed old behavior.

**Cause**: forgot to bump `values-dev-eks.yaml` image tag before `helm upgrade`. Helm doesn't re-pull on `image.pullPolicy: Always` if the tag is unchanged — it sees the same Deployment spec, no rollout.

**Fix workflow**:
1. PR merge
2. `docker build -t ...:<new-tag>`
3. `docker push ...:<new-tag>`
4. Edit `values-dev-eks.yaml` → bump tag
5. `helm upgrade -f values-dev-eks.yaml`
6. `kubectl rollout status deploy/agentserver -n agentserver`

Skip step 4 → silent no-op. Always bump.

### Pattern: "sandbox CRD spec snapshot stale"

When changing pod spec logic (mount paths, env vars, etc.), **existing sandboxes don't auto-update**. The agent-sandbox CRD spec was generated at create time and persisted. `kubectl delete pod` recreates the pod from the same CRD spec — same broken behavior.

**Fix**: delete the sandbox entirely via `DELETE /api/sandboxes/{id}`, then recreate. Or: write a separate migration that re-emits CRD specs for existing sandboxes (we didn't need this yet — test sandboxes are disposable).

### Pattern: "config show says X but LLM behaves like Y"

Hit on hermes persona iterations (PR #27 → #28). `hermes config show` printed the right values, but the LLM ignored them.

**Lesson**: config display ≠ config effect. Verify the actual code path. Add a `grep -rn "<config field>" /opt/<service>/...` step before declaring victory.

### Pattern: "image source dive beats schema guessing"

We saved hours on PR #29 by SSH'ing into a running hermes pod and grepping the source for `SOUL.md`. Sources of truth, in order:

1. Image source files (grep `/opt/<service>/`)
2. Bundled reference plugins/manifests (`/app/extensions/<known-plugin>/`)
3. Schema definitions (if they exist as standalone files: `/app/dist/config-schema-*.js`)
4. Upstream docs (last resort — frequently stale vs the image you have)

---

## Future-agent checklist

When opening a new PR that touches:

- **OpenClaw plugin manifest** — verify `openclaw.plugin.json` shape against `/app/extensions/openshell/openclaw.plugin.json` in the image.
- **OpenClaw persona** — static persona: mount at `~/.openclaw/workspace/SOUL.md` (OpenClaw auto-loads it on boot); do not add root `agent` key to openclaw.json. Reach for the plugin-sdk hook only for dynamic/templated persona or tools (see "Soul/persona injection" rubric above). The legacy `/home/agent/.openclaw/soul.md` + `OPENCLAW_SOUL_FILE` are dead — don't rely on them.
- **Hermes persona** — go straight to `/opt/data/SOUL.md`. Don't touch config.yaml.
- **Sandbox pod spec** — remember existing sandboxes won't update. Spawn a fresh one to test.
- **DB writes before goroutines** — invariants must hold before `go func() { ... }()`. Don't write after.
- **LLM proxy bearer** — mint via `DB.GetOrCreateWorkspaceToken`. Internal secret doesn't work.
- **Deploy** — bump tag in `values-dev-eks.yaml` first, then `helm upgrade`.
- **Schema unknowns** — probe with `grep` in the image source before adding fields. The cost of a single grep beats 3 rebuild/deploy cycles.

---

## References

- `docs/playground-design.md` — design + Appendix A (alternatives considered)
- `docs/openclaw-skill-slash-research.md` — slash command native path (Option A-D analysis)
- `docs/multi-channel-routing.md` — N:M schema + auto-bind
- `docs/whatsapp-cloud-integration.md` — Meta Cloud webhook + HMAC
- `skills/agentserver-helper/SKILL.md` — repo guide for future agents
- PRs #13, #23, #24, #25, #26, #27, #28, #29 — the iterations covered here
