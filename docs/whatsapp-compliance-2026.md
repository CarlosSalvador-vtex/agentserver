# WhatsApp AI Chatbot Policy — Compliance Assessment (2026)

> Assessed 2026-05-30 via workflow research. Sources: TechCrunch (2025-10-18),
> Meta/Facebook developers documentation, follow-up coverage through March 2026.

## TL;DR

The Meta/WhatsApp ban on general-purpose AI chatbots via the Business Platform API is
**real and in effect** (globally since 2026-01-15, Brazil excluded). It **does not affect
this product** as currently designed: agentserver/OpenClaw Enterprise deploys
task-specific, business-scoped bots (sales, collections, support) — all categories
explicitly permitted by Meta. Risk level: **LOW**. Six action items to close gaps.

## What the Ban Says

- **Effective:** 2026-01-15 (new accounts from 2025-10-15).
- **Banned:** General-purpose AI assistants distributed via the WhatsApp Business API —
  standalone chatbots whose primary purpose is open-ended, assistant-style interaction
  (examples: ChatGPT/OpenAI, Perplexity, Microsoft Copilot-style deployment).
- **Allowed:** Task-specific, business-scoped bots deployed by a business to serve that
  business's own customers within a defined scope. Explicitly permitted:
  customer support, order management, sales assistance, appointments/reservations,
  notifications/alerts, surveys.
- **Applies to:** WhatsApp Business Platform (Cloud API / Business API) — not the
  consumer WhatsApp app.
- **Regional exceptions:** Brazil excluded from the ban (2026-01-15). EU under
  investigation; Meta announced paid access for rival AI chatbots in Europe (March 2026).
  Monitor before deploying in these jurisdictions.

## This Product (agentserver/OpenClaw Enterprise)

- **API used:** WhatsApp Business Cloud API (Meta Graph API v18.0), confirmed in
  `internal/imbridge/whatsapp_provider.go` — posts to `graph.facebook.com/v18.0/{phone_number_id}/messages` with a Bearer token + webhook verification.
- **Deployment model:** Multi-tenant B2B SaaS — businesses deploy bots to serve their
  own customers. Each bot has a specific persona and defined scope.

### Use Case Compliance

| Bot persona | Meta category | Compliant? | Notes |
|---|---|---|---|
| Vendas (sales) | "sales assistance" | ✅ | Explicitly permitted |
| Cobrança (collections) | "account/payment management" | ✅ | Fits allowed bucket |
| SAC (customer support) | "customer support" | ✅ | Explicitly permitted |

The claim **"workflow-based agents are naturally compliant"** is accurate for this
product — each persona has a narrow, predefined role and targeted prompt, not open-ended
general-purpose behavior.

## Action Items

### 🔴 Code (implementable now)

**B14 — Content guardrails middleware**
Today there is zero content filtering in the code:
- Inbound: `handlers.go` webhook dispatches raw text directly to `Bridge.DispatchInbound`
  with only HMAC signature verification (integrity, not policy).
- Outbound: `whatsapp_provider.go` `Send()` has no content moderation.

Risk: if a bot produces an out-of-scope or harmful message, Meta can suspend the phone
number under the "intended design and strategic focus" clause.

Fix: lightweight middleware that (1) validates inbound messages are within scope before
forwarding to the agent, and (2) checks outbound agent responses before sending.
See `docs/cursor-handoffs/B14-whatsapp-content-guardrails.md`.

**Status:** ✅ Shipped in PR #158 (migration 048, `GuardrailsChecker` interface +
`NoopGuardrails` + `ScopeGuardrails` via llmproxy). Opt-in per channel; fail-open when
llmproxy is unreachable. Telegram/weixin/Matrix are unaffected.

**How to activate for a WhatsApp bot (production checklist):**

1. Decide the scope description for the bot — a 1-2 sentence description of what the
   bot is allowed to discuss. Example for cobrança:
   ```
   "Agente de cobrança da Acme. Escopo: regularização de dívidas, confirmação de
   identidade e parcelamento. Não responde sobre outros temas."
   ```

2. Set it on the channel via the API (maintainer or owner role required):
   ```http
   PATCH /api/workspaces/{workspace_id}/im/channels/{channel_id}
   Content-Type: application/json

   { "scope_description": "Agente de cobrança da Acme. Escopo: ..." }
   ```

