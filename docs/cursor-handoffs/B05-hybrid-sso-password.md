# Cursor Handoff — B05: Híbrido SSO + senha local (Opção C)

**Backlog:** B05 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** +150 sobre B04 (~750 total)
**Tempo:** 1 sprint (após B04 mergeado)
**Dependências:** B04 completo

## Goal

Adicionar fallback de senha local em workspaces com SSO configurado, para casos break-glass (IdP fora do ar) ou usuários convidados que ainda não têm conta no IdP.

## Why now

- SSO down = ninguém entra no workspace. Risco operacional alto.
- Convidados externos (consultores, parceiros) podem não ter conta no IdP do workspace.
- Admins precisam de break-glass — rotina padrão em compliance.

## Required reading

- [`B04-sso-per-workspace.md`](B04-sso-per-workspace.md) — pré-requisito
- [`../workspace-auth-design.md`](../workspace-auth-design.md) Opção C
- `internal/auth/auth.go::LoginWithWorkspace` (vai sofrer mods)

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/044_sso_fallback_flag.sql` | flag `allow_password_fallback` |
| `internal/db/workspace_sso.go` | expose flag em CRUD |
| `internal/auth/auth.go` | login flow checa `allow_password_fallback` |
| `internal/server/handlers_sso.go` | endpoint toggle flag |
| `web/src/pages/SSOConfig.tsx` | checkbox "Allow password fallback" |
| `web/src/components/Login.tsx` | UI fallback button + "use password" link |

## Migration 044

```sql
-- 044_sso_fallback_flag.sql
ALTER TABLE workspace_sso_configs
    ADD COLUMN IF NOT EXISTS allow_password_fallback BOOLEAN NOT NULL DEFAULT false;
```

## Fluxo

```
{slug}.<base>/login →
  workspace.sso_enabled?
    sim:
      mostra botão "Sign in with <Provider>" (default)
      if allow_password_fallback:
        mostra link "Use password instead" (toggle pra form senha)
      else:
        sem opção senha
    não:
      form senha original
```

## Mudança no LoginWithWorkspace

```go
// internal/auth/auth.go
func (a *Auth) LoginWithWorkspace(email, password, workspaceSlug string) (string, string, bool) {
    if workspaceSlug != "" {
        ws, err := a.db.GetWorkspaceBySlug(workspaceSlug)
        if err != nil || ws == nil { return "", "", false }

        sso, err := a.db.GetSSOConfig(ws.ID)
        if err == nil && sso != nil && sso.Enabled {
            // SSO is the canonical path. Allow password ONLY if fallback enabled.
            if !sso.AllowPasswordFallback {
                a.auditSSO(ws.ID, email, "password_blocked_sso_required", "")
                return "", "", false
            }
            // Fallthrough to password flow.
        }
    }

    // Existing password verification path
    token, userID, ok := a.Login(email, password)
    if !ok { return "", "", false }

    if workspaceSlug != "" {
        // ... existing stamp active_workspace_id logic
    }
    return token, userID, true
}
```

## Frontend changes

```tsx
// Login.tsx (additions on top of B04 flow)
{step === 'sso' && (
  <>
    <Button onClick={initiateSSO}>{ssoInfo.button_label}</Button>
    {ssoInfo.allow_password_fallback && (
      <a onClick={() => setStep('password')} className="text-sm">
        Use password instead
      </a>
    )}
  </>
)}
```

`/api/auth/sso/discover` response ganha campo `allow_password_fallback`.

## Acceptance criteria

- [ ] Migration 044 aplica
- [ ] Admin UI tem checkbox "Allow password fallback" + warning explicando o risco
- [ ] SSO-only workspace (`allow_password_fallback=false`): login com password retorna 401
- [ ] SSO + fallback workspace: login com password OU SSO ambos funcionam
- [ ] Audit log registra `password_used_when_sso_available` (alerta)
- [ ] Frontend mostra link "Use password instead" só quando flag ON
- [ ] Tests cobrem ambos cenários

## Test plan

```bash
go test -tags goolm ./internal/auth/...

# Smoke DEV
# 1. Workspace com SSO + fallback OFF → password 401
# 2. Workspace com SSO + fallback ON → password 200 + audit log
# 3. Workspace sem SSO → password normal
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Fallback é per-user ou per-workspace | per-workspace v1 (per-user v2) |
| Alertar audit pra cada password login com SSO disponível | Sim — sinal de compromisso possível |
| Senha mínima quando SSO ON | rotacionar trimestralmente ou exigir 2FA — backlog |
| Bloquear fallback após N falhas SSO | feature futura |

## Anti-patterns

- ❌ Permitir fallback como default ao criar SSO config — explicit opt-in
- ❌ Não auditar password login em workspace SSO-enabled
- ❌ Esconder fallback completamente — admin emergência precisa saber que existe

## Out of scope

- 2FA obrigatório quando fallback ON (backlog)
- Limite de N usos do fallback por dia (backlog)
- Break-glass time-boxed (ativa fallback por 24h depois desativa)

## Definition of done

- PR mergeado
- Tests passam
- Doc `docs/workspace-auth-sso.md` ganha seção "Password fallback"
- DEV smoke completo
