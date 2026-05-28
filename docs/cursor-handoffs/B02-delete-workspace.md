# Cursor Handoff — B02: DELETE workspace

**Backlog:** B02 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~80
**Tempo:** 1-2h
**Dependências:** nenhuma

## Status

- **State:** OPEN — not started
- **Dependencies:** none
- **Estimated PR size:** M (~80 LOC)

## Goal

Endpoint `DELETE /api/workspaces/{id}` que limpa workspace + cascade dos recursos (membros, sandboxes, drafts). Soft delete por default.

## Why now

- Hoje cleanup é SQL manual no DB de prod — alto risco
- Workspaces de teste acumulam em DEV (`empresa-custom-teste`, `auto-derive-me`)
- Owner deve poder encerrar workspace sem suporte

## Required reading

- `internal/db/workspaces.go` (struct + queries)
- `internal/server/server.go` busque `r.Get("/api/workspaces/{id}"` — vê handlers existentes
- `internal/server/server.go::requireWorkspaceRole` (RBAC)
- `internal/db/migrations/039_token_active_workspace.sql` (FK `ON DELETE SET NULL` em auth_tokens.active_workspace_id)
- `internal/sandbox/manager.go::DeleteSandbox` (se precisar limpar sandboxes ativas)

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/041_workspace_deleted_at.sql` | nova migration (soft delete column) |
| `internal/db/workspaces.go` | `SoftDeleteWorkspace(id)` + filtro `deleted_at IS NULL` em queries existentes |
| `internal/server/server.go` | route + handler `handleDeleteWorkspace` |
| `internal/server/api_types.go` | (se houver request body — provavelmente não) |
| `internal/server/server_test.go` | tests httptest |
| `docs/api/openapi.{yaml,json}` | regen via `make openapi` |
| `web/src/lib/api.ts` | `deleteWorkspace(id)` |
| `web/src/components/WorkspaceDetail.tsx` | botão "Delete workspace" + confirmation modal |

## Migration 041 (soft delete)

```sql
-- 041_workspace_deleted_at.sql
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_workspaces_deleted_at
    ON workspaces(deleted_at) WHERE deleted_at IS NOT NULL;
