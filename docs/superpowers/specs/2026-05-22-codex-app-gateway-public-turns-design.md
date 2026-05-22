# codex-app-gateway: public `/api/turns` + workspace API keys — Design

**Date**: 2026-05-22
**Author**: brainstorming with mryao
**Related**: Phase 1.a/1.b OpenAPI organization (PRs #152–#159), `docs/api/openapi.yaml` (agentserver REST spec)

## Goal

Make `POST /api/turns` on `codex-app.agent.cs.ac.cn` a publicly callable REST endpoint so external integrations (bots, IM bridges, webhooks) can submit turns to a workspace's codex without going through the WS path. Document it via OpenAPI following the conventions established in Phase 1.

To do this safely, introduce a **per-workspace developer API key** system: long-lived secrets that workspace maintainers mint via the SPA, present in `Authorization: Bearer wak_<...>` headers, and use to authenticate REST calls scoped to that workspace.

## Non-goals (this phase)

- OAuth 2.0 / PKCE flows for API keys (long-lived static secrets only)
- Resource-pattern / wildcard scopes (e.g. `turns:*`, `/api/threads/{id}:read`) — coarse action-based scopes only
- Role-based scope aliases (`viewer` / `editor` / `admin` shortcuts)
- Rate limiting / per-key quota (covered by existing workspace-level LLM quotas)
- Audit log of every key use (only `last_used_at` is tracked)
- Public exposure of WS endpoints on codex-app-gateway (only `/api/turns`)
- Other codex-app-gateway HTTP routes (`/healthz`, `/internal/connected`) — stay undocumented
- API key rotation tooling (revoke + mint a new one is the supported workflow)

## Architecture

```
                              codex-app.agent.cs.ac.cn
                                       │
                ┌──────────────────────┴──────────────────────┐
                │                                             │
        Authorization: Bearer wak_<...>           X-Internal-Secret: <secret>
        (public clients: bots, webhooks)          (in-cluster: imbridge)
                │                                             │
                ▼                                             ▼
    ┌────────────────────────────┐              ┌──────────────────────────┐
    │ requireBearerAPIKey        │              │ requireInternalSecret    │
    │ (codex-app-gateway/auth.go)│              │ (existing)               │
    │                            │              │                          │
    │  POST /internal/workspace- │              │  Header constant compare │
    │   api-keys/validate        │              │                          │
    │    → agentserver           │              └──────────────┬───────────┘
    │  → 200 {workspace_id,      │                             │
    │         key_id}            │                             │
    └────────────┬───────────────┘                             │
                 │                                             │
                 │   workspace_id from key                     │   workspace_id from body
                 │   MUST match body.workspaceId               │   (existing behavior)
                 │                                             │
                 └──────────────────┬──────────────────────────┘
                                    │
                                    ▼
                       turnAPIHandler.ServeHTTP
                       (existing /api/turns logic)
```

The two auth middlewares mount on the same route. Either passes the request through; both apply the same handler. The handler checks workspace consistency for the Bearer path (the secret is scoped to ONE workspace; can't be used to send turns to a different workspace).

```
                       agentserver (existing binary)
                              │
        SPA: WorkspaceDetail "API Keys" tab
                │
                ▼
    ┌──────────────────────────────────────────────┐
    │ Public REST (cookie auth, owner/maintainer)  │
    │  POST   /api/workspaces/{wid}/api-keys       │ ← mint, returns secret ONCE
    │  GET    /api/workspaces/{wid}/api-keys       │ ← list (prefix only)
    │  DELETE /api/workspaces/{wid}/api-keys/{id}  │ ← revoke (soft delete)
    └────────────────────┬─────────────────────────┘
                         │
                         ▼
                    ┌─────────────────────┐
                    │  workspace_api_keys │ (new table)
                    └─────────────────────┘
                         ▲
                         │
    ┌────────────────────┴─────────────────────────┐
    │ Internal RPC (X-Internal-Secret, NOT public) │
    │  POST /internal/workspace-api-keys/validate  │ ← called by codex-app-gateway
    │    body: {secret}                            │
    │    200:  {workspace_id, key_id}              │
    │    401:  unauthorized                        │
    └──────────────────────────────────────────────┘
```

## Data model

New table `workspace_api_keys`:

```sql
CREATE TABLE workspace_api_keys (
    id            TEXT        PRIMARY KEY,                    -- "wak_<8-char-prefix>"
    workspace_id  TEXT        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id       TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    prefix        TEXT        NOT NULL,                       -- duplicated from id for display
    secret_hash   TEXT        NOT NULL,                       -- hex(sha256(secret))
    scopes        TEXT[]      NOT NULL DEFAULT '{}',          -- e.g. ['turns:submit']
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX idx_workspace_api_keys_workspace_active
    ON workspace_api_keys (workspace_id)
    WHERE revoked_at IS NULL;
```

**Wire format of a key:**

```
wak_<8-char-prefix-base32>_<40-char-secret-base32>
^^^^                       ^^^^^^^^^^^^^^^^^^^^^^^
prefix used for indexing   secret hashed with SHA-256
(5 random bytes →          (25 random bytes → 40 base32 chars)
 8 base32 chars)
```

Total length: 4 + 8 + 1 + 40 = 53 characters. Both halves use Crockford base32 (no padding, no ambiguous chars).

**Why not bcrypt:** secrets are 25 bytes of CSPRNG output = ~200 bits of entropy. SHA-256 is collision/preimage-safe under that condition; bcrypt's slowness adds nothing.

**Lookup flow:**

1. Parse `Authorization: Bearer wak_<prefix>_<secret>`
2. `SELECT secret_hash, workspace_id, id, scopes FROM workspace_api_keys WHERE prefix = $1 AND revoked_at IS NULL`
3. `subtle.ConstantTimeCompare(stored_hash, sha256(presented_secret))`
4. On match: `UPDATE … SET last_used_at = NOW() WHERE id = $key_id` (best-effort, fire-and-forget)
5. Return `{workspace_id, key_id, scopes}`

`prefix` is the index lookup key. Two keys can't share a prefix (UNIQUE on `id`). The secret is **never** stored in plaintext.

## Scopes

Action-based scope strings, namespaced as `<resource>:<verb>`. Per-handler enforcement.

**v1 catalog** (single source of truth, server-side, in `internal/server/api_key_scopes.go`):

| Scope | Description | Enforced now? |
|---|---|---|
| `turns:submit` | Submit codex turns via `POST /api/turns` | ✅ |
| `turns:read` | List past turn history | ⏳ catalogued, no endpoint yet |
| `threads:create` | Start a fresh thread | ⏳ |
| `threads:cancel` | Cancel an in-flight turn | ⏳ |
| `threads:read` | Read thread history | ⏳ |
| `mailbox:read` | Read inbound mailbox messages | ⏳ |
| `mailbox:send` | Send to a mailbox | ⏳ |

Server-side mint rejects unknown scope strings AND rejects scopes that aren't yet `Available` — so a maintainer can't mint a `mailbox:send` key today even though it's listed (UI greys out unavailable scopes).

**Wire format:** the scope list is a JSON array of strings on every API key payload. Validated on mint, returned on list, included in the internal validate response.

**Enforcement model:** every public-facing handler that participates in the API-key auth path declares its required scope as a Go constant:

```go
const ScopeTurnsSubmit = "turns:submit"

// /api/turns handler:
if !slices.Contains(scopesFromContext(r), ScopeTurnsSubmit) {
    http.Error(w, "missing scope: turns:submit", http.StatusForbidden)
    return
}
```

`X-Internal-Secret` path bypasses scope check (imbridge is in-cluster trusted; it doesn't carry a scoped key).

**Catalog discovery endpoint:** `GET /api/workspaces/{wid}/api-keys/scopes` returns the catalog (scope name + description + availability flag) so the SPA renders checkboxes that auto-sync with the backend's source of truth. Workspace member auth (no special role required).

## Endpoints

### Agentserver (new)

Under tag `Workspace API Keys` in existing `docs/api/openapi.yaml`:

| Method | Path | Auth | Body | 200 Response |
|---|---|---|---|---|
| `POST` | `/api/workspaces/{wid}/api-keys` | cookie + owner/maintainer | `{name, scopes: ["turns:submit", ...]}` | `{id, name, prefix, secret, scopes, created_at}` — `secret` is returned ONCE here, never again |
| `GET` | `/api/workspaces/{wid}/api-keys` | cookie + member | — | `[{id, name, prefix, scopes, created_at, last_used_at, revoked_at}]` |
| `DELETE` | `/api/workspaces/{wid}/api-keys/{id}` | cookie + owner/maintainer | — | `204` (sets `revoked_at = NOW()`) |
| `GET` | `/api/workspaces/{wid}/api-keys/scopes` | cookie + member | — | `[{name, description, available}]` — scope catalog for the mint UI |

Internal RPC (NOT documented in the public spec):

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| `POST` | `/internal/workspace-api-keys/validate` | `X-Internal-Secret` | `{secret: "wak_..."}` | `200 {workspace_id, key_id, scopes: [...]}` or `401` |

### codex-app-gateway (existing endpoint, now publicized)

In new file `docs/api/codex-app-gateway.openapi.yaml`:

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| `POST` | `/api/turns` | `Authorization: Bearer wak_<...>` OR `X-Internal-Secret` | `{workspaceId, threadId?, params, timeoutMs?}` | `{threadId, turn? \| transport?}` (existing shape, no wire change) |

When `Authorization: Bearer wak_<...>` is presented:
- Handler asserts `body.workspaceId == key.workspace_id` → 403 on mismatch.
- Handler asserts `"turns:submit" ∈ key.scopes` → 403 on missing scope.

When `X-Internal-Secret` is presented: handler trusts the body's `workspaceId` (existing behavior, used by imbridge in-cluster). Scope check is bypassed.

## OpenAPI spec organization

Following Phase 1.a/1.b conventions:

```
docs/api/
├── openapi.yaml                     # existing — agentserver
├── openapi.json                     # existing
├── codex-app-gateway.openapi.yaml   # NEW — codex-app-gateway
├── codex-app-gateway.openapi.json   # NEW
└── README.md                        # update with the second spec
```

**Separate file rationale:** different binary, different host, different deployment lifecycle, different on-call ownership. Single spec via `servers:` arrays would tangle agentserver-team and codex-team changes.

**Toolchain duplication:** new Makefile targets `openapi-codex-app-gateway` and `openapi-codex-app-gateway-check`. Adds ~30 lines to Makefile and one new step to CI. Adopts the same `swag init → swagger2openapi → committed` flow.

**Naming conventions inherited from Phase 1:**

- `validate:"required"` on non-pointer fields that the handler always populates
- `extensions:"x-nullable=true"` on `*T` fields that should appear as `null` in the wire format
- `// @name <BareName>` on the closing brace to strip the `server.` prefix
- swag annotation block immediately above each handler, TAB-indented after `//`

## Auth scheme on the spec side

`docs/api/codex-app-gateway.openapi.yaml` declares two security schemes:

```yaml
components:
  securitySchemes:
    WorkspaceAPIKey:
      type: http
      scheme: bearer
      bearerFormat: wak_<prefix>_<secret>
      description: Workspace-scoped API key. Mint via POST /api/workspaces/{wid}/api-keys on agentserver.
    InternalSecret:
      type: apiKey
      in: header
      name: X-Internal-Secret
      description: Pre-shared secret for in-cluster RPC. Not for public consumers.
```

The `/api/turns` operation declares both via OpenAPI's `security: [...]` array (OR semantics — either passes auth).

## Frontend (SPA) changes

New `web/src/components/WorkspaceDetail.tsx` tab "API Keys":

- Table: name, prefix (e.g. `wak_a1b2c3d4…`), scopes (badges), created_at, last_used_at, revoked status
- "Create new key" button → modal:
  - Name input (required)
  - **Scope checkbox grid** — populated from `GET /api/workspaces/{wid}/api-keys/scopes`. Available scopes are checkable (default-checked for v1's single `turns:submit`); unavailable scopes are greyed with a "coming soon" hint
  - At least one scope must be selected → submit button disabled otherwise
  - Submit → mints → shows **full secret** with copy button + warning "you won't see this again" + scope summary so user verifies what they granted
  - After dismiss, secret hidden forever
- "Revoke" button per row → confirmation → DELETE

Generated TypeScript types via existing `openapi-typescript` pipeline (no toolchain changes). Scope catalog endpoint adds one more schema (`APIKeyScopeDescriptor`) but uses the same codegen path.

## Migration & back-compat

- `X-Internal-Secret` middleware on `/api/turns` stays as-is. imbridge in-cluster callers continue to work without modification.
- New `requireBearerAPIKey` middleware is **additive** — it routes to the same handler.
- Existing API-key-less callers see no behavior change.
- Database migration is purely additive (one new table + one index).
- Rollback safe: dropping the table is safe as long as no API keys have been minted; once minted, the user must re-issue keys after rollback.

## Decisions log

| Decision | Options considered | Chose | Why |
|---|---|---|---|
| Auth scheme | Cookie / Codex session bearer / NEW workspace API key | NEW workspace API key | Mature pattern for external integrations; isolates blast radius per key; survives user session expiry |
| Key wire format | UUID / opaque random / prefixed (`wak_…`) | `wak_<prefix>_<secret>` | Prefix enables O(1) DB lookup; `wak_` prefix is easy to grep in code/logs and distinguish from session tokens |
| Hash function | bcrypt / scrypt / SHA-256 | SHA-256 | Secret is CSPRNG (~200 bits); bcrypt's KDF overhead adds nothing against random secrets |
| Spec file | one spec with `servers:` / separate file per service | separate file | Lifecycle isolation; agentserver and codex-app-gateway evolve independently |
| Workspace scoping | per-workspace / per-user / cross-workspace | per-workspace | Simplest model that contains blast radius; the key authorizes ONE workspace only |
| Scope model | none / coarse action-based / resource-pattern (AWS IAM-lite) / role-based aliases | coarse action-based (`turns:submit`) | Matches swag's per-handler operationId 1:1; ships with one scope today, extensible without DB schema change; resource-pattern/aliases deferred until justified by real demand |
| Scope catalog storage | DB table / in-code constant map | in-code constant map | Catalog evolution is a code change anyway; DB row would just duplicate the truth and create migration drift |
| Validation path | codex-app-gateway has DB access / forwards to agentserver | forward to agentserver | Single source of truth; codex-app-gateway already uses agentserver for codex-auth/codex-tokens validation |
| Mint-once display | secret cached in DB / shown once via API response | shown once | Industry standard (GitHub, Stripe, etc.); avoids "leaked DB → leaked all secrets" |

## Risks & mitigations

- **Validate latency adds to `/api/turns` p99**: each call adds one in-cluster HTTP roundtrip to agentserver. *Mitigation*: agentserver-side caching with 60s TTL (key → workspace_id), in-process LRU. Adds ~50 lines.
- **`X-Internal-Secret` accidentally exposed to public clients**: header travels over public TLS but currently is only used in-cluster. *Mitigation*: keep gateway middleware order — secret check fires first, returns immediately on match, never validates bearer if secret matched. So API-key-bearing clients can't fish for an internal-secret response shape.
- **API key leak via copy-paste**: secret only shown once is the standard mitigation. *Additional*: include `last_used_at` so a maintainer can spot unexpected usage.
- **`/api/turns` is a write endpoint to user-bound LLM provider**: a leaked key burns LLM budget. *Mitigation*: existing workspace-level LLM quota applies (`handleGetWorkspaceLLMQuota`).
- **Frontend codegen pulls in codex-app-gateway types and accidentally creates a cross-binary type leak**: only agentserver's spec feeds the frontend codegen. codex-app-gateway spec is for external integrators / docs only — frontend does NOT consume it.

## Out of scope (future phases)

- Resource-pattern scopes (`turns:*`, `/api/threads/{id}:read`)
- Role-based scope aliases (`viewer` / `editor` / `admin`)
- Scope derivation from a user's workspace role at mint time
- Per-key rate limiting independent of workspace quota
- API key rotation tooling (mint-with-grace-period)
- Webhook signatures using the API key
- OAuth-style ephemeral tokens minted from API keys
- SDK clients (Python, JS) generated from the spec
