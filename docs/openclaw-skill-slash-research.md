# OpenClaw skill slash commands — research + trade-offs

> **Status (2026-05-27):** cobranca now uses `definePluginEntry` from `openclaw/plugin-sdk/core`
> via the initContainer symlink from #16. Soul persona works natively — OpenClaw reads
> `SOUL.md` from workspace bootstrap without any plugin-side injection. Slash commands
> remain out of scope (registerCommand is a noop in this image version).
>
> Original research notes below (kept for context on why slash commands aren't native yet).

## TL;DR

| Today | Native slash (future PR) |
|---|---|
| `index.mjs` is plain Node, no SDK imports | `import { definePluginEntry } from "openclaw/plugin-sdk/core"` |
| `register(api)` only logs — no commands / tools wired | `register` uses SDK helpers to register `/cobranca`, tools, channel hooks |
| Persona kicks in because the prompt **tells the LLM** to read `prompt.md` | LLM sees `/cobranca` as a first-class command + invokes tools via function-calling |
| Skill is a ConfigMap-mounted directory, YAML drop-in, no node_modules | Plugin needs the `openclaw/plugin-sdk/*` modules resolvable from skill dir |

The blocker is not syntax. It's that the `openclaw/plugin-sdk/*`
modules live **inside** the `openclaw-agent` Docker image at
`/app/node_modules/openclaw/`, and our skill ConfigMap mount lands at
`/home/agent/.openclaw/extensions/cobranca/` with **no node_modules**.
Without a runtime resolver hook, every `import { ... } from
"openclaw/plugin-sdk/core"` would 404 at module load.

---

## What we shipped (PR #13)

```js
// deploy/helm/agentserver/skills/cobranca/index.mjs
import fs from "node:fs";
import path from "node:path";

const HERE = path.dirname(new URL(import.meta.url).pathname);
const PROMPT_BODY = fs.readFileSync(path.join(HERE, "prompt.md"), "utf8");
const LEADS = JSON.parse(fs.readFileSync(path.join(HERE, "references", "leads.json"), "utf8"));

const plugin = {
  id: "cobranca",
  name: "Cobrança pt-BR (mock)",
  description: "...",
  configSchema: { type: "object", additionalProperties: false, properties: {} },
  register(api) {
    if (api?.logger?.info) {
      api.logger.info(`[cobranca] loaded — ${LEADS.length} leads, prompt ${PROMPT_BODY.length} chars`);
    }
    return { id: "cobranca", version: "0.1.0" };
  },
};

export default plugin;
```

OpenClaw's plugin loader picks it up via filesystem auto-discovery
(see `manager.go::skillVolumesAndMounts` mounting at
`/home/agent/.openclaw/extensions/cobranca/`). At boot the loader logs:

```
[plugins] plugins.allow is empty; discovered non-bundled plugins may
  auto-load: cobranca (/home/agent/.openclaw/extensions/cobranca/index.mjs)
[cobranca] loaded — 3 leads, prompt 2523 chars
```

But the `api` object the loader passes to our `register` does **not**
expose `commands.register('/cobranca', handler)` or
`tools.register({...})`. Those surfaces live in the typed SDK that we
don't import. The only thing register can do here is log + cache state.

How `/cobranca` "works" today: the user (or agent prompt) explicitly
points the LLM at the skill files:

```bash
hermes -z "Leia /opt/data/skills/personal/cobranca/prompt.md ... atue como Julia ..." --yolo
```

This is **path-based prompting**, not slash command routing. Works
end-to-end (Júlia persona, LGPD flow, lead lookup) but the LLM is doing
the work; the plugin host isn't.

---

## What native slash would need

### SDK surface (peeked from the openclaw image)

```bash
$ docker run --rm --entrypoint sh ghcr.io/agentserver/openclaw-agent@sha256:1c037... \
    -c "ls /app/dist/plugin-sdk/ | wc -l"
100

$ grep -E "^export " /app/dist/plugin-sdk/core.js | head
export { definePluginEntry, defineChannelPluginEntry,
         defineSetupPluginEntry, createChatChannelPlugin,
         createChannelPluginBase, buildPluginConfigSchema,
         buildChannelConfigSchema, KeyedAsyncQueue,
         buildAgentSessionKey, ... };
```

### Real plugin shape

`/app/extensions/openshell/index.ts` (TypeScript source committed to
the upstream openclaw-agent repo, compiled into the image):

```ts
import type { OpenClawPluginApi } from "openclaw/plugin-sdk/core";
import { registerSandboxBackend } from "openclaw/plugin-sdk/sandbox";
import {
  createOpenShellSandboxBackendFactory,
  createOpenShellSandboxBackendManager,
} from "./src/backend.js";

const plugin = {
  id: "openshell",
  name: "OpenShell Sandbox",
  description: "...",
  configSchema: createOpenShellPluginConfigSchema(),
  register(api: OpenClawPluginApi) {
    if (api.registrationMode !== "full") return;
    const cfg = resolveOpenShellPluginConfig(api.pluginConfig);
    registerSandboxBackend("openshell", {
      factory: createOpenShellSandboxBackendFactory({ pluginConfig: cfg }),
      manager: createOpenShellSandboxBackendManager({ pluginConfig: cfg }),
    });
  },
};

export default plugin;
```

Slash commands specifically come from one of:

| Plugin kind | Where slash routing lives |
|---|---|
| **Channel plugin** (`createChatChannelPlugin`) | Channel's inbound pipeline parses slash before forwarding to LLM. See `/app/extensions/mattermost/src/mattermost/slash-commands.ts`. |
| **Agent turn hook** | `api.agent.onTurnStart(ctx => ...)` style — intercept user message, dispatch to handler. (Speculative shape; confirm against `OpenClawPluginApi` types.) |
| **Tools (function-calling)** | `api.tools.register({ name, input_schema, handler })` exposes a tool the LLM picks autonomously. Doesn't fire on literal slash but is the most idiomatic path for LLM workflows. |

All three require `import` from `openclaw/plugin-sdk/*`. That's the
crux: not the API surface, the **import resolution**.

---

## Why import resolution is the build pipeline gap

### Today's runtime layout in the openclaw sandbox pod

```
/home/agent/.openclaw/extensions/cobranca/   ← ConfigMap mount (RO)
├── index.mjs
├── openclaw.plugin.json
├── prompt.md
├── package.json                              ← marker, no real deps
└── references/

/app/                                         ← Image baked-in
├── extensions/
│   ├── anthropic/
│   ├── openshell/
│   └── ... (100+ built-in plugins, all compiled .js)
├── dist/plugin-sdk/                          ← 100 SDK modules
└── node_modules/
    └── openclaw/                             ← TypeScript types + runtime
        └── plugin-sdk/                       ← what `import` resolves
```

Node's import resolution algorithm (when evaluating `import x from "openclaw/plugin-sdk/core"`):

1. Look for `node_modules/openclaw/` next to the importing file
   → `/home/agent/.openclaw/extensions/cobranca/node_modules/openclaw/`
   **Not there** (ConfigMap doesn't ship node_modules).
2. Walk up parent directories
   → `/home/agent/.openclaw/extensions/node_modules/openclaw/`,
   `/home/agent/.openclaw/node_modules/openclaw/`,
   `/home/agent/node_modules/openclaw/`,
   `/home/node_modules/openclaw/`,
   `/node_modules/openclaw/`
   **None exist**.
3. **MODULE_NOT_FOUND** at load. Plugin fails to register. OpenClaw logs the error and continues without it.

So even though the SDK files **are** present in the image, Node can't
find them from our skill directory.

### ConfigMap size constraint

Kubernetes ConfigMaps cap at **1 MiB** per object (effective limit
~957 KiB after etcd overhead). The compiled SDK (`/app/dist/plugin-sdk/`)
is **~5-10 MiB** of `.js` plus minified helpers. Cannot inline.

Even if you bundle aggressively with esbuild + tree-shaking, a typical
slash-command plugin pulls in:
- `core.js` (~50 KiB minified)
- `channel-runtime.js` + dependencies (~100 KiB)
- `agent-runtime.js` + dependencies (~200 KiB)
- `KeyedAsyncQueue`, session helpers, etc. (~50 KiB)

Realistic bundle: 300-500 KiB. **Possible** to fit in a ConfigMap, but
that's a per-skill bundle (every skill ships the SDK redundantly) and
the bundle has to be rebuilt every time the upstream SDK ships a
breaking change.

---

## The 4 paths forward

### Option A — TypeScript + bundler, inline SDK

```
skills/cobranca/
├── src/
│   ├── index.ts
│   └── persona.ts
├── tsconfig.json
├── package.json (deps: "openclaw": "^2026.x")
└── (build script) → dist/index.mjs    ← SDK inlined ~400 KiB
```

| ✅ | ❌ |
|---|---|
| Skill stays YAML-droppable (build artifact mounts in ConfigMap) | Adds TS + bundler to skill repo |
| Native slash commands work | ConfigMap bloat (~400 KiB per skill) |
| Versioned via lockfile | Per-skill SDK rebuild on upstream bumps |
| | Risk of duplicate SDK runtime instances across skills |

**Verdict:** viable for 1-2 critical skills, doesn't scale to N tenants × M skills.

### Option B — Runtime resolver hook (initContainer symlink)

```yaml
# deploy/helm/agentserver/templates/_sandbox_skill_init.tpl
initContainers:
- name: link-sdk
  image: busybox
  command: [sh, -c]
  args:
  - |
    for skill in /home/agent/.openclaw/extensions/*/; do
      mkdir -p "$skill/node_modules"
      ln -s /app/node_modules/openclaw "$skill/node_modules/openclaw"
    done
```

Plus per-pod EmptyDir for the symlink target (ConfigMap mount is RO).

| ✅ | ❌ |
|---|---|
| Skill `index.mjs` imports from `openclaw/plugin-sdk/*` like first-party plugins | initContainer per pod (50-100ms boot overhead) |
| No bundle / no TS in skill repo | Tightly coupled to image layout — upstream relocates `node_modules` and we break |
| SDK version pinned to image, no duplication | RWO PVC + symlink semantics need careful testing |
| Single Helm-side change, no per-tenant config | Symlink across RO/RW filesystems sometimes touchy on K8s nodes |

**Verdict:** lowest-friction technical path. ~30-50 LOC in `manager.go`
to wire the initContainer + EmptyDir. Best candidate for the next PR.

### Option C — Publish skills as npm packages

```bash
# Skill author
cd skills/cobranca && npm publish --registry=https://npm.internal.vtex.com

# Sandbox pod boots, runs:
openclaw plugins install @vtex/skill-cobranca@1.2.0
```

| ✅ | ❌ |
|---|---|
| Canonical OpenClaw plugin distribution path | Requires private npm registry |
| Full SDK access, no resolver tricks | npm publish gate per release |
| Per-tenant version pinning trivial | Loses "edit + helm upgrade = changed in pod" loop |
| | Per-pod npm install at boot (network + cache concerns) |

**Verdict:** right path for production multi-tenant. Heavy operational
lift for a vault-side skill experiment.

### Option D — Bake skills into a custom OpenClaw image

```dockerfile
FROM ghcr.io/agentserver/openclaw-agent:latest
COPY ./skills/cobranca /app/extensions/cobranca
```

| ✅ | ❌ |
|---|---|
| SDK resolves natively (skill lives next to built-in plugins) | Image rebuild per skill change |
| Zero runtime overhead | Per-tenant skills = per-tenant image (operational nightmare) |
| | Can't ship skill independently of agentserver release cycle |

**Verdict:** only viable if a skill becomes a **platform-wide built-in**
(e.g. every tenant gets the cobranca skill by default).

---

## Recommendation: Option B for the next PR

**Why:**
1. **Lowest LOC** — fits in a single chart template + ~30 LOC of Go to
   wire the EmptyDir + initContainer.
2. **No new build pipeline** — skill repo stays YAML-only. Authors edit
   `.mjs` + `helm upgrade`, done.
3. **Skill imports become legitimate** — `definePluginEntry`,
   `createChatChannelPlugin`, `api.tools.register` all available.
4. **Decoupled from npm** — no registry, no publish gate, no per-tenant
   distribution problem.

**Risks to plan for:**
- Symlink in a RO ConfigMap mount won't work. Need an
  EmptyDir volume that the initContainer populates AND that the agent
  container mounts at the same skill paths. Per-skill EmptyDir
  + symlink dance.
- Upstream `openclaw-agent` could relocate `node_modules` between
  releases. Pin image by digest (we already do this in
  `values-dev-eks.yaml`) and write a smoke check.
- `api.registrationMode` per plugin probably distinguishes "schema-only"
  load vs "full" load. Our `register` needs to honour both.

### Concrete plan for the future PR

```
1. internal/sandbox/manager.go::buildOpenclawPodSpec
   - Add EmptyDir volume `openclaw-sdk-links`.
   - Add initContainer `link-sdk` (busybox) that, for each entry in
     /home/agent/.openclaw/extensions/, creates a node_modules subdir
     and symlinks /app/node_modules/openclaw into it.
   - Mount the EmptyDir into the agent container at
     /home/agent/.openclaw/extensions/<skill>/node_modules/.

2. Test with cobranca first:
   - Convert index.mjs → index.mjs (still ESM) importing
     definePluginEntry from "openclaw/plugin-sdk/core".
   - Replace passive `register(api)` with proper api.commands.register
     or api.tools.register calls (TBD against actual SDK types).
   - Smoke: `openclaw agent --to ... -m "/cobranca"` triggers the
     command without needing path-based prompt hack.

3. Doc: extend docs/skills-system.md with the symlink trick + how
   skill authors should `import` from the SDK.

4. Rollback: if symlink approach breaks on certain K8s versions, the
   skill `register()` is still safe to call without SDK access (it's
   resilient to a missing api surface).
```

---

## Why we didn't ship Option B in PR #13

1. Task scope: "make cobranca work" — done via path-based, both
   runtimes (OpenClaw + Hermes) respond with the Júlia persona + LGPD
   flow + lead lookup. Tests pass end-to-end.
2. initContainer + EmptyDir + symlink across mounts is a real
   architectural decision. It crosses 3 layers (chart, container,
   plugin-sdk versioning) and deserves its own review.
3. Production multi-tenant likely wants Option C (npm publish) anyway,
   so Option B might end up being a transitional bridge — not the
   permanent answer. Worth getting clarity on the production path
   before locking in the symlink design.

---

## References

- `internal/sandbox/manager.go::skillVolumesAndMounts` — ConfigMap → pod mount.
- `internal/sandbox/config.go::BuildOpenclawConfig` — `__OPENCLAW_INJECT_CFG` env emission.
- `deploy/helm/agentserver/skills/cobranca/` — current skill bundle.
- `docs/skills-system.md` — distribution + lifecycle of skills via ConfigMap.
- Upstream image: `ghcr.io/agentserver/openclaw-agent@sha256:1c03752715d7739093a764e5d4fea097f970ad315201ce6c9b4d7e903ada6a5d`
- PR #13: `fix(skill): cobranca skill auto-discovers in OpenClaw + Hermes` — the 4 fixes that got us this far (path, installs, manifest, sync register).
