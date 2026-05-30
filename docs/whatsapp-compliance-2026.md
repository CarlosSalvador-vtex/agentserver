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

### 🟡 Operational (onboarding + infra decisions)

**OPS-1 — Per-tenant WhatsApp number registration**
Each deployed WhatsApp phone number must be registered under a WhatsApp Business Account
belonging to the **tenant** (the business using agentserver), not a shared
agentserver/OpenClaw account. If multiple tenant bots share a single BSP account, Meta
may flag the aggregate behavior as a general-purpose platform.

Action: document in the onboarding runbook that tenants must provide their own WhatsApp
phone number registered under their own Meta Business Suite account. agentserver acts as
a BSP/ISV, not as the first-party account holder.

**OPS-2 — HSM template approval for proactive outbound (cobrança)**
WhatsApp Cloud API requires pre-approved Message Templates (HSM) for proactive outbound
messages (bot-initiated). The cobrança automation (Automations PR1–3) fires outbound
messages — these must use approved templates.

Action: before enabling cobrança automation in production, submit and approve a WhatsApp
Message Template for the debt notification flow. Also flag for legal review: debt
collection messaging in Brazil may face PROCON/BACEN regulations independent of Meta
policy.

**OPS-3 — Regional policy monitoring (Brazil, EU)**
Brazil is currently excluded from the ban; the EU situation is evolving. Confirm current
regional policy before go-live in these jurisdictions.

**OPS-4 — Acceptable-use policy in product ToS**
Document in the product Terms of Service that tenants may only deploy bots with
specific, defined purposes (not general-purpose AI assistants) via the WhatsApp channel.
This transfers compliance responsibility to tenants and provides a contractual backstop
if a tenant misuses the platform.

## Summary Table

| Item | Type | Priority | Status |
|---|---|---|---|
| Content guardrails middleware (B14) | Code | HIGH | Spec → `docs/cursor-handoffs/B14-whatsapp-content-guardrails.md` |
| Per-tenant phone number registration | Ops/runbook | HIGH | Pending |
| HSM templates for cobrança automation | Ops/legal | HIGH (before prod) | Pending |
| Regional policy monitoring (BR, EU) | Ops | MEDIUM | Pending |
| Acceptable-use ToS clause | Legal/business | MEDIUM | Pending |

## References

- TechCrunch (2025-10-18): WhatsApp changes its terms to bar general-purpose chatbots
- Meta/Facebook Developers: WhatsApp Business Platform Policy
- Internal: `internal/imbridge/whatsapp_provider.go`, `internal/imbridgesvc/handlers.go:900-1010`
- Internal: `docs/multibot-im-simulation-plan.md`
