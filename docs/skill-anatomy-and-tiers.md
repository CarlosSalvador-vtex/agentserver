# Skill anatomy & capability tiers

> What a "skill" actually is in agentserver, what each file does, and the two
> capability tiers — instruction (no-code, layperson) vs plugin (code, agentic).
> Companion to `docs/skills-system.md` (which covers the ConfigMap mount plumbing)
> and `docs/playground-design.md` (the editor/publish flow).

## A skill is an OpenClaw plugin, not just a prompt

A skill is **not** a single markdown file. It is a small package (npm-style) that
OpenClaw loads as a plugin and mounts at `/home/agent/.openclaw/extensions/<name>/`
(Hermes: `/opt/data/skills/personal/<name>/`). Markdown is only the persona/prompt
part; the rest is the actual program.

Example: the `cobranca` skill (`deploy/helm/agentserver/skills/cobranca/`):

| File | Type | Role |
|------|------|------|
| `prompt.md` | markdown | **Main instructions / persona.** Who the agent is (Júlia), rules (pt-BR, short, LGPD), tone. This is the bulk of the natural-language behavior. |
| `SKILL.md` | markdown | Descriptor/metadata for the skill (catalog-facing). |
| `openclaw.plugin.json` | json | Plugin **manifest** — id + entry. OpenClaw reads it to register the plugin. |
| `index.mjs` | code (ESM) | **The plugin.** Loads data, wraps + injects the persona, and defines + implements the **tools**. See breakdown below. |
| `package.json` | json | npm metadata (`type: module`, deps). Needed for ESM resolution + the plugin-sdk symlink. |
| `references/leads.json` | json | Data fixture the skill reads at runtime (LGPD-safe synthetic leads). |

### What `index.mjs` actually contains

It is the whole plugin (~200 lines for cobranca), not just tool declarations:

1. **Imports** — `fs`, `path`, `openclaw/plugin-sdk/core`.
2. **Data load** — reads `prompt.md` into `PROMPT_BODY`, `references/leads.json` into `LEADS`.
3. **State** — e.g. `const agreements = new Map()` (in-memory store).
4. **Persona envelope** — `PERSONA_SYSTEM = "<cobranca-persona> … ${PROMPT_BODY} …"` (a thin wrapper around `prompt.md`).
5. **Business logic / helpers** — `normalizeCpfDigits`, `findLeadByCpfLast3`, `findLeadById`.
6. **Bootstrap** — `definePluginEntry({ ... })`, on-load log.
7. **Lifecycle hook** — `api.on("before_prompt_build", () => ({ prependSystemContext: PERSONA_SYSTEM }))` — this is what actually **injects** the instructions into the system prompt at runtime.
8. **Tools (definition + implementation)** — `api.registerTool(...)` for `lookup_debt`, `generate_boleto`, `mark_agreement`. Each carries its schema **and** the code that executes the action.

So a tool is not merely "declared" — `index.mjs` carries the real execution (validate CPF → filter `LEADS`, generate a boleto link, write to the `agreements` map).

## Instructions vs capability — where each lives

- **Instructions / persona / rules** → `prompt.md` (and the `soul` entity). Editing
  `prompt.md` changes *what* the agent says and how it behaves. No code.
- **Capability / tools** → `index.mjs`. Adding or changing a tool changes *what the
  agent can do*. This is code.

`prompt.md` says **what** to do; `index.mjs` is **how** it does it (code + data + tools).

## The two capability tiers

| Tier | What it is | Power | Agentic? | Who edits |
|------|-----------|-------|----------|-----------|
| **Instruction** (`prompt.md`, `soul`, `SKILL.md`) | Persona, rules, tone — text the model reads and follows. Uses only tools the host already exposes. | Low — declarative, no new capability | ❌ text only | layperson (no code) |
| **Plugin** (`index.mjs`: tools + hooks) | Code that registers new tools, hooks the prompt lifecycle, holds state, loads data, runs JS in the sandbox runtime. | High — imperative, adds capability + intercepts the agent | ✅ agentic | developer |

### Why the plugin tier is "more agentic"

Agency = the ability to **act and react to results**, not just produce text. That
capability comes specifically from the **tools** — they are the agent's hands:

```
lookup_debt(cpf_last_3)   → perceive   (query the lead)
        ↓ decide (based on the result)
generate_boleto / mark_agreement → act (create / mutate)
        ↓ observe the return → continue the loop
```

- **Markdown-only skill** → the model only emits text following the prompt. A chatbot
  with a persona. No actions, no tool-call loop.
- **Plugin skill** → the model calls tools, gets results, decides the next call. This
  perceive → decide → act → observe loop is the core of agentic behavior. The
  `before_prompt_build` hook adds further runtime control over behavior.

Without tools the agent only talks; with tools it does things. The agency lives in
`index.mjs`.

## Layperson vs developer (UX implication)

The model already separates the no-code path from the code path:

- **No-code (layperson):** edit the **soul** (persona via a form: voice, tone,
  constraints) and the skill's `prompt.md` (instructions). This covers *who the agent
  is* and *how it talks* — no JavaScript.
- **Code (developer):** edit `index.mjs` to add or change **tools** — *what the agent
  can do*. Custom tools genuinely require code today.

The Playground reflects this: non-admin sees a simplified view; `isDevMode` (admin)
exposes the full file tree including the code files.

### Future direction (reduce the code barrier for capability)

Custom tools require `.mjs` today. To let non-developers extend *capability* without
writing JS:

- **Tool catalog / library** — pre-built, reusable tools a layperson can **enable and
  configure** via a form (per-skill `configSchema`, already noted as a Playground
  picker gap). A tool becomes a configurable building block instead of code.
- This would split "configure an existing tool" (form, layperson) from "author a new
  tool" (code, developer).

## Summary

- A skill = an OpenClaw plugin package (manifest + code + prompt + data), not a lone markdown.
- `prompt.md` (+ soul) = the main instructions/persona — no-code, layperson tier.
- `index.mjs` = data + logic + hooks + **tools** — the code/agentic tier.
- Agentic capability (act → observe loop) comes from the tools in `index.mjs`.
- Layperson path = persona/instructions; new capability (tools) = developer (code) today.
