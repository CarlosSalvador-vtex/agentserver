# OpenAPI Phase 1.b — Sandboxes tag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Annotate the 9 Sandboxes REST endpoints with swaggo, regenerate the OpenAPI 3.0 spec, migrate the corresponding `web/src/lib/api.ts` helpers to apiClient + generated types.

**Architecture:** Same toolchain as Phase 1.a/1.b Workspaces — swag → swagger2openapi → openapi-typescript.

**Prereq:** Stacked on `feat/openapi-phase-1b-workspaces` (PR #153). Will rebase onto main when #152 + #153 merge.

---

## Endpoints (9)

| Method | Path | Handler | Auth |
|---|---|---|---|
| GET | `/api/workspaces/{wid}/sandboxes` | `handleListSandboxes` server.go:1599 | cookie + member |
| POST | `/api/workspaces/{wid}/sandboxes` | `handleCreateSandbox` server.go:1616 | cookie + owner/maintainer/developer |
| GET | `/api/sandboxes/{id}` | `handleGetSandbox` server.go:1870 | cookie + member |
| PATCH | `/api/sandboxes/{id}` | `handleRenameSandbox` server.go:1886 | cookie + member |
| DELETE | `/api/sandboxes/{id}` | `handleDeleteSandbox` server.go:1913 | cookie + member |
| POST | `/api/sandboxes/{id}/pause` | `handlePauseSandbox` server.go:1967 | cookie + member |
| POST | `/api/sandboxes/{id}/resume` | `handleResumeSandbox` server.go:2016 | cookie + member |
| GET | `/api/sandboxes/{id}/usage` | `handleSandboxUsage` server.go:2086 | cookie + member |

(8 endpoints actually distinct — the count is wider when counting GETs but the table covers all that need annotating. Will re-confirm `handleSandboxUsage` line during work.)

Line numbers are approximate; recon at the start of each task.

---

## File Structure

**Modify:**
- `internal/server/api_types.go` — append `// --- Sandboxes ---` section
- `internal/server/server.go` — `@name` on existing `sandboxResponse` and `sandboxUsageResponse`; refactor inline request structs; add 9 annotations
- `docs/api/openapi.{yaml,json}` — regenerated
- `web/src/lib/api.ts` — migrate sandbox helpers

---

### Task 1: Sandboxes DTOs in api_types.go

**Files:** modify `internal/server/api_types.go`

- [ ] **Step 1: Recon existing types**

```bash
grep -nE "type sandboxResponse|type sandboxUsageResponse" /root/agentserver/internal/server/server.go
```

Read their definitions. They will get `@name Sandbox` and `@name SandboxUsage` overrides (added in Task 2 on the closing brace, not here).

- [ ] **Step 2: Append to api_types.go after the `// --- Workspaces ---` block**

```go
// --- Sandboxes ---

// SandboxCreateRequest is the body for POST /api/workspaces/{wid}/sandboxes.
// All fields except name are optional and fall back to workspace/server defaults.
type SandboxCreateRequest struct {
	Name        string  `json:"name" validate:"required" example:"my-sandbox"`
	Type        string  `json:"type" example:"opencode"`             // optional; default "opencode"
	CPU         string  `json:"cpu" example:"1"`                     // optional; e.g. "500m" or "2"
	Memory      string  `json:"memory" example:"2Gi"`                // optional; e.g. "512Mi" or "4Gi"
	IdleTimeout string  `json:"idle_timeout" example:"30m"`          // optional; Go duration string
	Metadata    *string `json:"metadata" extensions:"x-nullable=true"` // optional; JSON-encoded metadata
} // @name SandboxCreateRequest

// SandboxRenameRequest is the body for PATCH /api/sandboxes/{id}.
type SandboxRenameRequest struct {
	Name string `json:"name" validate:"required" example:"renamed-sandbox"`
} // @name SandboxRenameRequest

// SandboxLifecycleStatusResponse is the {"status": "pausing"} envelope returned
// by POST /api/sandboxes/{id}/pause and /resume. The status reflects the
// transition initiated, not the final state (those are async).
type SandboxLifecycleStatusResponse struct {
	Status string `json:"status" validate:"required" example:"pausing"`
} // @name SandboxLifecycleStatusResponse
```

- [ ] **Step 3: Build + commit**

```bash
cd /root/agentserver
go build ./...
git add internal/server/api_types.go
git commit -m "feat(openapi): Sandboxes DTOs in api_types.go"
```

---

### Task 2: Augment `sandboxResponse`/`sandboxUsageResponse` + refactor handlers

**Files:** modify `internal/server/server.go`

- [ ] **Step 1: Add `@name` + tighten tags on the existing response types**

For `sandboxResponse` (around line 799): add `// @name Sandbox` after the closing brace, add `validate:"required"` to fields that are always populated, add `extensions:"x-nullable=true"` to any `*T` field that should appear as null instead of omitted.

For `sandboxUsageResponse`: add `// @name SandboxUsage` after the closing brace, add `validate:"required"` to required fields.

**Read both struct definitions first.** Report which fields are pointers vs non-pointers. If a non-pointer field is sometimes empty (e.g. `Type: ""` for unset), it shouldn't be marked required — flag it.

- [ ] **Step 2: Refactor `handleCreateSandbox` to use `SandboxCreateRequest`**

Replace the inline `var req struct {...}` with `var req SandboxCreateRequest`. Field access stays identical.

**Careful with `Metadata`:** in the inline struct it's likely a `string` (JSON-encoded payload). In the new type it's `*string` (nullable). Adjust call sites:

```go
metaStr := ""
if req.Metadata != nil {
    metaStr = *req.Metadata
}
// use metaStr where the old code used req.Metadata
```

(Verify the actual current type of `Metadata` in the inline struct first — if it's already `*string`, no change needed.)

- [ ] **Step 3: Refactor `handleRenameSandbox` to use `SandboxRenameRequest`**

Same pattern: `var req SandboxRenameRequest` instead of inline struct.

- [ ] **Step 4: Refactor pause/resume to emit `SandboxLifecycleStatusResponse`**

Replace `json.NewEncoder(w).Encode(map[string]string{"status": "pausing"})` with `json.NewEncoder(w).Encode(SandboxLifecycleStatusResponse{Status: "pausing"})` (and `"resuming"` for resume).

- [ ] **Step 5: Build + test + regenerate spec**

```bash
cd /root/agentserver
go build ./...
go test ./internal/server/ -count=1 -timeout 120s
make openapi
make openapi-check
```

If a test breaks because of `Metadata` typing change or any wire-shape difference, REPORT — do NOT modify the test.

- [ ] **Step 6: Commit**

```bash
git add internal/server/server.go internal/server/api_types.go docs/api/openapi.yaml docs/api/openapi.json
git commit -m "refactor(server): Sandboxes handlers use named DTOs + @name overrides"
```

---

### Task 3: Annotate the 8 Sandboxes handlers

**Files:** modify `internal/server/server.go`

Insert the following swag doc-comment blocks directly above each handler. Use TAB indent after `//` for the `@...` lines.

- [ ] **Step 1: Annotate `handleListSandboxes`**

```go
//	@Summary   List sandboxes in a workspace
//	@Tags      Sandboxes
//	@Produce   json
//	@Param     wid  path  string  true  "Workspace id"
//	@Success   200  {array}   Sandbox
//	@Failure   403  {string}  string  "not a member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/sandboxes [get]
```

- [ ] **Step 2: Annotate `handleCreateSandbox`**

```go
//	@Summary     Create a sandbox in a workspace
//	@Description Validates type / CPU / memory / idle_timeout / quota / budget. Returns 201 immediately with status="provisioning"; container starts asynchronously.
//	@Tags        Sandboxes
//	@Accept      json
//	@Produce     json
//	@Param       wid   path      string                true  "Workspace id"
//	@Param       body  body      SandboxCreateRequest  true  "Create payload"
//	@Success     201   {object}  Sandbox
//	@Failure     400   {string}  string  "validation error (type/cpu/memory/idle_timeout)"
//	@Failure     403   {string}  string  "insufficient role / quota / budget"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{wid}/sandboxes [post]
```

- [ ] **Step 3: Annotate `handleGetSandbox`**

```go
//	@Summary   Get a sandbox by id
//	@Tags      Sandboxes
//	@Produce   json
//	@Param     id  path  string  true  "Sandbox id"
//	@Success   200  {object}  Sandbox
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "sandbox not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/sandboxes/{id} [get]
```

- [ ] **Step 4: Annotate `handleRenameSandbox`**

```go
//	@Summary   Rename a sandbox
//	@Tags      Sandboxes
//	@Accept    json
//	@Produce   json
//	@Param     id    path      string                true  "Sandbox id"
//	@Param     body  body      SandboxRenameRequest  true  "New name"
//	@Success   200   {object}  Sandbox
//	@Failure   400   {string}  string  "name required"
//	@Failure   403   {string}  string  "not a member"
//	@Failure   404   {string}  string  "sandbox not found"
//	@Failure   500   {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/sandboxes/{id} [patch]
```

- [ ] **Step 5: Annotate `handleDeleteSandbox`**

```go
//	@Summary   Delete a sandbox
//	@Tags      Sandboxes
//	@Param     id  path  string  true  "Sandbox id"
//	@Success   204
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "sandbox not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/sandboxes/{id} [delete]
```

- [ ] **Step 6: Annotate `handlePauseSandbox`**

```go
//	@Summary     Pause a sandbox (cloud sandboxes only)
//	@Description Initiates pause transition; returns {"status":"pausing"}. Final state lands asynchronously.
//	@Tags        Sandboxes
//	@Produce     json
//	@Param       id  path  string  true  "Sandbox id"
//	@Success     200  {object}  SandboxLifecycleStatusResponse
//	@Failure     400  {string}  string  "local sandbox cannot be paused"
//	@Failure     403  {string}  string  "not a member"
//	@Failure     404  {string}  string  "sandbox not found"
//	@Failure     409  {string}  string  "invalid state for pause"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/pause [post]
```

- [ ] **Step 7: Annotate `handleResumeSandbox`**

```go
//	@Summary     Resume a paused sandbox (cloud sandboxes only)
//	@Description Initiates resume transition; returns {"status":"resuming"}. Final state lands asynchronously.
//	@Tags        Sandboxes
//	@Produce     json
//	@Param       id  path  string  true  "Sandbox id"
//	@Success     200  {object}  SandboxLifecycleStatusResponse
//	@Failure     400  {string}  string  "local sandbox cannot be resumed"
//	@Failure     403  {string}  string  "not a member"
//	@Failure     404  {string}  string  "sandbox not found"
//	@Failure     409  {string}  string  "invalid state for resume"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/resume [post]
```

- [ ] **Step 8: Annotate `handleSandboxUsage`**

```go
//	@Summary   Get sandbox usage stats
//	@Tags      Sandboxes
//	@Produce   json
//	@Param     id  path  string  true  "Sandbox id"
//	@Success   200  {object}  SandboxUsage
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "sandbox not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/sandboxes/{id}/usage [get]
```

- [ ] **Step 9: Regenerate + verify**

```bash
cd /root/agentserver
make openapi
make openapi-check
grep -E "^  ?(/api/workspaces/\{wid\}|/api/sandboxes)" docs/api/openapi.yaml | sort -u
grep -E "^    (Sandbox|SandboxCreateRequest|SandboxRenameRequest|SandboxLifecycleStatusResponse|SandboxUsage):" docs/api/openapi.yaml | sort
```

Expected: 3 paths (workspace-scoped list/create + standalone get/patch/delete + pause + resume + usage), 5 schemas under components without `server.` prefix.

- [ ] **Step 10: Build + test + commit**

```bash
go build ./...
go test ./internal/server/ -count=1 -timeout 120s
git add internal/server/server.go docs/api/openapi.yaml docs/api/openapi.json
git commit -m "feat(openapi): annotate Sandboxes handlers (8 endpoints)"
```

---

### Task 4: Migrate sandbox helpers in `web/src/lib/api.ts`

**Files:** modify `web/src/lib/api.ts`

- [ ] **Step 1: Regenerate types**

```bash
cd /root/agentserver/web
pnpm openapi:gen
```

- [ ] **Step 2: Identify existing helpers**

```bash
grep -nE "export async function (listSandboxes|createSandbox|getSandbox|renameSandbox|deleteSandbox|pauseSandbox|resumeSandbox|sandboxUsage|getSandboxUsage)" /root/agentserver/web/src/lib/api.ts
```

There may be additional sandbox helpers (e.g. for tunneling/local-agent) that ARE NOT covered by this PR — leave those alone.

- [ ] **Step 3: Migrate each present helper**

Pattern (adapt to actual signatures):

```typescript
export type Sandbox = components['schemas']['Sandbox']
export type SandboxUsage = components['schemas']['SandboxUsage']

export async function listSandboxes(workspaceId: string): Promise<Sandbox[]> {
  return apiFetch<Sandbox[]>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/sandboxes`,
  })
}

