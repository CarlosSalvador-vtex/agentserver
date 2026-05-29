# Cursor Agent Context

> Updated: 2026-05-29T00:00:00Z
> Branch: feat/telegram-openclaw-gate
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement spec **B12** — allow Telegram channels to bind **OpenClaw** sandboxes, not just
nanoclaw. Today `internal/imbridgesvc/handlers.go:405` hard-gates the Telegram configure
handler to `sbx.Type == "nanoclaw"` (400 otherwise), which blocks skill-running OpenClaw
bots from binding Telegram for the multi-bot simulation. Relax the gate to an
`{openclaw, nanoclaw}` allowlist.

## Source of truth

`docs/cursor-handoffs/B12-telegram-openclaw-binding.md` — the full spec (context, current
state, proposed change, acceptance criteria, test plan, rollback). Read it first. Decision
locked: D1 = allowlist `{openclaw, nanoclaw}` (reject other types like `hermes` with 400).

Key finding from the spec: inbound (poller → `forwardToCodex` → `/codex/turn` →
`processTurn`) and outbound (`/imbridge/send` → telegram provider) already route via the
codex path, which is **sandbox-type-agnostic**. So the nanoclaw gate is the ONLY blocker.

## Files in Scope

- `internal/imbridgesvc/handlers.go:405` — the gate inside `handleIMTelegramConfigure`.
  Replace:
  ```go
  if sbx.Type != "nanoclaw" {
      http.Error(w, "telegram binding is only available for nanoclaw sandboxes", http.StatusBadRequest)
      return
  }
  ```
  with a call to a small pure helper:
  ```go
  if !telegramBindAllowedType(sbx.Type) {
      http.Error(w, "telegram binding requires an openclaw or nanoclaw sandbox", http.StatusBadRequest)
      return
  }
  ```
  Add the helper in the same file:
  ```go
  // telegramBindAllowedType reports whether a sandbox of the given type may bind a
  // Telegram channel. Inbound/outbound routing is codex-path (type-agnostic); this
  // allowlist just keeps unsupported types (e.g. hermes) out.
  func telegramBindAllowedType(sandboxType string) bool {
      return sandboxType == "openclaw" || sandboxType == "nanoclaw"
  }
  ```
- `internal/imbridgesvc/telegram_configure_test.go` — NEW. Unit-test the helper
  (mirror the pure-helper test pattern in `whatsapp_hmac_test.go`):
  openclaw → true, nanoclaw → true, hermes → false, "" → false.
- Confirm `handleIMTelegramDisconnect` + poller lifecycle have NO separate nanoclaw
  assumption (grep `nanoclaw` in `internal/imbridgesvc/` + `internal/imbridge/`). If one
  exists, note it in the Progress Log (do NOT expand scope without flagging).

## Constraints

- No force-push. No new deps. ONE-line gate change + helper + test — do NOT refactor the
  handler or touch the codex turn path (it already works type-agnostically).
- Do NOT change WhatsApp / weixin / matrix behavior.
- Build tag `goolm` in all Go commands.
- No migration, no schema, no OpenAPI change (error-string + logic only). If `make openapi`
  detects drift you didn't intend, revert it.
- Do NOT commit `web/dist/`. One PR.

## Next Action

Read `docs/cursor-handoffs/B12-telegram-openclaw-binding.md`, then edit
`internal/imbridgesvc/handlers.go:405` + add the `telegramBindAllowedType` helper.

## Done When

- `go build -tags goolm ./...` + `go vet -tags goolm ./...` pass.
- `handleIMTelegramConfigure` accepts `openclaw` and `nanoclaw` past the type gate, rejects
  other types with 400 "telegram binding requires an openclaw or nanoclaw sandbox".
- `telegramBindAllowedType` unit test passes (openclaw/nanoclaw true; hermes/"" false).
- WhatsApp/weixin/matrix paths untouched.
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-29T00:00:00Z STARTED — orchestrator reseeded context for B12 (telegram→openclaw gate)
