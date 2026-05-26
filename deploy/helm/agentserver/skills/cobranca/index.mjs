// OpenClaw plugin entry point — cobrança pt-BR (texto-only, mock).
//
// Shape matches OpenClaw v2026.3+ plugin contract:
//   default export must be an object literal with { id, register(api) }
//   where register is SYNCHRONOUS. Async register is silently ignored by
//   the loader. Real-world API surface (api.* methods) lives in the
//   openclaw/plugin-sdk modules; rich slash command + system-prompt
//   injection would require importing those at build time. For now this
//   register only validates module load — the actual prompt + tools are
//   exercised via filesystem reads using paths under /home/agent/.openclaw/
//   extensions/cobranca/ that the agent (LLM) is told about explicitly.
//
// Mock-only — fixtures in ./references/leads.json are LGPD-safe synthetic
// records. NEVER swap in real PII without rewriting the read paths.

import fs from "node:fs";
import path from "node:path";

const HERE = path.dirname(new URL(import.meta.url).pathname);

function loadSoulBody() {
  const soulPath =
    process.env.OPENCLAW_SOUL_FILE || "/home/agent/.openclaw/soul.md";
  try {
    if (fs.existsSync(soulPath)) {
      return fs.readFileSync(soulPath, "utf8").trim();
    }
  } catch {
    /* mount optional */
  }
  return (process.env.AGENTSERVER_SOUL_BODY || "").trim();
}

// Pre-load at module init so register() stays purely synchronous and the
// loader sees no promise on the return path.
let PROMPT_BODY = fs.readFileSync(path.join(HERE, "prompt.md"), "utf8");
const SOUL_BODY = loadSoulBody();
if (SOUL_BODY) {
  PROMPT_BODY = `## Persona (soul.md)\n${SOUL_BODY}\n\n${PROMPT_BODY}`;
}
const LEADS = JSON.parse(fs.readFileSync(path.join(HERE, "references", "leads.json"), "utf8"));

const plugin = {
  id: "cobranca",
  name: "Cobrança pt-BR (mock)",
  description:
    "Agente de cobrança LGPD-safe pt-BR para WhatsApp/Telegram/web chat. " +
    "Usa fixtures sintéticos — NÃO usar com dados reais.",
  configSchema: {
    type: "object",
    additionalProperties: false,
    properties: {},
  },
  register(api) {
    // Cheap visibility: log that the plugin loaded with the data preloaded.
    // The full slash-command + tool surface would require the plugin-sdk
    // typed APIs; this stub avoids loader errors and keeps the on-disk
    // skill bundle discoverable by name-based agent prompts.
    if (api?.logger?.info) {
      api.logger.info(
        `[cobranca] loaded — ${LEADS.length} leads, prompt ${PROMPT_BODY.length} chars` +
          (SOUL_BODY ? `, soul ${SOUL_BODY.length} chars` : ""),
      );
    }
    return { id: "cobranca", version: "0.1.0" };
  },
};

export default plugin;
