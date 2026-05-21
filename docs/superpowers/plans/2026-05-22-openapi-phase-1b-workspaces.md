# OpenAPI Phase 1.b — Workspaces tag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Annotate the 14 Workspaces REST endpoints with swaggo, regenerate the OpenAPI 3.0 spec, and migrate the corresponding helpers in `web/src/lib/api.ts` to use the apiClient + generated types.

**Architecture:** Same pipeline as Phase 1.a — swag → swagger2openapi → openapi-typescript → typed helpers. This PR exercises the toolchain on a larger surface (14 endpoints, 3 sub-areas: workspace CRUD, members, LLM config) and is the template for the remaining 6 tag PRs.

**Tech Stack:** Reuses everything from Phase 1.a; no new deps.

**Prereq:** Phase 1.a (PR #152) must be merged first OR this PR is stacked on `feat/openapi-phase-1a-infra-auth` (will need rebase after merge).

---

## Endpoint surface (14)

| Method | Path | Handler | Auth |
|---|---|---|---|
| GET | `/api/workspaces` | `handleListWorkspaces` server.go:1008 | cookie |
| POST | `/api/workspaces` | `handleCreateWorkspace` server.go:1024 | cookie |
| GET | `/api/workspaces/quota` | `handleGetWorkspacesQuota` server.go:990 | cookie |
| GET | `/api/workspaces/{id}` | `handleGetWorkspace` server.go:1099 | cookie + member |
| PATCH | `/api/workspaces/{id}` | `handleRenameWorkspace` server.go:1115 | cookie + owner/maintainer |
| DELETE | `/api/workspaces/{id}` | `handleDeleteWorkspace` server.go:1141 | cookie + owner |
| GET | `/api/workspaces/{id}/members` | `handleListMembers` server.go:1204 | cookie + member |
| POST | `/api/workspaces/{id}/members` | `handleAddMember` server.go:1238 | cookie + owner/maintainer |
| PUT | `/api/workspaces/{id}/members/{userId}` | `handleUpdateMemberRole` server.go:1278 | cookie + owner |
| DELETE | `/api/workspaces/{id}/members/{userId}` | `handleRemoveMember` server.go:1302 | cookie + owner |
| GET | `/api/workspaces/{id}/llm-quota` | `handleGetWorkspaceLLMQuota` server.go:1319 | cookie + any role |
| GET | `/api/workspaces/{id}/llm-config` | `handleGetWorkspaceLLMConfig` server.go:1336 | cookie + owner/maintainer |
| PUT | `/api/workspaces/{id}/llm-config` | `handleSetWorkspaceLLMConfig` server.go:1362 | cookie + owner/maintainer |
| DELETE | `/api/workspaces/{id}/llm-config` | `handleDeleteWorkspaceLLMConfig` server.go:1418 | cookie + owner/maintainer |

Line numbers are approximate (verify before editing — they will shift as we add doc comments).

---

## File Structure

**Create:** none (api_types.go already exists from Phase 1.a)

**Modify:**
- `internal/server/api_types.go` — append Workspaces section (new named types for request/response shapes that were inline anonymous structs / inline maps)
- `internal/server/server.go` — add swaggo doc-comment blocks above the 14 handlers; rewrite handlers that use inline anonymous structs to use the new named types
- `docs/api/openapi.yaml` + `docs/api/openapi.json` — regenerated (drift gate enforces)
- `web/src/lib/api.ts` — migrate workspace-related helpers to use `apiFetch` + generated types

**Out of scope (other Phase 1.b PRs):**
- Sandboxes (`/api/workspaces/{id}/sandboxes` etc.)
- IM channels, codex tokens, codex browser sessions, agent endpoints, misc

---

### Task 1: Extend api_types.go with Workspaces DTOs

**Files:**
- Modify: `internal/server/api_types.go`

Group the new types under a `// --- Workspaces ---` section, alphabetical within the group.

- [ ] **Step 1: Confirm the existing `workspaceResponse` and `workspaceMemberResponse` types**

```bash
grep -n "type workspaceResponse\|type workspaceMemberResponse" /root/agentserver/internal/server/*.go
```

These already exist as named types (Phase 1.a-friendly). They need `@name` overrides + `validate:"required"` tags to flow through swag cleanly. Read their current definitions and report back so the next step can produce the exact edits.

- [ ] **Step 2: Append DTOs to api_types.go**

After the existing `// --- Auth ---` block, append:

```go
// --- Workspaces ---

// WorkspaceRef is a workspace's primary fields, returned by list / get /
// create / patch endpoints.
//
// Note: in handlers this type currently lives as `workspaceResponse`
// (lowercase) — the @name override below exposes it as `Workspace` in
// the OpenAPI spec without forcing a Go rename.
//
//	@name Workspace
type WorkspaceRef struct {
	ID        string `json:"id" validate:"required"`
	Name      string `json:"name" validate:"required"`
	Workdir   string `json:"workdir" validate:"required"`
	CreatedAt string `json:"created_at" validate:"required"`
	UpdatedAt string `json:"updated_at" validate:"required"`
} // @name Workspace

// WorkspaceMember describes a single member of a workspace.
type WorkspaceMember struct {
	UserID  string  `json:"user_id" validate:"required"`
	Email   string  `json:"email" validate:"required"`
	Role    string  `json:"role" validate:"required" example:"developer"`
	Picture *string `json:"picture" extensions:"x-nullable=true"`
} // @name WorkspaceMember

// WorkspaceCreateRequest is the body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name string `json:"name" validate:"required" example:"My Workspace"`
} // @name WorkspaceCreateRequest

// WorkspaceRenameRequest is the body for PATCH /api/workspaces/{id}.
type WorkspaceRenameRequest struct {
	Name string `json:"name" validate:"required" example:"Renamed Workspace"`
} // @name WorkspaceRenameRequest

// WorkspaceQuotaResponse is the {"current": int, "max": int} envelope
// returned by GET /api/workspaces/quota.
type WorkspaceQuotaResponse struct {
	Current int `json:"current" validate:"required"`
	Max     int `json:"max" validate:"required"`
} // @name WorkspaceQuotaResponse

// MemberAddRequest is the body for POST /api/workspaces/{id}/members.
type MemberAddRequest struct {
	Email string `json:"email" validate:"required" example:"alice@example.com"`
	Role  string `json:"role" example:"developer"` // optional; defaults to "developer"
} // @name MemberAddRequest

// MemberRoleUpdateRequest is the body for PUT /api/workspaces/{id}/members/{userId}.
type MemberRoleUpdateRequest struct {
	Role string `json:"role" validate:"required" example:"maintainer"`
} // @name MemberRoleUpdateRequest

// LLMQuotaResponse mirrors the body the LLM proxy returns from its
// internal `/internal/quotas/{workspaceId}` endpoint. Fields not all
// guaranteed by the proxy — they're documented as observed.
type LLMQuotaResponse struct {
	WorkspaceID    string `json:"workspace_id" validate:"required"`
	DailyLimit     int    `json:"daily_limit" validate:"required"`
	DailyUsed      int    `json:"daily_used" validate:"required"`
	ResetsAt       string `json:"resets_at" validate:"required"`
} // @name LLMQuotaResponse

// LLMModel is one entry in a workspace's per-model LLM config.
type LLMModel struct {
	Name        string `json:"name" validate:"required" example:"claude-opus-4-7"`
	DisplayName string `json:"display_name" example:"Claude Opus 4.7"`
	MaxTokens   int    `json:"max_tokens" example:"200000"`
} // @name LLMModel

// LLMConfigResponse is the body returned by GET /api/workspaces/{id}/llm-config.
// `api_key` is masked (first 3 + "..." + last 4 chars) and is empty
// when no config exists.
type LLMConfigResponse struct {
	Configured bool       `json:"configured" validate:"required"`
	BaseURL    string     `json:"base_url"`
	APIKey     string     `json:"api_key"`
	Models     []LLMModel `json:"models"`
	UpdatedAt  *string    `json:"updated_at" extensions:"x-nullable=true"`
} // @name LLMConfigResponse

// LLMConfigUpsertRequest is the body for PUT /api/workspaces/{id}/llm-config.
// All three fields are required for a fresh config; for an update,
// omitting `api_key` retains the existing key.
type LLMConfigUpsertRequest struct {
	BaseURL string     `json:"base_url" validate:"required" example:"https://api.anthropic.com"`
	APIKey  string     `json:"api_key" example:"sk-ant-..."` // optional on update
	Models  []LLMModel `json:"models" validate:"required"`
} // @name LLMConfigUpsertRequest

// LLMConfigUpsertResponse is the body returned by the upsert endpoint.
type LLMConfigUpsertResponse struct {
	OK bool `json:"ok" validate:"required"`
} // @name LLMConfigUpsertResponse
```

- [ ] **Step 3: Verify build**

```bash
cd /root/agentserver
go build ./...
```

Should be silent.

- [ ] **Step 4: Commit**

```bash
git add internal/server/api_types.go
git commit -m "feat(openapi): Workspaces DTOs in api_types.go"
```

---

### Task 2: Refactor handlers to use the new named DTOs

**Files:**
- Modify: `internal/server/server.go` — Workspaces handlers + the inline `workspaceResponse` definition

- [ ] **Step 1: Find the inline `workspaceResponse` and `workspaceMemberResponse` definitions**

```bash
grep -n "type workspaceResponse struct\|type workspaceMemberResponse struct\|workspaceResponse{" /root/agentserver/internal/server/server.go
```

These typically live near the top of `server.go` or near where they're returned. Read them.

- [ ] **Step 2: Either delete the lowercase types and switch callers to `WorkspaceRef`/`WorkspaceMember`, OR add `@name` to the lowercase types**

Recommended: **keep the lowercase Go names** (`workspaceResponse`, `workspaceMemberResponse`) for backwards compatibility with the surrounding code, just add the `@name Workspace` / `@name WorkspaceMember` swag overrides on their closing brace, and DROP the `WorkspaceRef`/`WorkspaceMember` types from Task 1's api_types.go. Rationale: minimal touch to server.go internals. Only the JSON wire / swag schema name changes; Go callers are untouched.

Apply this: edit api_types.go, remove the duplicated `WorkspaceRef` and `WorkspaceMember` types. Then edit server.go to add `// @name Workspace` after the closing brace of `workspaceResponse` and `// @name WorkspaceMember` after `workspaceMemberResponse`. Also add `validate:"required"` to non-optional fields and `extensions:"x-nullable=true"` to `Picture`.

(If the existing lowercase types lack JSON tags or have inconvenient field shapes, fall back to introducing the uppercase types and migrating callers. Report which path you chose.)

- [ ] **Step 3: Refactor inline anonymous request structs in the 4 mutation endpoints**

Endpoints that use `var req struct {...}` inline:
- `handleCreateWorkspace` — replace with `var req WorkspaceCreateRequest`
- `handleRenameWorkspace` — replace with `var req WorkspaceRenameRequest`
- `handleAddMember` — replace with `var req MemberAddRequest`
- `handleUpdateMemberRole` — replace with `var req MemberRoleUpdateRequest`
- `handleSetWorkspaceLLMConfig` — replace with `var req LLMConfigUpsertRequest`

For each, the rewrite is mechanical: replace the inline struct + the `var req` line with a single `var req <NamedType>`. Field access stays identical.

- [ ] **Step 4: Refactor inline map responses to use named types**

Endpoints that build inline `map[string]interface{}` or `map[string]int` responses:
- `handleGetWorkspacesQuota` → return `WorkspaceQuotaResponse{Current: ..., Max: ...}`
- `handleGetWorkspaceLLMConfig` → return `LLMConfigResponse{Configured: ..., BaseURL: ..., APIKey: ..., Models: ..., UpdatedAt: ...}`
- `handleSetWorkspaceLLMConfig` → return `LLMConfigUpsertResponse{OK: true}`

- [ ] **Step 5: Build + test**

```bash
cd /root/agentserver
go build ./...
go test ./internal/server/ -count=1 -timeout 120s
```

If tests assert exact JSON shapes of these responses, verify they still match the new typed encoding (field order in struct = field order in JSON, so as long as field order in the new types matches the order the inline map was constructed, the wire format is identical).

If any test breaks, REPORT — do not modify the test without escalation.

- [ ] **Step 6: Commit**

```bash
git add internal/server/api_types.go internal/server/server.go
git commit -m "refactor(server): Workspaces handlers use named DTOs"
```

---

### Task 3: Add swag annotations to the 14 Workspaces handlers

**Files:**
- Modify: `internal/server/server.go`

For each handler, add a doc-comment block ABOVE the func line with `@Summary`, `@Tags Workspaces`, `@Param`, `@Success`, `@Failure`, `@Router`, and `@Security CookieAuth` (all 14 require auth).

- [ ] **Step 1: Annotate the 6 workspace-core handlers**

Examples (verify line numbers first with `grep -n "func (s \*Server) handle\(List\|Create\|Get\|Rename\|Delete\)Workspace\b\|handleGetWorkspacesQuota\b" internal/server/server.go`):

**Above `handleListWorkspaces`:**

```go
//	@Summary    List workspaces for the current user
//	@Tags       Workspaces
//	@Produce    json
//	@Success    200  {array}   Workspace
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces [get]
```

**Above `handleCreateWorkspace`:**

```go
//	@Summary    Create a new workspace
//	@Description Creator is auto-added as owner. May fail with 403 if the per-user workspace quota is exceeded.
//	@Tags       Workspaces
//	@Accept     json
//	@Produce    json
//	@Param      body  body      WorkspaceCreateRequest  true  "Workspace name"
//	@Success    201   {object}  Workspace
//	@Failure    400   {string}  string  "bad request / empty name"
//	@Failure    403   {string}  string  "workspace quota exceeded"
//	@Failure    500   {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces [post]
```

**Above `handleGetWorkspacesQuota`:**

```go
//	@Summary    Get per-user workspace quota
//	@Tags       Workspaces
//	@Produce    json
//	@Success    200  {object}  WorkspaceQuotaResponse
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/quota [get]
```

**Above `handleGetWorkspace`:**

```go
//	@Summary    Get a workspace by id
//	@Tags       Workspaces
//	@Produce    json
//	@Param      id  path  string  true  "Workspace id"
//	@Success    200  {object}  Workspace
//	@Failure    403  {string}  string  "not a member"
//	@Failure    404  {string}  string  "workspace not found"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id} [get]
```

**Above `handleRenameWorkspace`:**

```go
//	@Summary    Rename a workspace
//	@Tags       Workspaces
//	@Accept     json
//	@Produce    json
//	@Param      id    path      string                  true  "Workspace id"
//	@Param      body  body      WorkspaceRenameRequest  true  "New name"
//	@Success    200   {object}  Workspace
//	@Failure    400   {string}  string  "empty name"
//	@Failure    403   {string}  string  "owner or maintainer required"
//	@Failure    500   {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id} [patch]
```

**Above `handleDeleteWorkspace`:**

```go
//	@Summary    Delete a workspace (owner only; cascades to sandboxes + namespace)
//	@Tags       Workspaces
//	@Param      id   path  string  true  "Workspace id"
//	@Success    204
//	@Failure    403  {string}  string  "owner only"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id} [delete]
```

- [ ] **Step 2: Annotate the 4 members handlers**

**Above `handleListMembers`:**

```go
//	@Summary    List members of a workspace
//	@Tags       Workspaces
//	@Produce    json
//	@Param      id  path  string  true  "Workspace id"
//	@Success    200  {array}   WorkspaceMember
//	@Failure    403  {string}  string  "not a member"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/members [get]
```

**Above `handleAddMember`:**

```go
//	@Summary    Add a member to a workspace
//	@Description Looks up the user by email. Default role is "developer" if omitted.
//	@Tags       Workspaces
//	@Accept     json
//	@Produce    json
//	@Param      id    path      string            true  "Workspace id"
//	@Param      body  body      MemberAddRequest  true  "Email and optional role"
//	@Success    201   {object}  WorkspaceMember
//	@Failure    400   {string}  string  "bad request"
//	@Failure    403   {string}  string  "owner or maintainer required"
//	@Failure    404   {string}  string  "user not found"
//	@Failure    500   {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/members [post]
```

**Above `handleUpdateMemberRole`:**

```go
//	@Summary    Change a member's role (owner only)
//	@Tags       Workspaces
//	@Accept     json
//	@Param      id      path  string                   true  "Workspace id"
//	@Param      userId  path  string                   true  "User id"
//	@Param      body    body  MemberRoleUpdateRequest  true  "New role"
//	@Success    204
//	@Failure    400  {string}  string  "empty role"
//	@Failure    403  {string}  string  "owner only"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/members/{userId} [put]
```

**Above `handleRemoveMember`:**

```go
//	@Summary    Remove a member (owner only)
//	@Tags       Workspaces
//	@Param      id      path  string  true  "Workspace id"
//	@Param      userId  path  string  true  "User id"
//	@Success    204
//	@Failure    403  {string}  string  "owner only"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/members/{userId} [delete]
```

- [ ] **Step 3: Annotate the 4 LLM-config / LLM-quota handlers**

**Above `handleGetWorkspaceLLMQuota`:**

```go
//	@Summary    Get the workspace's daily LLM request quota usage
//	@Tags       Workspaces
//	@Produce    json
//	@Param      id  path  string  true  "Workspace id"
//	@Success    200  {object}  LLMQuotaResponse
//	@Failure    403  {string}  string  "insufficient role"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/llm-quota [get]
```

**Above `handleGetWorkspaceLLMConfig`:**

```go
//	@Summary    Get workspace LLM config (owner/maintainer)
//	@Description The returned api_key is masked (first 3 + "..." + last 4). updated_at is null when no config is set.
//	@Tags       Workspaces
//	@Produce    json
//	@Param      id  path  string  true  "Workspace id"
//	@Success    200  {object}  LLMConfigResponse
//	@Failure    403  {string}  string  "insufficient role"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/llm-config [get]
```

**Above `handleSetWorkspaceLLMConfig`:**

```go
//	@Summary    Upsert workspace LLM config (owner/maintainer)
//	@Description On update, omitting api_key retains the existing key.
//	@Tags       Workspaces
//	@Accept     json
//	@Produce    json
//	@Param      id    path      string                  true  "Workspace id"
//	@Param      body  body      LLMConfigUpsertRequest  true  "Config payload"
//	@Success    200   {object}  LLMConfigUpsertResponse
//	@Failure    400   {string}  string  "validation error (invalid URL / missing field / too many models)"
//	@Failure    403   {string}  string  "insufficient role"
//	@Failure    500   {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/llm-config [put]
```

**Above `handleDeleteWorkspaceLLMConfig`:**

```go
//	@Summary    Delete workspace LLM config (owner/maintainer)
//	@Tags       Workspaces
//	@Param      id  path  string  true  "Workspace id"
//	@Success    204
//	@Failure    403  {string}  string  "insufficient role"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{id}/llm-config [delete]
```

- [ ] **Step 4: Regenerate spec + verify**

```bash
cd /root/agentserver
make openapi
grep -E "^  /api/workspaces" docs/api/openapi.yaml | sort
make openapi-check
```

Expected: all 14 paths appear. Drift gate exits 0.

Spot-check that schema names appear unprefixed:

```bash
grep -E "^\\s*(Workspace|WorkspaceMember|WorkspaceCreateRequest|WorkspaceRenameRequest|WorkspaceQuotaResponse|MemberAddRequest|MemberRoleUpdateRequest|LLMQuotaResponse|LLMModel|LLMConfigResponse|LLMConfigUpsertRequest|LLMConfigUpsertResponse):" docs/api/openapi.yaml | sort
```

Expected: 12 schema names listed without `server.` prefix.

- [ ] **Step 5: Build + test**

```bash
go build ./...
go test ./internal/server/ -count=1 -timeout 120s
```

- [ ] **Step 6: Commit**

```bash
git add internal/server/server.go docs/api/openapi.yaml docs/api/openapi.json
git commit -m "feat(openapi): annotate Workspaces handlers (14 endpoints, 3 sub-areas)"
```

---

### Task 4: Migrate workspace helpers in `web/src/lib/api.ts`

**Files:**
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Regenerate frontend types**

```bash
cd /root/agentserver/web
pnpm openapi:gen
```

Expected: `web/src/lib/api-generated/schema.d.ts` updated with new schemas.

- [ ] **Step 2: Find the workspace-related helpers in api.ts**

```bash
grep -nE "export async function (listWorkspaces|createWorkspace|getWorkspace|renameWorkspace|deleteWorkspace|getWorkspacesQuota|listMembers|addMember|updateMemberRole|removeMember|getWorkspaceLLMQuota|getWorkspaceLLMConfig|setWorkspaceLLMConfig|deleteWorkspaceLLMConfig)" /root/agentserver/web/src/lib/api.ts
```

Identify the 14 (or fewer — some endpoints may not have frontend helpers yet) helpers. For each, rewrite the body to use `apiFetch` + `components['schemas']['<Name>']` from the generated schema. KEEP external signatures identical so all callers (`WorkspaceList.tsx`, `WorkspaceDetail.tsx`, `WorkspaceCreateModal.tsx`, …) work without changes.

Template per helper (adapt to actual signatures):

```typescript
export async function listWorkspaces(): Promise<Workspace[]> {
  return apiFetch<components['schemas']['Workspace'][]>({
    method: 'GET',
    path: '/api/workspaces',
  })
}

export async function createWorkspace(name: string): Promise<Workspace> {
  return apiFetch<components['schemas']['Workspace']>({
    method: 'POST',
    path: '/api/workspaces',
    body: { name } satisfies components['schemas']['WorkspaceCreateRequest'],
  })
}
```

For mutation endpoints that previously caught errors and returned booleans (mirroring the Auth helpers' `boolean` pattern), preserve that contract:

```typescript
export async function renameWorkspace(id: string, name: string): Promise<boolean> {
  try {
    await apiFetch<components['schemas']['Workspace']>({
      method: 'PATCH',
      path: `/api/workspaces/${encodeURIComponent(id)}`,
      body: { name } satisfies components['schemas']['WorkspaceRenameRequest'],
    })
    return true
  } catch (err) {
    if (err instanceof ApiError) return false
    throw err
  }
}
```

If you find a helper whose return shape doesn't cleanly map to a single component schema (e.g. it currently builds a composite from multiple API calls), keep the helper's existing internal logic but switch the underlying fetch call to apiFetch + the relevant schema type.

- [ ] **Step 3: Drop any local TypeScript types in api.ts that are now duplicated by `components['schemas']`**

For example, if api.ts has its own `interface Workspace { id: string; name: string; ... }`, delete it and replace usages with `type Workspace = components['schemas']['Workspace']`. This removes a divergence vector. Re-export the alias if callers import the name directly:

```typescript
export type Workspace = components['schemas']['Workspace']
export type WorkspaceMember = components['schemas']['WorkspaceMember']
// ... etc.
```

If a local type has extra fields the spec doesn't declare, REPORT — that's a hidden divergence we should resolve before completing the migration.

- [ ] **Step 4: Verify tsc + lint + build**

```bash
cd /root/agentserver/web
pnpm tsc --noEmit
pnpm lint
pnpm build
```

Expected: tsc clean, no new lint errors, build succeeds.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add web/src/lib/api.ts
git commit -m "refactor(web): migrate Workspaces helpers to apiClient + generated types"
```

---

### Task 5: End-to-end verification + PR

**Files:** none (verification only)

- [ ] **Step 1: Drift gate sanity**

```bash
cd /root/agentserver
make openapi-check
```

Expected: pass.

- [ ] **Step 2: Full Go test suite**

```bash
go test ./internal/server/ -count=1 -timeout 120s
```

Expected: pass.

- [ ] **Step 3: Full frontend build**

```bash
cd /root/agentserver/web
pnpm openapi:gen && pnpm build
```

Expected: pass; produces `dist/`.

- [ ] **Step 4: Push the branch**

```bash
cd /root/agentserver
git push -u github feat/openapi-phase-1b-workspaces
```

(Branch name assumes you branched off `feat/openapi-phase-1a-infra-auth` or `main`. If you stacked on 1.a and PR #152 has merged in the meantime, rebase first: `git rebase github/main`.)

- [ ] **Step 5: Open PR**

```bash
gh pr create --base main --title "feat(openapi): Phase 1.b — Workspaces tag (14 endpoints)" --body "$(cat <<'EOF'
## Summary

Phase 1.b kickoff: annotates the 14 Workspaces REST endpoints with swaggo, regenerates `docs/api/openapi.{yaml,json}`, and migrates the corresponding frontend helpers in `web/src/lib/api.ts` to apiClient + generated types. External signatures of those helpers unchanged.

Same shape as Phase 1.a (PR #152); this is the template for the remaining 6 tag PRs.

Reference: spec `docs/superpowers/specs/2026-05-21-openapi-organization-design.md`, plan `docs/superpowers/plans/2026-05-22-openapi-phase-1b-workspaces.md`.

## Endpoints

- Workspace CRUD: list / create / get / rename / delete / quota
- Members: list / add / update role / remove
- LLM config: quota / get / upsert / delete

## What changed

- `internal/server/api_types.go` — added Workspaces section (10+ new DTOs)
- `internal/server/server.go` — handlers refactored to use named DTOs; 14 swag annotation blocks added
- `docs/api/openapi.{yaml,json}` — regenerated
- `web/src/lib/api.ts` — workspace helpers migrated; old hand-written types replaced with `components['schemas']` aliases

## Verification

- `make openapi-check` green
- `go test ./internal/server/` green
- `cd web && pnpm tsc --noEmit && pnpm build` green

## Next

Sandboxes is the next tag in the queue (~8 endpoints).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 6: Verification done**

---

## Done when

- PR opened against `main` with 4 commits (DTOs, refactor, annotate, frontend migration)
- All 14 endpoints appear in `docs/api/openapi.yaml` under tag `Workspaces`
- Frontend builds and existing workspace UI works (visually verified once dev server reloads)