3. Verify activation: send an out-of-scope message to the bot (e.g. "me diga a
   previsão do tempo"). The bot must reply with a redirect message and NOT forward
   the message to the agent. Check imbridge logs for `guardrail_blocked`.

4. If no `scope_description` is set, the channel uses `NoopGuardrails` (always
   allowed) — no change to existing behavior. This is the safe default for channels
   not yet configured.

5. The `LLMPROXY_URL` environment variable must be set in the imbridge pod for
   `ScopeGuardrails` to work. If unreachable, the guardrail fails open (all messages
   pass through) and logs `infra_allow`.

### 🟡 Operational (onboarding + infra decisions)

**OPS-1 — Per-tenant WhatsApp number registration**

**Why it matters:** if multiple tenant bots share a single BSP account or phone number,
Meta sees the aggregate traffic as a general-purpose platform (not individual business
bots), which is banned. Each phone number must belong to the specific business deploying
the bot, not to agentserver/OpenClaw as the SaaS provider.

**What needs to happen:**
- Each tenant must register their own WhatsApp phone number under their own **Meta
  Business Suite** account and grant agentserver API access (System User token or BSP
  delegation). agentserver acts as a BSP/ISV — it makes the technical connection but the
  number belongs to the client.
- This is already technically supported: `POST /api/workspaces/{id}/im/whatsapp/configure`
  accepts `phone_number_id` + `access_token` from the tenant's Meta account. The gap is
  that this is not documented as a **compliance requirement** in the onboarding runbook.
- The agentserver operator must NOT register a pool of numbers in their own Meta account
  and sub-lease them to tenants — that model violates the policy.

**Who / when:** Product + Engineering, in the onboarding runbook, before first
commercial customer. No code changes needed.

---

**OPS-2 — HSM template approval for proactive outbound (cobrança)**

**Why it matters:** WhatsApp Business Cloud API requires **pre-approved Message
Templates (HSM — Highly Structured Messages)** for any message *initiated by the bot*
(outbound/proactive). Sending free-form text as the first message in a session is only
allowed if the user messaged the bot first in the last 24h (the "customer service window").
The cobrança Automation (Automations PR1–3) fires outbound messages to customers — this
is bot-initiated, so it requires an approved template.

**What are HSM templates:** structured messages with fixed text + named placeholders,
registered in Meta Business Suite and reviewed by Meta (1-3 business days). Example:

```
Template name: cobranca_notificacao_v1
Body: "Olá {{1}}, sou da Acme. Identificamos uma pendência de {{2}} vencida em {{3}}.
Para regularizar, confirme os 3 últimos dígitos do seu CPF."
```

Once approved, the `whatsapp_provider.go` `Send()` must be updated to accept a
`template_name` + `components` payload (instead of free-form text) for the first
proactive message. Subsequent replies within the 24h window can use free-form text.

**Brazilian layer (PROCON/BACEN):** independent of Meta policy, debt collection via
messaging in Brazil is regulated. Key rules: allowed hours (8h-20h weekdays, 8h-14h
Saturdays), maximum contact frequency, mandatory opt-out mechanism, identification of the
creditor. These are legal requirements regardless of WhatsApp's own policy.

**Who / when:** Product + Jurídico, before enabling cobrança Automations in production.
Requires: (1) template submission and approval in Meta Business Suite; (2) code change in
`whatsapp_provider.go` to send template on first message; (3) legal review of the
message content against PROCON/BACEN rules.

---

**OPS-3 — Regional policy monitoring (Brazil, EU)**

**Brazil:** excluded from the ban by judicial order effective 2026-01-15 (court ordered
Meta to suspend enforcement in Brazil). This exemption may be reversed if the order is
overturned. Before deploying commercially in Brazil, confirm the current status — the ban
could be reinstated on short notice. **Action:** assign a named owner (Jurídico/CSM) to
check WhatsApp Business Platform policy news in Brazil before each new customer contract
in that territory.

**European Union:** opened an investigation in December 2025. In March 2026, Meta
announced it would allow rival AI chatbots in Europe but for a fee. The model is still
evolving. Before deploying in EU jurisdictions, confirm: (a) whether the paid-access
model applies to agentserver's use case; (b) whether GDPR-specific data handling
requirements apply to bot conversations stored in the platform. **Action:** same as
Brazil — legal review per contract, not a blanket clearance.

**Who / when:** Jurídico/CSM at each customer onboarding in BR or EU. No code changes
needed; this is a go/no-go checkpoint in the sales/onboarding process.

---

**OPS-4 — Acceptable-use policy in product ToS**

**Why it matters:** if a tenant deploys a general-purpose AI assistant via the
agentserver WhatsApp channel, Meta can ban the tenant's phone number — and potentially
the BSP account — impacting other tenants. Without a contractual backstop, agentserver
bears operational risk for tenant misuse.

**What needs to be in the ToS:**
1. "The WhatsApp channel may only be used for bots with a specific, defined business
   purpose (customer support, sales, collections, scheduling, notifications). General-
   purpose AI assistants are prohibited."
2. "The tenant is responsible for compliance with the WhatsApp Business Platform Terms of
   Service, including obtaining required Message Template approvals for proactive outbound
   messages."
3. "agentserver reserves the right to suspend WhatsApp access for any workspace where a
   bot violates Meta's Acceptable Use Policy."
4. "The tenant warrants that their WhatsApp phone number is registered under their own
   Meta Business Suite account and that they are the first-party holder of that number."

**Who / when:** Jurídico, before first commercial contract. This is a one-time document
update that covers all future tenants.

## Summary Table

| Item | Type | Priority | Status |
|---|---|---|---|
| Content guardrails middleware (B14) | Code | HIGH | ✅ Shipped PR #158 — activate via `PATCH` `scope_description` on channel |
| Per-tenant phone number registration | Ops/runbook | HIGH | Pending |
| HSM templates for cobrança automation | Ops/legal | HIGH (before prod) | Pending |
| Regional policy monitoring (BR, EU) | Ops | MEDIUM | Pending |
| Acceptable-use ToS clause | Legal/business | MEDIUM | Pending |

## References

- TechCrunch (2025-10-18): WhatsApp changes its terms to bar general-purpose chatbots
- Meta/Facebook Developers: WhatsApp Business Platform Policy
- Internal: `internal/imbridge/whatsapp_provider.go`, `internal/imbridgesvc/handlers.go:900-1010`
- Internal: `docs/multibot-im-simulation-plan.md`
