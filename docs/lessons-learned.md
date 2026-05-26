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
