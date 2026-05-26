# Skills System — How It Works & When You Need to Touch Code

How custom skills (Hermes `SKILL.md` + OpenClaw plugins) are delivered into
sandbox pods in the agentserver dev EKS deployment.

**TL;DR**
- The **first** skill (`cobranca`) required Go + Helm changes to *build* the
  generic plumbing.
- **Every subsequent** skill is **YAML-only** — drop files in
  `deploy/helm/agentserver/skills/<name>/`, add a one-line values entry,
  `helm upgrade`. No code, no rebuild, no image push.

---

## Why backend code was edited for the first skill

A skill ConfigMap has three concerns the chart alone can't handle:

| Concern | Why YAML can't do it |
|---|---|
| **Cross-namespace replication** | ConfigMaps are namespace-scoped. The chart renders them in the release namespace (`agentserver`). Sandboxes run in per-workspace namespaces (`agent-ws-<id>`). Copying a ConfigMap between namespaces is a runtime K8s API call (`Get` in `agentserver` ns, `Create`/`Update` in workspace ns) — only the Go controller can do that. |
| **Per-platform mount paths** | Hermes expects skills under `/opt/data/skills/personal/<name>/`. OpenClaw expects them under `/home/node/.openclaw/plugins/<name>/`. The Go switch on `SandboxType` is what selects the right mount root. |
| **OpenClaw config injection** | OpenClaw activates skills via `plugins.entries.<name>.enabled = true` in `openclaw.json`. That file is deep-merged at container start via `__OPENCLAW_INJECT_CFG` (env var produced by `BuildOpenclawConfig` in Go). To register a new plugin entry we had to extend that emitter. |

These three things are one-time-only — once they exist, they work for every
skill that lands in `deploy/helm/agentserver/skills/`.

### One-time additions (this PR)

- `internal/sandbox/config.go`
  - `Config.SkillConfigMaps map[string]string` — parsed from
    `SANDBOX_SKILL_CONFIGMAPS=cobranca=agentserver-skill-cobranca,...`
  - `OpenclawConfigOptions` (plugins + WhatsApp allowlist arguments to
    `BuildOpenclawConfig`).
- `internal/sandbox/manager.go`
  - `replicateConfigMap(name, targetNS)` — generic CM copy across namespaces.
  - `replicateSkillConfigMaps(ctx, ns)` — looper over `SkillConfigMaps`.
  - `skillVolumesAndMounts(ctx, platform)` — builds Volume + per-file
    VolumeMount entries; decodes the `__` → `/` slash encoding used in
    ConfigMap data keys; uses `item.Path` (not `item.Key`) as SubPath.
  - Hermes branch: also exports `WHATSAPP_ALLOWED_USERS` env when set.
  - OpenClaw branch: enriches the inject payload with
    `OpenclawConfigOptions{EnabledPlugins, WhatsappAllowed}`.
- `deploy/helm/agentserver/templates/skills-configmap.yaml` — ranges over
  `.Values.sandbox.skills`, emits one ConfigMap per enabled entry, with all
  files in the skill folder inlined via `.Files.Get`.
- `deploy/helm/agentserver/templates/deployment.yaml` — wires
  `SANDBOX_SKILL_CONFIGMAPS`, `HERMES_WHATSAPP_ALLOWED`,
  `OPENCLAW_WHATSAPP_ALLOWED` to the agentserver pod env.

---

## Lifecycle of a skill ConfigMap

```
chart values.yaml                  helm install / upgrade           runtime
sandbox.skills.cobranca   ───►   ConfigMap                  ───►   agentserver pod
{ enabled: true }               agentserver/                       env: SANDBOX_SKILL_CONFIGMAPS=
                                agentserver-skill-cobranca               cobranca=agentserver-skill-cobranca
                                (data: SKILL.md, prompt.md,
                                 plugin.json, index.mjs,
                                 references__leads.json,
                                 references__script_full.md)
                                                                   │
                                                                   ▼ POST /api/workspaces/<wid>/sandboxes
                                                              replicateSkillConfigMaps(workspaceNS)
                                                                   │
                                                                   ▼
                                                              ConfigMap copied into
                                                              agent-ws-<wid>/
                                                              agentserver-skill-cobranca
                                                                   │
                                                                   ▼
                                                              Sandbox pod mounts:
                                                              /opt/data/skills/personal/cobranca/
                                                                ├── SKILL.md
                                                                ├── prompt.md
                                                                ├── plugin.json
                                                                ├── index.mjs
                                                                └── references/
                                                                    ├── leads.json
                                                                    └── script_full.md
```

Key detail: ConfigMap data keys can't contain `/`. Nested paths are encoded
with `__` (`references/leads.json` → `references__leads.json`). The Go mount
builder restores the slash via `items[].path` and uses that same nested path
as `SubPath` so the on-disk layout matches what the agent expects.

---

## Adding skill #2 — pure YAML

For any future skill, you only touch the chart. **No image rebuild, no Go
changes, no PR review on the backend.**

### Step 1 — drop the files

