# Cursor Handoff — B03: DELETE user (LGPD/GDPR)

**Backlog:** B03 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~120
**Tempo:** 1 dia
**Dependências:** B07 (audit log) recomendado — preservar history corretamente

## Goal

Endpoint `DELETE /api/users/{id}` (global admin only) que anonimiza dados pessoais sem quebrar integridade referencial. Atende direito ao esquecimento (LGPD Art. 18 / GDPR Art. 17).

## Why now

- LGPD/GDPR exigem mecanismo de exclusão de dados pessoais a pedido do titular
- Hoje só SQL manual no prod — sujeito a erro + falta auditoria
- Users desligados acumulam (turnover de empresas tenants)

## Required reading

- `internal/db/users.go` (schema users)
- `internal/db/tokens.go` (FK ON DELETE no auth_tokens — verificar)
- `internal/db/migrations/039_token_active_workspace.sql` (FK behavior)
- `internal/server/server.go` busque `requireGlobalAdmin` ou similar
- [`B02-delete-workspace.md`](B02-delete-workspace.md) — padrão soft delete

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/042_user_anonymized_at.sql` | column `anonymized_at`, `original_email_hash` |
| `internal/db/users.go` | `AnonymizeUser(id)` |
| `internal/server/server.go` | route + handler |
| `internal/server/handlers_users.go` (novo) | `handleDeleteUser` |
| `docs/api/openapi.{yaml,json}` | regen |

## Migration 042

```sql
-- 042_user_anonymized_at.sql
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS original_email_hash TEXT;

-- Index pra dedup futura (não permitir mesmo email anonimizado 2x).
CREATE INDEX IF NOT EXISTS idx_users_anonymized
    ON users(anonymized_at) WHERE anonymized_at IS NOT NULL;
```

## Anonymização (não delete)

```go
// AnonymizeUser substitui PII por placeholders mas mantém o row pra
// preservar integridade referencial (audit logs, workspace_members.created_by, etc).
func (db *DB) AnonymizeUser(userID string) error {
    return db.WithTx(func(tx *sql.Tx) error {
        // 1. Get current email pra hash + audit
        var email string
        if err := tx.QueryRow(`SELECT email FROM users WHERE id = $1`, userID).
            Scan(&email); err != nil {
            return err
        }
        emailHash := fmt.Sprintf("%x", sha256.Sum256([]byte(email)))

        // 2. Anonimizar
        _, err := tx.Exec(`
            UPDATE users
            SET email = $1,
                name = NULL,
                picture = NULL,
                password_hash = NULL,
                anonymized_at = NOW(),
                original_email_hash = $2
            WHERE id = $3
        `, fmt.Sprintf("deleted-%s@anonymized.local", userID), emailHash, userID)
        if err != nil { return err }

        // 3. Invalidar todas as sessions ativas
        if _, err := tx.Exec(`DELETE FROM auth_tokens WHERE user_id = $1`, userID); err != nil {
            return err
        }

        // 4. Remover de todos os workspaces (mas preservar audit logs)
        if _, err := tx.Exec(`DELETE FROM workspace_members WHERE user_id = $1`, userID); err != nil {
            return err
        }

        return nil
    })
}
```

## Restrições

| Cenário | Comportamento |
|---|---|
| User é último `owner` de um workspace | 409 — "transfer ownership first" |
| User tem invites pendentes criados | invites apontam pro `created_by` deletado — manter (não cascade) |
| User é o caller (self-delete) | Permitido, mas sessão dele invalidada (precisa relogar pra confirmar) |
| Já anonimizado | 410 Gone |

## Handler

```go
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
    targetID := chi.URLParam(r, "id")
    callerID := auth.UserIDFromContext(r.Context())

    // RBAC: global admin OR self
    callerRole, _ := s.DB.GetUserRole(callerID)
    if callerID != targetID && callerRole != "admin" {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }

    // Pre-check: user is last owner anywhere?
    orphans, err := s.DB.WorkspacesWhereUserIsLastOwner(targetID)
    if err != nil { http.Error(w, "internal", 500); return }
    if len(orphans) > 0 {
        http.Error(w, fmt.Sprintf(
            "transfer ownership of %d workspace(s) before delete: %v",
            len(orphans), orphans), http.StatusConflict)
        return
    }

    if err := s.DB.AnonymizeUser(targetID); err != nil {
        http.Error(w, "internal", 500); return
    }

    // Audit (vide B07)
    s.Audit.Log(r.Context(), "user.anonymized", map[string]any{
        "target_user_id": targetID,
        "actor_user_id":  callerID,
    })

    w.WriteHeader(http.StatusNoContent)
}
```

## Acceptance criteria

- [ ] Migration 042 aplica
- [ ] Anonimização preserva `id` (FK não quebra)
- [ ] Email pós-anonimização tem padrão `deleted-<id>@anonymized.local`
- [ ] `password_hash`, `name`, `picture` ficam NULL
- [ ] `original_email_hash` permite forensics futura (sem reverter PII)
- [ ] Sessions ativas do user invalidadas
- [ ] Memberships removidas
- [ ] Last owner blocking funciona
- [ ] Self-delete permitido
- [ ] Audit log entrada criada
- [ ] Tests unit + httptest

## Test plan

```bash
go test -tags goolm ./internal/db/... ./internal/server/...

# Smoke DEV
ADMIN_COOKIE=...
TARGET=tester-empresa-custom user id

# 1. Tenta deletar tester (já tem membership)
curl -X DELETE -b $ADMIN_COOKIE https://.../api/users/$TARGET
# expected: 204

# 2. Confirma anonimização
curl -b $ADMIN_COOKIE https://.../api/admin/users/$TARGET
# email: deleted-<id>@anonymized.local
# anonymized_at: not null

# 3. Tenta login com senha antiga
curl -X POST https://.../api/auth/login -d '{"email":"tester-...","password":"test12345"}'
# expected: 401 (email já não existe nesse formato)
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Soft anonymize vs hard delete | **Soft anonymize** — integridade referencial |
| Quem pode deletar | global admin OR self |
| Cooldown (delete imediato vs delay 30 dias) | imediato; backup faz role de undo |
| Notificar workspaces que perderam membro | feature futura |
| Anonimizar audit log antigo (referências) | NÃO — audit imutável |

## Anti-patterns

- ❌ Hard delete: quebra audit + referenced rows
- ❌ Permitir delete via session do próprio user sem confirmação dupla
- ❌ Manter password_hash mesmo após anonymize — vetor de ataque
- ❌ Permitir delete cross-tenant por admin de workspace (não é admin global)

## Definition of done

- PR mergeado
- Smoke DEV completo: anonymize → login fail → audit row presente
- CI verde
- Doc `docs/workspace-auth.md` ganha seção "User deletion (LGPD)"
