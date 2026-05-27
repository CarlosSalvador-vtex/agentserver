// Cobrança pt-BR — OpenClaw plugin with native plugin-sdk imports.
//
// Requires the openclaw/plugin-sdk symlink provided by the link-sdk
// initContainer (improvements.md #16). If the symlink is absent the
// import will throw MODULE_NOT_FOUND and OpenClaw will log the error
// and skip this plugin.
//
// Soul persona is injected by OpenClaw's workspace bootstrap loader
// from /home/agent/.openclaw/workspace/SOUL.md — no in-plugin system
// prompt injection needed. See auth-profiles source in the image:
//   if (hasSoulFile) lines.push("If SOUL.md is present, embody its
//   persona and tone.");
//
// Mock-only — fixtures in ./references/leads.json are LGPD-safe
// synthetic records. NEVER swap in real PII.

import fs from "node:fs";
import path from "node:path";
import { definePluginEntry } from "openclaw/plugin-sdk/core";

const HERE = path.dirname(new URL(import.meta.url).pathname);

const PROMPT_BODY = fs.readFileSync(path.join(HERE, "prompt.md"), "utf8");
const LEADS = JSON.parse(
  fs.readFileSync(path.join(HERE, "references", "leads.json"), "utf8"),
);

export default definePluginEntry({
  id: "cobranca",
  name: "Cobrança pt-BR (mock)",
  description:
    "Agente de cobrança LGPD-safe pt-BR para WhatsApp/Telegram/web chat. " +
    "Usa fixtures sintéticos — NÃO usar com dados reais.",
  register(api) {
    if (api?.logger?.info) {
      api.logger.info(
        `[cobranca] loaded via plugin-sdk — ${LEADS.length} leads, prompt ${PROMPT_BODY.length} chars`,
      );
    }
  },
});
