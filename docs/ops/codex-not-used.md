# Codex (OpenAI) subsystem — NOT used in this deployment

> **Decision (2026-05-28):** The OpenAI **Codex** integration is **not part of this
> product**. It stays in the tree for now (dormant), but **do not extend it** and
> **do not wire new features to it**. Removal is deferred to a dedicated cleanup —
> see "Removal plan" below. This doc is the map so a future cleanup is surgical.

## What "codex" means here

Two related-but-separable things, both tied to **OpenAI Codex** (OpenAI's coding
agent CLI):

1. **Auth/identity bridge** — `internal/codexauth`. A self-hosted OIDC/PKCE/device-flow
   shim that mints **OpenAI-shaped** Agent Identity JWTs (issuer
   `https://chatgpt.com/codex-backend/agent-identity`, claims under
   `https://api.openai.com/auth`, `chatgpt_account_id`, `chatgpt_plan_type`). Lets a
   Codex CLI `codex login --issuer …` authenticate against agentserver. Mounted under
   `/codex-auth/*`.
2. **Execution stack** — gateways that run the Codex CLI as an agent backend:
   `cmd/codex-app-gateway`, `cmd/codex-exec-gateway`, `internal/codexappgateway`,
   `internal/codexexecgateway`, plus IM routing mode `"codex"`, executors, browser
   sessions, and workspace bearer tokens (`codex_tokens`).

The product's real agent backends are **OpenClaw** and **Hermes** (see
`docs/playground-design.md`, sandbox types). Codex is one extra backend we are not
shipping.

## Inventory (do-not-extend surface)

| Area | Paths |
|------|-------|
| Auth bridge | `internal/codexauth/` |
| Gateways (bin) | `cmd/codex-app-gateway/`, `cmd/codex-exec-gateway/` |
| Gateway pkgs | `internal/codexappgateway/` (+ `codexhome/`, `auth/`), `internal/codexexecgateway/` |
| Server handlers | `internal/server/codex_tokens*.go`, `codex_executors*.go`, `codex_browsers.go`, `codex_browser_sessions_internal.go`, `codex_client*.go`, `codex_im_inbound*.go`, `codex_dispatcher_test.go` |
| DB | `internal/db/codex_tokens.go`, `codex_browser_sessions.go`, `agent_sessions_codex_test.go` |
| Migrations (applied — do NOT delete files) | `022_codex_remote_tokens`, `024_codex_thread_id`, `025_codex_auth`, `026_codex_browser_sessions`, `027_im_routing_default_codex`, `029_purge_codex_remote_tokens_legacy_hash` |
| Frontend | `web/src/components/CodexTokensPanel.tsx` + codex types in `web/src/lib/api.ts` |
| Routes | `/codex-auth/*`, `/api/internal/codex/tokens/{verify,session-*}`, codex token CRUD, IM `routing_mode="codex"` |
| Wiring | `cmd/serve.go` (`srv.CodexAuth = &codexauth.Server{…}`), `internal/server/server.go` (`CodexAuth`, `CodexAuthIssuerURL`, `OIDC.CodexAuthHost`) |
| Docs | `docs/superpowers/{plans,specs}/*codex*` (30+ historical), `docs/api/reference/codex-{tokens,browser-sessions}.md` |

## Coupling notes (matter for any future removal)

- `internal/codexauth` is imported by `cmd/serve.go`, `internal/server/server.go`,
  and `internal/server/codex_executors.go` (the "Add connector" path mints an OpenAI
  Agent Identity JWT). It is **NOT** imported by the execution gateways.
- `codex_tokens` (DB + `/api/internal/codex/tokens/verify`) is consumed by
  `internal/codexappgateway/auth/remote_verifier.go`. So the auth bridge and the
  workspace bearer tokens are **separable** — removing one does not force the other.
- Migrations 022–029 are already applied to dev/prod. Removal means a **new forward
  migration** that drops the codex tables/columns — never delete an applied migration
  file (breaks fresh-DB history).

## Removal plan (deferred — when we decide to delete)

Phased, smallest-blast-first:

1. **Auth bridge only** (clean island): delete `internal/codexauth`, drop the
   `/codex-auth/*` mount + `CodexAuth`/`CodexAuthIssuerURL`/`OIDC.CodexAuthHost`
   wiring, and remove the `MintAgentIdentity` branch from `codex_executors.go`
   (executor registration stays). Forward migration drops what `025_codex_auth`
   created. Keep `codex_tokens` + gateways intact.
2. **Execution stack** (separate, larger PR set): only if we also drop the Codex
   *backend* — gateways, IM `codex` routing, browser sessions, executors, tokens.
   Keep OpenClaw/Hermes.

## Impact on B08 (cookie host-only vs cross-tenant SSO)

`docs/cursor-handoffs/B08-codex-auth-vs-cookie.md` framed the host-only-cookie conflict
partly around codex-auth's "1 SSO token for all tenants" expectation. **With codex out
of scope, that pressure disappears.** B08 collapses toward **Path A (host-only,
status quo)** as the safe default; **Path C (single-use redirect token)** remains
worth doing purely for human multi-workspace UX, but is no longer blocking any Codex
SSO requirement.