```

## Implementation steps (TDD)

### Step 1: Migration + filtro em queries

1. Crie migration 041
2. Atualize `internal/db/workspaces.go::GetWorkspaceByID`, `GetWorkspaceBySlug`, `ListWorkspacesByUser` etc para incluir `AND deleted_at IS NULL`
3. Test: workspace soft-deleted não aparece em ListWorkspacesByUser

### Step 2: SoftDeleteWorkspace

```go
func (db *DB) SoftDeleteWorkspace(id string) error {
    res, err := db.Exec(
        `UPDATE workspaces SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
        id,
    )
    if err != nil { return err }
    n, _ := res.RowsAffected()
    if n == 0 { return sql.ErrNoRows }
    return nil
}
```

Test:
```go
func TestSoftDeleteWorkspace(t *testing.T) {
    db := setupTestDB(t); defer db.Close()
    db.CreateWorkspaceWithSlug("w-1", "X", "")

    if err := db.SoftDeleteWorkspace("w-1"); err != nil { t.Fatal(err) }

    got, _ := db.GetWorkspaceByID("w-1")
    if got != nil { t.Fatal("expected workspace to be hidden after soft delete") }

    // Duplicate delete returns ErrNoRows
    if err := db.SoftDeleteWorkspace("w-1"); err != sql.ErrNoRows {
        t.Fatalf("expected ErrNoRows, got %v", err)
    }
}
```

### Step 3: Handler `handleDeleteWorkspace`

```go
// internal/server/server.go
r.Delete("/api/workspaces/{id}", s.handleDeleteWorkspace)

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
    wsID := chi.URLParam(r, "id")
    if !s.requireWorkspaceRole(w, r, wsID, "owner") {
        return  // 403 already written
    }

    // 1. Stop running sandboxes for this workspace.
    if err := s.Sandboxes.DeleteAllForWorkspace(r.Context(), wsID); err != nil {
        log.Printf("delete sandboxes for ws %s: %v", wsID, err)
        // continue — soft-delete shouldn't be blocked by sandbox cleanup failure
    }

    // 2. Soft delete the workspace row.
    if err := s.DB.SoftDeleteWorkspace(wsID); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        log.Printf("soft delete workspace %s: %v", wsID, err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }

    // 3. Tokens com active_workspace_id = wsID já estão protegidos por FK ON DELETE SET NULL
    //    quando o workspace for hard-deleted; soft-delete não dispara. Como a UI lista
    //    workspaces ativos, sessions órfãs ficam apontando p/ ws soft-deleted — limpar manualmente:
    if err := s.DB.ClearActiveWorkspace(wsID); err != nil {
        log.Printf("clear active_workspace_id for ws %s: %v", wsID, err)
    }

    w.WriteHeader(http.StatusNoContent)
}
```

### Step 4: Handler test (httptest)

```go
func TestHandleDeleteWorkspaceOwnerOnly(t *testing.T) {
    srv := newTestServer(t)
    srv.auth.Register("u-1", "owner@x.com", "pw")
    srv.auth.Register("u-2", "dev@x.com", "pw")
    srv.db.CreateWorkspaceWithSlug("w-1", "X", "")
    srv.db.AddWorkspaceMember("w-1", "u-1", "owner")
    srv.db.AddWorkspaceMember("w-1", "u-2", "developer")

    // developer cannot delete
    rr := authedRequest(t, srv, "u-2", "DELETE", "/api/workspaces/w-1", nil)
    if rr.Code != 403 { t.Fatalf("dev should get 403, got %d", rr.Code) }

    // owner can
    rr2 := authedRequest(t, srv, "u-1", "DELETE", "/api/workspaces/w-1", nil)
    if rr2.Code != 204 { t.Fatalf("owner should get 204, got %d", rr2.Code) }

    // gone
    rr3 := authedRequest(t, srv, "u-1", "GET", "/api/workspaces/w-1", nil)
    if rr3.Code != 404 { t.Fatalf("expected 404 after delete, got %d", rr3.Code) }
}
```

### Step 5: Frontend (UI confirmation)

`web/src/components/WorkspaceDetail.tsx`:

```tsx
const [confirming, setConfirming] = useState(false);

<Button variant="destructive" onClick={() => setConfirming(true)}>
  Delete workspace
</Button>

{confirming && (
  <ConfirmModal
    title="Delete this workspace?"
    body={`This will hide "${workspace.name}" and all its sandboxes. The action is reversible only via DB intervention.`}
    confirmLabel="Delete"
    onConfirm={async () => {
      await api.deleteWorkspace(workspace.id);
      navigate('/');
    }}
    onCancel={() => setConfirming(false)}
  />
)}
```

### Step 6: OpenAPI regen

```bash
make openapi && make api-docs
git add docs/api/
```

### Step 7: Commit + PR

```bash
git add internal/db/migrations/041_workspace_deleted_at.sql \
        internal/db/workspaces.go internal/db/workspaces_test.go \
        internal/server/server.go internal/server/server_test.go \
        web/src/lib/api.ts web/src/components/WorkspaceDetail.tsx \
        docs/api/

git commit -m "feat(workspaces): DELETE /api/workspaces/{id} soft delete (B02)"
git push -u origin feat/delete-workspace
gh pr create --title "feat(workspaces): DELETE /api/workspaces/{id} (B02)" --body "..."
```

## Acceptance criteria

- [ ] Migration 041 aplica em DB existente
- [ ] Workspaces soft-deleted somem de `GET /api/workspaces` e `GET /api/workspaces/{id}` (404)
- [ ] Apenas role `owner` pode deletar (403 pra outros)
- [ ] DELETE retorna 204
- [ ] Sandbox CRs no namespace do workspace são limpas no delete
- [ ] Tokens com `active_workspace_id` apontando pro ws deletado ficam NULL
- [ ] Frontend tem confirmation modal antes de submeter
- [ ] OpenAPI regenerado e commitado
- [ ] Tests unit + httptest passam
- [ ] CI verde

## Test plan

```bash
# Unit
go test -tags goolm ./internal/db/... ./internal/server/...

# DEV smoke após deploy
DEV="--context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
helm --kube-context "${DEV#--context=}" upgrade ... # vide CLAUDE.md

# Owner deleta workspace de teste
WS_TO_DEL="7c4fb23d-f0fe-4759-8c77-5f8ff36b2c45"  # auto-derive-me
curl -X DELETE https://default-workspace.agentserver.analytics.vtex.com/api/workspaces/$WS_TO_DEL \
  -b /tmp/cj_admin
# expected: 204

# Lista não inclui
curl https://default-workspace.agentserver.analytics.vtex.com/api/workspaces -b /tmp/cj_admin
# expected: array sem o id deletado
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Soft vs hard delete | **Soft** por default (`deleted_at`), `?purge=true` futuro |
| Sandboxes ativas | **Stop** + cleanup CR no momento do delete |
| Tokens órfãos | **Clear** `active_workspace_id` (não invalidar sessão) |
| Subdomínio do ws deletado | **404** — frontend trata como workspace não existe |
| Slug reusável após delete | **NÃO v1** — slug fica "ocupado" pela linha soft-deleted (UNIQUE constraint mantém) |

## Anti-patterns

- ❌ Hard delete sem aviso — perde audit history
- ❌ Permitir `developer`/`viewer` deletar — só `owner`
- ❌ Esquecer de filtrar `deleted_at IS NULL` em SELECTs existentes — workspaces fantasmas
- ❌ Esquecer de limpar sandboxes — pods órfãos consumindo recursos

## Out of scope

- Hard delete / `?purge=true` (vira PR futuro B02.1)
- Restore endpoint (`POST /api/workspaces/{id}/restore`) — backlog
- Bulk delete — backlog
- Email de notificação aos membros — depende de B1

## Definition of done

- PR mergeado em main
- CI verde
- DEV smoke: workspace `auto-derive-me` deletado, some da lista
- Workspaces de teste do PR #57+#58 limpos
