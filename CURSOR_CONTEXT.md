# Cursor Agent Context

> Updated: 2026-05-30T00:00:00Z
> Branch: feat/skills-real-endpoints
> Status: IN_PROGRESS
> Model: composer-2.5

## Active Task

Implement **B1 — Skills com endpoints reais**. Full spec in
`docs/cursor-handoffs/B1-skills-real-endpoints.md`. Make the cobrança skill's
`lookup_debt` tool fetch from an HTTP endpoint (`GET /api/sim/cobranca/lookup`) instead
of the local `leads.json` fixture, with fail-open fallback to the fixture.

## Source of truth

`docs/cursor-handoffs/B1-skills-real-endpoints.md` — read first (3-layer pattern,
acceptance criteria, files, effort).

## Key decisions (from spec)

- New `internal/server/sim_endpoints.go`: `GET /api/sim/cobranca/lookup?cpf_last_3=XXX`
  → `{found: bool, lead?: {...}}`. Synthetic LGPD-safe data embedded in Go (same 3 leads
  as the fixture, marked TEST). No auth (internal sim endpoint).
- Register route in `server.go`.
- Inject `SIM_API_BASE_URL=http://agentserver:8080` into sandbox `containerEnv` in
  `internal/sandbox/manager.go` (both creation paths). Comment the multi-tenant
  extension point (per-workspace override is a later PR).
- `index.mjs` `lookup_debt.execute`: fetch from `${SIM_API_BASE_URL}/api/sim/cobranca/lookup`
  with 5s timeout; on error OR missing env → fall back to local `findLeadByCpfLast3`.
- Only `lookup_debt` in this PR. generate_boleto + mark_agreement stay fixture-based
  (replicate later). vendas/SAC endpoints later.

## Files in Scope

- `internal/server/sim_endpoints.go` — NEW: handler + embedded synthetic leads.
- `internal/server/sim_endpoints_test.go` — NEW: found + not-found tests.
- `internal/server/server.go` — register `GET /api/sim/cobranca/lookup`.
- `internal/sandbox/manager.go` — inject `SIM_API_BASE_URL` env var.
- `deploy/helm/agentserver/skills/cobranca/index.mjs` — `lookup_debt` fetch + fallback.

## Constraints

- No force-push. No new top-level deps. Data stays 100% synthetic LGPD-safe.
- Fallback MUST preserve current behavior (fail-open: no env or unreachable → fixture).
- Build tag `goolm` in all Go commands. Run `make openapi` if you add OpenAPI annotations
  to the new endpoint (optional for sim endpoint — keep minimal).
- Do NOT commit `web/dist/`. One PR.

## Next Action

Read `docs/cursor-handoffs/B1-skills-real-endpoints.md` + `internal/server/server.go`
(find route registration block) + `internal/sandbox/manager.go` (find `containerEnv`),
then create `sim_endpoints.go` with the handler + embedded leads.

## Done When

- `go build -tags goolm ./...` + `go vet` pass.
- `GET /api/sim/cobranca/lookup?cpf_last_3=111` → found L-001; `?cpf_last_3=999` → not found.
- `lookup_debt` fetches from endpoint when `SIM_API_BASE_URL` set; falls back to fixture
  when unset/unreachable.
- Unit tests pass (found + not-found).
- PR opened against `main`; CI green. Update this file: Status=AWAITING_MERGE + PR URL
  under `## PR Ready for Merge`.

## Progress Log

<!-- Cursor appends one line here after each response -->
- 2026-05-30T00:00:00Z STARTED — orchestrator reseeded context for B1 (skills real endpoints)