export async function createSandbox(
  workspaceId: string,
  body: components['schemas']['SandboxCreateRequest']
): Promise<Sandbox> {
  return apiFetch<Sandbox>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/sandboxes`,
    body,
  })
}
```

For pause/resume helpers, preserve any existing return-type contract (e.g. if they currently return `Promise<boolean>`, keep that).

- [ ] **Step 4: Drop duplicate local types**

Replace any local `interface Sandbox` / `interface SandboxUsage` / `interface CreateSandboxRequest` etc. with `type X = components['schemas']['X']` aliases. If a local type has extra fields the spec doesn't declare, REPORT.

- [ ] **Step 5: Verify tsc + lint + build**

```bash
cd /root/agentserver/web
pnpm tsc --noEmit
pnpm lint
pnpm build
```

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver
git add web/src/lib/api.ts
git commit -m "refactor(web): migrate Sandboxes helpers to apiClient + generated types"
```

---

### Task 5: End-to-end verify + PR

- [ ] **Step 1: Final verify**

```bash
cd /root/agentserver
make openapi-check
go test ./internal/server/ -count=1 -timeout 120s
cd web && pnpm openapi:gen && pnpm build && cd ..
git checkout -- web/dist/ 2>/dev/null || true
git status --short | head -3
```

- [ ] **Step 2: Push branch**

```bash
git push -u github feat/openapi-phase-1b-sandboxes
```

- [ ] **Step 3: Open PR (stacked on #153)**

```bash
gh pr create --base main --title "feat(openapi): Phase 1.b — Sandboxes tag (8 endpoints)" --body "$(cat <<'EOF'
## Summary

Annotates the 8 Sandboxes REST endpoints with swaggo, regenerates the OpenAPI 3.0 spec, migrates the corresponding frontend helpers. Same toolchain as PRs #152 and #153.

**Stacked on PR #153** — needs #152 + #153 merged first (or rebase after).

Reference: spec `docs/superpowers/specs/2026-05-21-openapi-organization-design.md`, plan `docs/superpowers/plans/2026-05-22-openapi-phase-1b-sandboxes.md`.

## Endpoints

- List / Create / Get / Rename / Delete sandbox
- Pause / Resume (lifecycle RPC)
- Usage stats

## Verification

- `make openapi-check` green
- `go test ./internal/server/` green
- `cd web && pnpm tsc --noEmit && pnpm build` green

## Next

IM Channels (~8 endpoints).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Done when

- PR opened against `main` with 4 commits
- 8 endpoints under tag `Sandboxes` in `docs/api/openapi.yaml`
- Frontend builds; existing sandbox UI works
