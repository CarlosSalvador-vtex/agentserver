# B14 — WhatsApp content guardrails middleware

> Compliance requirement surfaced by the WhatsApp AI chatbot ban assessment
> (docs/whatsapp-compliance-2026.md, 2026-05-30). GitHub Issues disabled → handoff doc.

## Context

Meta's WhatsApp Business Platform policy (effective 2026-01-15) bans general-purpose AI
assistants and allows only task-specific, business-scoped bots. This product's use cases
(vendas, cobrança, SAC) are compliant by design, but there is **zero content enforcement
in the code**: inbound messages are dispatched raw to the agent without scope validation,
and outbound replies are sent without any safety check. If a bot produces an out-of-scope
or harmful message, Meta can suspend the phone number under the "intended design and
strategic focus" clause.

## Current state (verified)

- `internal/imbridgesvc/handlers.go` WhatsApp webhook handler (`handleWhatsAppWebhookInbound`
  ~line 900-1010): performs only HMAC signature verification, then calls
  `Bridge.DispatchInbound` with raw text — no topic filtering.
- `internal/imbridge/whatsapp_provider.go` `Send()`: posts directly to Meta API with no
  outbound content check.
- No content moderation exists anywhere in the inbound or outbound WhatsApp path.

## Proposed change

Add a lightweight **scope guardrail** layer — two hooks, one inbound, one outbound:

### Inbound guard (pre-dispatch)

Before `Bridge.DispatchInbound`, check if the inbound message is within the scope of the
bot bound to that WhatsApp channel. Out-of-scope messages get a polite redirect reply
("Posso ajudar com [propósito do bot]. Para outros assuntos, entre em contato por outro canal.")
and are NOT forwarded to the agent.

Implementation:
- Add `GuardrailsChecker` interface in `internal/imbridge/` (or `internal/server/`):
  ```go
  type GuardrailsChecker interface {
      CheckInbound(ctx context.Context, channelID, text string) (allowed bool, reason string)
      CheckOutbound(ctx context.Context, channelID, text string) (allowed bool, reason string)
  }
  ```
- Default implementation: `NoopGuardrails` (always allowed — for channels without a
  configured guardrail, preserves existing behavior for Telegram/weixin/Matrix).
- WhatsApp-specific implementation: `ScopeGuardrails` — uses the channel's bound sandbox
  soul/persona description (from `automations.Config` or a new `channel_scope` field in
  `workspace_im_channels`) as the scope definition. Calls an LLM (via llmproxy) to
  classify whether the message is within scope. Cache results (e.g. 60s) by (channelID,
  normalized_text_hash) to avoid per-message LLM calls in practice.
- Wire in the WhatsApp webhook handler and in `whatsapp_provider.go` `Send()`.
- Keep the guardrail **optional and per-channel**: channels without a configured scope
  description skip the check (NoopGuardrails).

### Outbound guard (pre-send)

Before `whatsapp_provider.go` `Send()` posts to Meta, run a lightweight safety check on
the agent's reply:
- Block or redact replies that contain: (a) content violating Meta's commerce policy
  (e.g. explicit illegal offers), (b) PII the agent should not repeat back (e.g.
  full CPF echoed), (c) text that clearly falls outside the bot's configured scope.
- On block: log + substitute a safe fallback message.

### Minimal viable scope definition

Store a `scope_description` string on the channel (or derive it from the soul's
`description` field in `soul_drafts`). Example for cobrança:
```
"Agente de cobrança da Acme. Escopo: regularização de dívidas, parcelamento e
confirmação de identidade. Não responde sobre outros temas."
```

## Acceptance criteria

1. Inbound message clearly out of scope for the bot (e.g. "me diga a previsão do tempo"
   sent to a cobrança bot) → bot replies with redirect message, message NOT forwarded to
   the agent. Log the rejection.
2. Inbound message within scope → forwarded to agent as today (no change).
3. Outbound reply that echoes a full CPF or contains clearly prohibited content →
   blocked, safe fallback sent, incident logged.
4. Telegram / weixin / Matrix channels unaffected (NoopGuardrails path).
5. WhatsApp channel without configured scope_description → NoopGuardrails (opt-in, not
   forced).
6. `go build -tags goolm ./...` + `go vet` pass; unit tests for Noop path and scope-match
   path (with fake LLM client); build + vet for existing WhatsApp HMAC tests green.

## Out of scope

- HSM template approval for proactive outbound (OPS-2 in compliance doc — operational,
  not code).
- Per-tenant phone number enforcement (OPS-1 — onboarding runbook).
- Regional policy monitoring (OPS-3).
- Guardrails for Telegram/weixin/Matrix (can be added later via same interface).

## Files reference

| File | Change |
|---|---|
| `internal/imbridge/guardrails.go` | NEW: `GuardrailsChecker` interface + `NoopGuardrails` + `ScopeGuardrails` |
| `internal/imbridgesvc/handlers.go` | Wire inbound check before `Bridge.DispatchInbound` on WhatsApp path |
| `internal/imbridge/whatsapp_provider.go` | Wire outbound check in `Send()` |
| `internal/db/im_channels.go` or migration | Add `scope_description text` to `workspace_im_channels` (nullable) |
| `internal/db/migrations/047_*` | Already exists (047 = automations locked_until); use 048 |

## Effort

~M. Interface + Noop: ~30min. ScopeGuardrails (LLM classify): ~2h. Wiring + tests: ~1h.
Migration: ~15min. Total: ~4h.

## Related

- `docs/whatsapp-compliance-2026.md` — full compliance assessment.
- `internal/imbridgesvc/handlers.go` (`handleWhatsAppWebhookInbound`).
- `internal/imbridge/whatsapp_provider.go` (`Send()`).
