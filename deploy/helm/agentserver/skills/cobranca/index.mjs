// OpenClaw plugin entry point — cobrança pt-BR (texto-only, mock).
//
// Plugin host expectations (best-effort against OpenClaw v0.26+):
//   - default export receives a `ctx` with command/tool registries + a system
//     prompt appender + file access scoped to the plugin directory.
//   - This module is loaded via `plugins.entries.cobranca.enabled = true` in
//     ~/.openclaw/openclaw.json (the agentserver fork injects this flag).
//
// Mock-only — fixtures in ./references/leads.json are LGPD-safe synthetic
// records. NEVER swap in real PII without rewriting the read paths.

import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";

const HERE = path.dirname(new URL(import.meta.url).pathname);
const LEADS_PATH = path.join(HERE, "references", "leads.json");
const STATE_DIR = path.join(HERE, "state");
const AGREEMENTS_LOG = path.join(STATE_DIR, "agreements.log");

let leadsCache = null;

async function loadLeads() {
  if (leadsCache) return leadsCache;
  const raw = await fs.readFile(LEADS_PATH, "utf8");
  leadsCache = JSON.parse(raw);
  return leadsCache;
}

async function loadPromptBody() {
  return fs.readFile(path.join(HERE, "prompt.md"), "utf8");
}

// --- tools -----------------------------------------------------------------

async function lookup_debt({ cpf_last_3 }) {
  const leads = await loadLeads();
  const found = leads.find((l) => l.cpf_last_3 === String(cpf_last_3));
  return found || null;
}

async function generate_boleto({ lead_id, amount, due_date }) {
  const url = `https://mock.acme.local/boleto/${lead_id}.pdf`;
  // Fake 47-digit barcode. Pseudo-Febraban shape, not a real spec.
  const barcode = crypto.randomBytes(24).toString("hex").slice(0, 47).replace(/[a-f]/g, "0");
  return { url, barcode, amount, due_date };
}

async function mark_agreement({ lead_id, status }) {
  if (!["agreed", "refused", "callback"].includes(status)) {
    throw new Error(`mark_agreement: invalid status "${status}"`);
  }
  await fs.mkdir(STATE_DIR, { recursive: true });
  const recorded_at = new Date().toISOString();
  const line = JSON.stringify({ lead_id, status, recorded_at }) + "\n";
  await fs.appendFile(AGREEMENTS_LOG, line, "utf8");
  return { ok: true, recorded_at };
}

// --- registration ----------------------------------------------------------

export default async function register(ctx) {
  const promptBody = await loadPromptBody();

  // Append the persona/flow as a system-prompt block so the LLM picks it up
  // on every turn while this plugin is active.
  if (ctx?.systemPrompt?.append) {
    ctx.systemPrompt.append("\n\n### cobranca skill (active)\n\n" + promptBody);
  }

  // Slash command /cobranca — opens the conversation with the greeting and
  // pre-loads the persona for downstream turns.
  if (ctx?.commands?.register) {
    ctx.commands.register("/cobranca", async ({ args, reply }) => {
      const opener =
        "Oi! Aqui é a Júlia, da Acme Cobranças. " +
        "Estou ligando sobre um débito em aberto. " +
        "Estou falando com a pessoa do cadastro?";
      await reply(opener);
      // Hint for the LLM: the next user turn should provide identity confirmation.
      ctx.session?.set?.("cobranca.stage", "await_identity");
    });
  }

  // Tools — registered with JSON-Schema-style signatures.
  if (ctx?.tools?.register) {
    ctx.tools.register({
      name: "lookup_debt",
      description: "Look up a debtor by the last 3 digits of their CPF. Returns the lead record or null.",
      input_schema: {
        type: "object",
        properties: { cpf_last_3: { type: "string", pattern: "^[0-9]{3}$" } },
        required: ["cpf_last_3"],
      },
      handler: lookup_debt,
    });

    ctx.tools.register({
      name: "generate_boleto",
      description: "Generate a mock Febraban-style boleto URL + barcode.",
      input_schema: {
        type: "object",
        properties: {
          lead_id: { type: "string" },
          amount: { type: "number" },
          due_date: { type: "string" },
        },
        required: ["lead_id", "amount", "due_date"],
      },
      handler: generate_boleto,
    });

    ctx.tools.register({
      name: "mark_agreement",
      description: "Record the outcome of the call (agreed / refused / callback).",
      input_schema: {
        type: "object",
        properties: {
          lead_id: { type: "string" },
          status: { type: "string", enum: ["agreed", "refused", "callback"] },
        },
        required: ["lead_id", "status"],
      },
      handler: mark_agreement,
    });
  }

  return {
    name: "cobranca",
    version: "0.1.0",
    tools: ["lookup_debt", "generate_boleto", "mark_agreement"],
    commands: ["/cobranca"],
  };
}
