// Cobrança pt-BR — OpenClaw plugin with native plugin-sdk integration.
//
// Registers:
//   - persona injection via before_prompt_build hook (replaces text-based prompt hack)
//   - lookup_debt tool — fetch lead by CPF last 3 digits from leads.json fixture
//   - generate_boleto tool — mock boleto link generation
//   - mark_agreement tool — record agreement status
//
// Requires the openclaw/plugin-sdk symlink shim (fix/esm-skill-resolution PR
// added a node_modules sibling next to this file so ESM `import` works).
//
// Mock-only — fixtures in ./references/leads.json are LGPD-safe synthetic
// records. NEVER swap in real PII.

import fs from "node:fs";
import path from "node:path";
import { definePluginEntry } from "openclaw/plugin-sdk/core";

const HERE = path.dirname(new URL(import.meta.url).pathname);

const PROMPT_BODY = fs.readFileSync(path.join(HERE, "prompt.md"), "utf8");
const LEADS = JSON.parse(
  fs.readFileSync(path.join(HERE, "references", "leads.json"), "utf8"),
);

// In-memory store for agreements within a single sandbox session.
// Acceptable for a mock — real impl would persist to DB.
const agreements = new Map();

const PERSONA_SYSTEM = `<cobranca-persona>
You are operating with the cobranca persona (Júlia, Acme Cobranças).
Always respond in pt-BR with short, professional messages.

LGPD: NEVER reveal amount, due_date, or creditor before the user confirms
their identity by providing the last 3 digits of their CPF. Use the
\`lookup_debt\` tool with those 3 digits to fetch the real record; do
NOT invent values.

Available tools (call them deterministically — do not paraphrase outputs):
  - lookup_debt(cpf_last_3): returns the lead record or { found: false }
  - generate_boleto(lead_id, amount_final, due_date): returns a mock link
  - mark_agreement(lead_id, status): records "agreed" | "refused" | "callback"

Full operating script (treat as authoritative):
${PROMPT_BODY}
</cobranca-persona>
`;

function normalizeCpfDigits(input) {
  return String(input ?? "").replace(/\D/g, "");
}

function findLeadByCpfLast3(cpfLast3) {
  const last3 = normalizeCpfDigits(cpfLast3).slice(-3);
  if (last3.length !== 3) return null;
  return LEADS.find((lead) => lead.cpf_last_3 === last3) ?? null;
}

function findLeadById(leadId) {
  return LEADS.find((lead) => lead.lead_id === leadId) ?? null;
}

export default definePluginEntry({
  id: "cobranca",
  name: "Cobrança pt-BR (mock)",
  description:
    "Agente de cobrança LGPD-safe pt-BR (Júlia/Acme). Fixtures sintéticos. NÃO usar com dados reais.",
  register(api) {
    api.logger?.info?.(
      `[cobranca] loaded via plugin-sdk — ${LEADS.length} leads, prompt ${PROMPT_BODY.length} chars`,
    );

    // Persona injection. Runs every turn before the LLM sees the prompt.
    api.on(
      "before_prompt_build",
      async () => ({ prependSystemContext: PERSONA_SYSTEM }),
      { name: "cobranca-persona" },
    );

    api.registerTool(
      (_ctx) => ({
        name: "lookup_debt",
        label: "Cobrança — Consultar Dívida",
        description:
          "Consulta um lead no fixture local de cobrança usando os 3 últimos dígitos do CPF. " +
          "Retorna o registro completo (name_masked, amount, due_date, creditor, status, note) " +
          "ou { found: false }. NÃO invente valores — sempre chame essa tool antes de citar dados.",
        parameters: {
          type: "object",
          additionalProperties: false,
          properties: {
            cpf_last_3: {
              type: "string",
              description: "Últimos 3 dígitos do CPF do cliente (ex: '111').",
              minLength: 3,
              maxLength: 4,
            },
          },
          required: ["cpf_last_3"],
        },
        async execute(_toolCallId, rawParams) {
          const lead = findLeadByCpfLast3(rawParams?.cpf_last_3);
          if (!lead) {
            return { found: false, cpf_last_3: rawParams?.cpf_last_3 };
          }
          return { found: true, lead };
        },
      }),
      { name: "lookup_debt" },
    );

    api.registerTool(
      (_ctx) => ({
        name: "generate_boleto",
        label: "Cobrança — Gerar Boleto",
        description:
          "Gera um link de boleto MOCK para um lead. Retorna { boleto_url, lead_id, amount_final }.",
        parameters: {
          type: "object",
          additionalProperties: false,
          properties: {
            lead_id: { type: "string", description: "Lead ID (ex: L-001)." },
            amount_final: {
              type: "number",
              description: "Valor final após desconto/negociação.",
              minimum: 0,
            },
            due_date: {
              type: "string",
              description: "Nova data de vencimento (YYYY-MM-DD).",
            },
          },
          required: ["lead_id", "amount_final", "due_date"],
        },
        async execute(_toolCallId, rawParams) {
          const lead = findLeadById(rawParams?.lead_id);
          if (!lead) {
            return {
              error: "lead_not_found",
              lead_id: rawParams?.lead_id,
            };
          }
          return {
            boleto_url: `https://mock.cobranca.local/boleto/${lead.lead_id}?v=${Date.now()}`,
            lead_id: lead.lead_id,
            amount_final: rawParams.amount_final,
            due_date: rawParams.due_date,
          };
        },
      }),
      { name: "generate_boleto" },
    );

    api.registerTool(
      (_ctx) => ({
        name: "mark_agreement",
        label: "Cobrança — Registrar Acordo",
        description:
          "Registra o desfecho da negociação para um lead. status ∈ { agreed, refused, callback }.",
        parameters: {
          type: "object",
          additionalProperties: false,
          properties: {
            lead_id: { type: "string", description: "Lead ID (ex: L-001)." },
            status: {
              type: "string",
              enum: ["agreed", "refused", "callback"],
              description: "Resultado da negociação.",
            },
            notes: {
              type: "string",
              description: "Observações curtas (opcional).",
            },
          },
          required: ["lead_id", "status"],
        },
        async execute(_toolCallId, rawParams) {
          const lead = findLeadById(rawParams?.lead_id);
          if (!lead) {
            return { error: "lead_not_found", lead_id: rawParams?.lead_id };
          }
          agreements.set(rawParams.lead_id, {
            status: rawParams.status,
            notes: rawParams.notes ?? null,
            at: new Date().toISOString(),
          });
          return {
            ok: true,
            lead_id: rawParams.lead_id,
            status: rawParams.status,
          };
        },
      }),
      { name: "mark_agreement" },
    );
  },
});
