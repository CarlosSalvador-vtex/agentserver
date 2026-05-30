# Cursor Agent Context

> Updated: 2026-05-30T00:00:00Z
> Branch: feat/whatsapp-guardrails
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **B14 — WhatsApp content guardrails middleware**. Full spec in
`docs/cursor-handoffs/B14-whatsapp-content-guardrails.md`. Add a lightweight scope
guardrail to the WhatsApp inbound/outbound path so out-of-scope or unsafe messages are
caught before reaching the agent or the user.

## Source of truth

`docs/cursor-handoffs/B14-whatsapp-content-guardrails.md` — read this first (interface,
acceptance criteria, files to touch, effort).
`docs/whatsapp-compliance-2026.md` — compliance context (why this matters).

## Key decisions (from spec)

- `GuardrailsChecker` interface with two methods: `CheckInbound` + `CheckOutbound`.
- **`NoopGuardrails`** = always-allowed, default for all non-WhatsApp channels. Preserves
  existing Telegram/weixin/Matrix behavior unchanged.
- **`ScopeGuardrails`** = uses channel's `scope_description` (new nullable column in
  `workspace_im_channels`) + LLM call via llmproxy to classify inbound scope. Outbound:
  blocks if reply echoes PII (full CPF regex) or clearly out-of-scope. Cache classify
  results 60s by (channelID, sha256(normalized_text)[:8]).
- **Opt-in per channel**: if `scope_description` is NULL/empty → Noop (no forced
  breaking change on existing channels).
- Wire inbound check in `internal/imbridgesvc/handlers.go` WhatsApp webhook path
  (`handleWhatsAppWebhookInbound`), before `Bridge.DispatchInbound`.
- Wire outbound check in `internal/imbridge/whatsapp_provider.go` `Send()`.
- Migration **048** for `scope_description text` column (047 = automations locked_until,
  already exists — next is 048).

## Files in Scope

- `internal/imbridge/guardrails.go` — NEW: interface + NoopGuardrails + ScopeGuardrails.
  LLM call via llmproxy (same HTTP client as codex_im_inbound.go's postSend).
- `internal/imbridgesvc/handlers.go` — wire inbound check in the WhatsApp webhook handler
  (search for `handleWhatsAppWebhookInbound`; add guard before DispatchInbound call).
- `internal/imbridge/whatsapp_provider.go` — wire outbound check in `Send()`.
- `internal/db/migrations/048_channel_scope.sql` — NEW: `ALTER TABLE workspace_im_channels
  ADD COLUMN IF NOT EXISTS scope_description text;`
- `internal/db/im_channels.go` — add `ScopeDescription *string` to `IMChannel` struct +
  UPDATE query for setting it.
- `internal/imbridge/guardrails_test.go` — NEW: unit tests for Noop path (always allowed),
  ScopeGuardrails with fake LLM (in-scope → allowed, out-of-scope → blocked, PII in
  outbound → blocked).

## Constraints

- No force-push. No new top-level deps (use existing HTTP client + llmproxy).
- Telegram/weixin/Matrix paths MUST be unaffected (NoopGuardrails for all non-WhatsApp
  channels unless they also have a scope_description, which they won't initially).
- If llmproxy is unreachable → default to **allow** (don't block on infra failure).
- Build tag `goolm` in all Go commands. Run `make openapi` + `make api-docs` if you add
  any new API endpoints (you probably won't for this PR — guardrails are internal).
- Do NOT commit `web/dist/`. One PR.

## Next Action

Read `docs/cursor-handoffs/B14-whatsapp-content-guardrails.md` + `internal/imbridgesvc/handlers.go`
(find `handleWhatsAppWebhookInbound`) + `internal/imbridge/whatsapp_provider.go` (`Send()`),
then write migration `048_channel_scope.sql` + `GuardrailsChecker` interface + NoopGuardrails.

## Done When

- `go build -tags goolm ./...` + `go vet` pass.
- `CheckInbound`: out-of-scope msg blocked + redirect reply sent; in-scope msg forwarded.
- `CheckOutbound`: full CPF regex blocked + fallback sent; normal reply passes.
- Telegram/weixin/Matrix unaffected (Noop path, verified by existing tests).
- WhatsApp channel without scope_description → Noop.
- Unit tests pass (Noop + ScopeGuardrails fake-LLM paths).
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-30T00:00:00Z STARTED — orchestrator reseeded context for B14 (WhatsApp guardrails)
