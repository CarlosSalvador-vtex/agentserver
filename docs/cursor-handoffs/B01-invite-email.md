# Cursor Handoff — B01: Invite por email

**Backlog:** B01 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~200
**Tempo:** 1-2 dias
**Dependências:** mailer config (a definir)

## Goal

Fluxo de onboarding self-service via convite por email. Admin gera link de convite no workspace; user clica, cria conta (se já não tem), e vira membro automaticamente.

## Why now

- Hoje admin precisa **adicionar membro por email**, e o user já tem que existir no banco. Cria fricção: admin manda invite manualmente fora da plataforma → user registra no apex → admin adiciona.
- Padrão SaaS: invite → link → signup integrado.

## Required reading

- [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md) seção B01
- `internal/server/server.go::handleAddMember` (linha ~1470)
- `internal/auth/auth.go::Register` + `LoginWithWorkspace`
- `internal/db/migrations/040_workspace_slug.sql` (formato de migration)
- `docs/workspace-auth.md` (URLs por subdomínio)
- `docs/sealed-secrets.md` (pra credentials do mailer)

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/041_workspace_invites.sql` | nova tabela |
| `internal/db/invites.go` | CRUD invite |
| `internal/db/invites_test.go` | tests |
| `internal/notif/mailer.go` | interface mailer (novo pacote) |
| `internal/notif/ses_mailer.go` | impl SES (ou Resend — vide decisão) |
| `internal/notif/dev_mailer.go` | mock pra DEV (loga stdout em vez de enviar) |
| `internal/server/api_types.go` | InviteCreateRequest, InviteInfo, InviteAcceptRequest |
| `internal/server/handlers_invites.go` | 3 handlers |
| `internal/server/server.go` | routes |
| `web/src/components/Workspace*` | UI invite + accept-invite page |
| `web/src/lib/api.ts` | functions |
| `docs/api/openapi.{yaml,json}` | regen |

## Migration 041

```sql
-- 041_workspace_invites.sql
CREATE TABLE IF NOT EXISTS workspace_invites (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email          TEXT NOT NULL,
    role           TEXT NOT NULL DEFAULT 'developer',
    token_hash     TEXT NOT NULL,         -- sha256 do token (token plain só no email)
    expires_at     TIMESTAMPTZ NOT NULL,
    accepted_at    TIMESTAMPTZ,
    created_by     TEXT NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One pending invite per (workspace, email).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_workspace_invites_pending
    ON workspace_invites(workspace_id, email)
    WHERE accepted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_invites_token_hash
    ON workspace_invites(token_hash);
```

## API endpoints

### `POST /api/workspaces/{wid}/invites` (admin only)

Request:
```json
{ "email": "alice@empresa-a.com", "role": "developer" }
```

Response 201:
```json
{
  "id": "inv-uuid",
  "email": "alice@empresa-a.com",
  "role": "developer",
  "expires_at": "2026-06-03T...",
  "invite_url": "https://empresa-a.agentserver.analytics.vtex.com/accept-invite?token=<plain>"
}
```

Side effect: envia email se mailer configurado; loga URL se dev mailer.

### `GET /api/auth/invite/{token}` (no auth)

Response 200:
```json
{
  "workspace_name": "Empresa A",
  "workspace_slug": "empresa-a",
  "role": "developer",
  "email": "alice@empresa-a.com",
  "expires_at": "2026-06-03T..."
}
```

404 se token inexistente ou expirou. **Não distinguir** as duas causas pra evitar enumeration.

### `POST /api/auth/invite/{token}/accept` (no auth)

Request:
```json
{ "password": "new-strong-pw" }
```

Lógica:
1. Busca invite por `token_hash = sha256(token)`. Se inexistente, expirado, ou já aceito → 404.
2. Se já existe user com `email` do invite:
   - Valida senha. Se errada → 401.
   - Adiciona como membro do workspace.
3. Senão:
   - Cria user com password.
   - Adiciona como membro.
4. Marca invite `accepted_at = NOW()`.
5. Cria session token com `active_workspace_id = workspace.id`.
6. Set-Cookie (host-only no subdomínio).

Response 200:
```json
{ "status": "ok", "active_workspace_id": "..." }
```

## Mailer interface

```go
// internal/notif/mailer.go
package notif

type Mailer interface {
    SendInvite(ctx context.Context, to, workspaceName, inviteURL string) error
}
```

DEV: `DevMailer` loga `[invite-mail] to=alice@x.com url=https://...` em stdout.

PROD: `SESMailer` usa `github.com/aws/aws-sdk-go-v2/service/sesv2` com IRSA. Templating mínimo (HTML + texto).

Config via env: `NOTIF_MAILER=ses|dev`, `NOTIF_SES_REGION`, `NOTIF_FROM=noreply@<base>`.

## Implementation steps (TDD)

### Step 1: Migration + DB layer

1. Crie migration 041
2. Crie `invites.go` com:
   - `CreateInvite(workspaceID, email, role, tokenHash, createdBy, expiresAt) (*Invite, error)`
   - `GetInviteByTokenHash(tokenHash) (*Invite, error)`  — só retorna se `accepted_at IS NULL AND expires_at > NOW()`
   - `MarkInviteAccepted(id) error`
3. Tests de cada query.

### Step 2: Mailer

1. Interface + `DevMailer` (~30 LOC)
2. `SESMailer` (~80 LOC, IRSA-aware via SDK default credential chain)
3. Wire em `cmd/agentserver/main.go` (escolha por env)

### Step 3: Handler `handleCreateInvite`

Test primeiro (httptest):
```go
func TestCreateInvite_OwnerOnly_GeneratesURL(t *testing.T) { ... }
```

Impl: gerar token via `crypto/rand` 32 bytes → hex; calcular `sha256(token)` → guardar hash; URL com slug do workspace:

```go
inviteURL := fmt.Sprintf("https://%s.%s/accept-invite?token=%s",
    workspace.Slug, s.BaseDomain, token)
```

(`s.BaseDomain` precisa vir de config — vide `internal/server/config.go`. Se não existir, adicionar via env `BASE_DOMAIN`.)

### Step 4: Handler `handleGetInvite` (no auth)

Decode token from query → hash → lookup → returna metadata sem expor que invite existe (404 genérico em qualquer falha).

### Step 5: Handler `handleAcceptInvite`

Atomicidade: usar transação com `BEGIN; ... COMMIT;` envolvendo:
- create user (se não existir)
- add member
- mark invite accepted
- create token with active_workspace_id

Rollback em qualquer falha.

### Step 6: Frontend

`/accept-invite?token=...` é rota nova. Detecta tenant subdomain do host (já existe `extractWorkspaceSlug`).

```tsx
// web/src/pages/AcceptInvite.tsx
useEffect(() => {
  api.getInvite(token).then(setInvite).catch(() => setError('invalid invite'));
}, []);

// Form: password (auto-fill email read-only)
// On submit: api.acceptInvite(token, password) → navigate('/')
```

Workspace detail UI: botão "Invite member" → modal:

```tsx
<Modal>
  <Input label="Email" value={email} />
  <Select label="Role" options={['owner','maintainer','developer','viewer']} />
  <Button onClick={async () => {
    const inv = await api.createInvite(wsID, { email, role });
    setInviteUrl(inv.invite_url);   // mostra pro admin copiar/enviar manualmente também
  }}>Send invite</Button>
</Modal>
```

### Step 7: OpenAPI + commit + PR

```bash
make openapi && make api-docs
git add ...
git commit -m "feat(invites): workspace invites by email (B01)"
```

## Acceptance criteria

- [ ] Migration 041 aplica em DB existente
- [ ] Admin pode criar invite via API + UI (apenas owner/maintainer)
- [ ] Token entregue só na response do POST + email (nunca em SELECT — só hash no DB)
- [ ] Invite URL usa subdomínio do workspace (não apex)
- [ ] User existente com senha certa: invite accept cria membership + login
- [ ] User existente com senha errada: 401 (não vaza que email já existe)
- [ ] User novo: invite accept cria conta + membership + login atomicamente
- [ ] Invite expirado: 404 genérico
- [ ] Invite já aceito: 404 genérico (não pode reusar)
- [ ] Duplicate pending invite pro mesmo (workspace,email): 409 ou substitui o pendente (decidir)
- [ ] Email enviado em prod (DEV: stdout log)
- [ ] Frontend tem fluxo accept completo
- [ ] OpenAPI regenerado
- [ ] CI verde

## Test plan

```bash
# Unit
go test -tags goolm ./internal/db/... ./internal/server/... ./internal/notif/...

# DEV smoke
# 1. Admin gera invite
curl -X POST https://default-workspace.<dev>/api/workspaces/<wid>/invites \
  -b /tmp/cj_admin -d '{"email":"new@x.com","role":"developer"}'
# response inclui invite_url

# 2. Pegar token do invite_url + GET pra ver info
curl https://<slug>.<dev>/api/auth/invite/<token>

# 3. Aceitar (novo user)
curl -c /tmp/cj_new -X POST https://<slug>.<dev>/api/auth/invite/<token>/accept \
  -d '{"password":"newpw123"}'

# 4. /me reflete membership
curl -b /tmp/cj_new https://<slug>.<dev>/api/auth/me
# active_workspace_id deve ser o do invite
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Mailer provider | SES (já tem IRSA infra) > Resend (SaaS, mais fácil) > Mailgun |
| Token TTL | 7 dias default; admin pode passar `?ttl=24h` |
| Duplicate pending invite | **Substitui** (1 invite ativo por workspace+email) |
| Aceitar invite com email diferente | **Rejeitar** — email do invite é canonical |
| Convidar como `owner` | **Permitir** mas só owners atuais podem fazer isso (não maintainers) |
| Limite de convites pendentes | 50 por workspace (anti-abuse) |
| Notificação ao admin quando aceito | feature futura |

## Anti-patterns

- ❌ Guardar token em plain text no DB — só hash
- ❌ Token via URL em email não-HTTPS — sempre HTTPS
- ❌ Permitir aceito múltiplo do mesmo invite — `accepted_at` UNIQUE
- ❌ Vazar se email já existe na plataforma — resposta de aceito uniforme
- ❌ Enviar invite pra `*@gmail.com` (catch-all) sem CAPTCHA — anti-spam

## Out of scope

- Resend invite endpoint (futuro)
- Bulk invite via CSV (futuro)
- Custom invite message do admin (futuro)
- Invite analytics (acceptance rate)
- SAML/OIDC invite (depende B04)

## Definition of done

- PR mergeado em main
- DEV smoke completo: criar invite → aceitar → login automático
- Email enviado em DEV (mailer dev) com URL correta
- Tests passam no CI
- OpenAPI commitada
- Doc atualizada: `docs/workspace-auth.md` ganha seção "Inviting members"