```
deploy/helm/agentserver/skills/<your-skill>/
├── SKILL.md           # Hermes frontmatter + body
├── prompt.md          # shared persona / instructions
├── plugin.json        # OpenClaw manifest (optional, omit if no OpenClaw)
├── index.mjs          # OpenClaw plugin entry (optional)
└── references/
    └── *.json|md      # fixtures, long-form docs
```

The folder name becomes the skill name and the slash command (`/<name>`).

### Step 2 — enable in values

```yaml
# values-dev-eks.yaml or your overrides file
sandbox:
  skills:
    your-skill:
      enabled: true
      platforms: [hermes, openclaw]  # informational
```

### Step 3 — apply

```bash
helm upgrade agentserver deploy/helm/agentserver \
  -n agentserver --kube-context "$CTX" -f values-dev-eks.yaml
```

The chart renders a new `{Release.Name}-skill-<your-skill>` ConfigMap.
On the next sandbox create the agentserver replicates and mounts it.

### Step 4 — restart agentserver if env needs to refresh

```bash
kubectl --context "$CTX" rollout restart deployment/agentserver -n agentserver
```

Only needed because `SANDBOX_SKILL_CONFIGMAPS` is read at startup. Skills
already enabled at chart time don't require this.

---

## What a skill file looks like

### `SKILL.md` (Hermes)

```markdown
---
name: refund
description: Mock customer refund flow — pt-BR.
tags: [refund, finance, mock]
command: /refund
platforms: [whatsapp, dashboard]
---

# Skill: refund

When `/refund` is invoked, follow the persona defined in `prompt.md`.
Look up orders with `lookup_order(order_id)` (reads `references/orders.json`).
…
```

### `plugin.json` + `index.mjs` (OpenClaw, optional)

```json
{
  "name": "refund",
  "version": "0.1.0",
  "type": "skill",
  "entry": "index.mjs"
}
```

```js
export default async function register(ctx) {
  const prompt = await (await import("node:fs/promises")).readFile(
    new URL("./prompt.md", import.meta.url), "utf8");
  ctx.systemPrompt.append(prompt);
  ctx.commands.register("/refund", async ({ reply }) => {
    await reply("Oi! Vou te ajudar com a devolução.");
  });
  ctx.tools.register({ name: "lookup_order", handler: lookup_order, ... });
}
```

---

## Why ConfigMap (and not a baked-in image)

| Option | Trade-off |
|---|---|
| Bake the skill into a custom sandbox image | Fast at runtime, slow to iterate (image build + push + helm rollout per change). Requires a fork of `openclaw-agent` / `hermes-agent`. |
| **ConfigMap mount (chosen)** | Slower at mount time (extra K8s API calls per sandbox create). Iteration loop is 1 file edit + `helm upgrade`. No custom image. Each skill ships as plain Helm chart asset. |
| Sidecar container hosting the skill | Adds a process per sandbox, more memory + IPC complexity. Overkill for text-only mock skills. |

For text-only skills (prompts + JSON fixtures + small JS), ConfigMap is the
sweet spot. If/when a skill needs a binary (Python wheel, native library), we
revisit and either:
- Mount a PVC pre-populated with the binary, or
- Build a per-skill sidecar image and mount the skill into the main container
  via a shared `emptyDir`.

---

## Edge cases / gotchas (already handled)

- **Data keys can't have `/`** → encoded with `__`, decoded in
  `skillVolumesAndMounts` via `items[].path`.
- **SubPath must match `items[].path`, not `items[].key`** → if you use the
  flat encoded key as SubPath, K8s creates an empty *directory* at the mount
  target instead of mounting the file. (See the v1 bug; fixed in
  `manager.go::skillVolumesAndMounts`.)
- **OpenClaw plugin gate** → the chart only delivers the files; the
  `plugins.entries.<name>.enabled=true` flag is what activates the plugin
  inside OpenClaw. The agentserver injects this flag via
  `OpenclawConfigOptions.EnabledPlugins` derived from `Config.SkillConfigMaps`.
- **Hermes auto-discovery** → the upstream `hermes` CLI doesn't yet auto-list
  skills dropped into `/opt/data/skills/personal/`. Today the LLM accesses the
  files via explicit path. A follow-up will wire `hermes skills install --local`
  during pod startup so they appear in `hermes skills list`.

---

## Quick sanity check after deploying a new skill

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# Chart-side: ConfigMap rendered
kubectl --context "$CTX" get configmap -n agentserver | grep skill

# Pod-side: files visible inside a fresh sandbox
kubectl --context "$CTX" exec -n agent-ws-<wid> agent-sandbox-<short> -c agent -- \
  find /opt/data/skills/personal/<name> -maxdepth 3 -type f | sort

# OpenClaw injection
kubectl --context "$CTX" exec -n agent-ws-<wid> agent-sandbox-<short> -c agent -- \
  cat /home/node/.openclaw/openclaw.json | jq '.plugins.entries'

# LLM smoke
kubectl --context "$CTX" exec -n agent-ws-<wid> agent-sandbox-<short> -c agent -- \
  /opt/hermes/.venv/bin/hermes -z "Use a skill <name>; follow the persona em prompt.md" --yolo
```
