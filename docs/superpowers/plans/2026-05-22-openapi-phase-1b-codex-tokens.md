# OpenAPI Phase 1.b — Codex Tokens tag Implementation Plan

**Goal:** Annotate 3 Codex Tokens REST endpoints at `/api/codex/tokens` (and
`/api/codex/tokens/{id}`). The handlers are fully implemented in
`internal/server/codex_tokens.go`; they already use named package-level structs
(`mintCodexTokenReq`, etc.) but those structs are unexported and in the handler
file, so they must be promoted to `api_types.go`.

**Prereq:** Stacked on `feat/openapi-phase-1b-im-channels` (PR #155).

**Reference spec:** `docs/superpowers/specs/` — no dedicated openapi-org spec
found on the working tree; follow the same conventions as PR #152 (Auth),
#153 (Workspaces), #154 (Sandboxes), #155 (IM Channels).

---

## Endpoints (3)

| Method | Path | Handler | Auth | Notes |
|---|---|---|---|---|
| POST | `/api/codex/tokens` | `handleMintCodexToken` | cookie + workspace member (non-guest) | 201 Created, returns full token once |
| GET | `/api/codex/tokens` | `handleListCodexTokens` | cookie + workspace member | `?workspace_id=` + `?include_revoked=true` query params; never returns raw `token` field |
| DELETE | `/api/codex/tokens/{id}` | `handleRevokeCodexToken` | cookie + token owner or workspace admin | 204 No Content, idempotent |

All three endpoints are registered in the main chi router at lines 449-451 of `internal/server/server.go`, guarded by the logged-in middleware.

---

## File Structure

**Modify:**
- `internal/server/api_types.go` — append `// --- Codex Tokens ---` section with 3 named types
- `internal/server/codex_tokens.go` — replace inline `mintCodexTokenReq`, `mintCodexTokenResp`, `listCodexTokenItem` with exported DTOs; add swag annotation blocks on each handler
- `docs/api/openapi.yaml` + `docs/api/openapi.json` — regenerated
- `web/src/lib/api.ts` — migrate 3 helpers + drop 3 local interfaces

**No new files needed** (direct annotation pattern, not wrapper pattern — handlers are self-contained, not proxied).

---

## Task Breakdown

### Task 1: DTOs — `internal/server/api_types.go`

Append after the `// --- IM Channels ---` block:

```
// --- Codex Tokens ---

// CodexTokenMintRequest is the body for POST /api/codex/tokens.
type CodexTokenMintRequest struct {
    WorkspaceID string `json:"workspace_id" validate:"required"`
    Name        string `json:"name" validate:"required" example:"my mac"`
    TTLDays     int    `json:"ttl_days,omitempty" example:"90"`
} // @name CodexTokenMintRequest

// CodexTokenMintResponse is returned (201) by POST /api/codex/tokens.
// token is the full bearer value; it is shown only once.
type CodexTokenMintResponse struct {
    ID          string    `json:"id" validate:"required"`
    Token       string    `json:"token" validate:"required"`
    Name        string    `json:"name" validate:"required"`
    WorkspaceID string    `json:"workspace_id" validate:"required"`
    ExpiresAt   time.Time `json:"expires_at" validate:"required"`
    CreatedAt   time.Time `json:"created_at" validate:"required"`
} // @name CodexTokenMintResponse

// CodexTokenListItem is one entry in the GET /api/codex/tokens response.
// last_used_at and revoked_at are null when not set.
type CodexTokenListItem struct {
    ID          string     `json:"id" validate:"required"`
    Name        string     `json:"name" validate:"required"`
    WorkspaceID string     `json:"workspace_id" validate:"required"`
    CreatedAt   time.Time  `json:"created_at" validate:"required"`
    ExpiresAt   time.Time  `json:"expires_at" validate:"required"`
    LastUsedAt  *time.Time `json:"last_used_at,omitempty" extensions:"x-nullable=true"`
    Revoked     bool       `json:"revoked"`
    RevokedAt   *time.Time `json:"revoked_at,omitempty" extensions:"x-nullable=true"`
} // @name CodexTokenListItem
```

Note: `api_types.go` does not currently import `time`; will need to add the import.

Commit: `feat(openapi): Codex Tokens DTOs in api_types.go`

---

### Task 2: Handler refactor + swag annotations

In `internal/server/codex_tokens.go`:
1. Replace local unexported structs with the exported DTOs from api_types.go
2. Add swag annotation blocks to each of the 3 handlers
3. Run `make openapi`, verify no `server.` prefixes in the spec
4. Run `make openapi-check`

Annotations:

**POST /api/codex/tokens** → `handleMintCodexToken`
- `@Tags Codex Tokens`
- `@Param body body CodexTokenMintRequest true`
- `@Success 201 {object} CodexTokenMintResponse`
- `@Failure 400, 403, 422, 500`

**GET /api/codex/tokens** → `handleListCodexTokens`
- `@Tags Codex Tokens`
- `@Param workspace_id query string true`
- `@Param include_revoked query bool false`
- `@Success 200 {array} CodexTokenListItem`
- `@Failure 400, 403, 500`

**DELETE /api/codex/tokens/{id}** → `handleRevokeCodexToken`
- `@Tags Codex Tokens`
- `@Param id path string true`
- `@Success 204`
- `@Failure 403, 500`

Commit: `feat(openapi): annotate Codex Tokens handlers (3 endpoints)`

---

### Task 3: Frontend migration

1. `cd web && pnpm openapi:gen`
2. In `web/src/lib/api.ts`:
   - Add type aliases:
     ```typescript
     export type CodexToken = components['schemas']['CodexTokenListItem']
     export type CodexTokenMintRequest = components['schemas']['CodexTokenMintRequest']
     export type CodexTokenMintResponse = components['schemas']['CodexTokenMintResponse']
     ```
   - Remove the 3 hand-written `export interface CodexToken / MintCodexTokenRequest / MintCodexTokenResponse`
   - Migrate `listCodexTokens` to `apiFetch`
   - Migrate `mintCodexToken` to `apiFetch`
   - Migrate `revokeCodexToken` to `apiFetch` (DELETE, 204, return void)
3. `pnpm tsc --noEmit && pnpm build`
4. `git checkout -- web/dist/`

Commit: `refactor(web): migrate Codex Tokens helpers to apiClient + generated types`

---

### Task 4: Verify + PR

```bash
make openapi-check
go test ./internal/server/ -count=1 -timeout 120s
cd web && pnpm openapi:gen && pnpm build && cd ..
git checkout -- web/dist/ 2>/dev/null || true
git push -u github feat/openapi-phase-1b-codex-tokens
gh pr create --base feat/openapi-phase-1b-im-channels \
  --title "feat(openapi): Phase 1.b — Codex Tokens tag (3 endpoints)" \
  --body "..."
```

---

## Done when

- 3 endpoints under tag `Codex Tokens` in `docs/api/openapi.yaml`
- `make openapi-check` passes
- `go test ./internal/server/` passes (existing tests must still pass — they test exact HTTP shapes, so DTO promotion must be wire-compatible)
- Frontend builds; existing Codex UI works
- PR open against `feat/openapi-phase-1b-im-channels`
