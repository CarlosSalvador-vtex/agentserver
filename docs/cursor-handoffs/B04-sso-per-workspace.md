# Cursor Handoff — B04: SSO por workspace (Opção B)

**Backlog:** B04 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~600
**Tempo:** 3 sprints (1 design + 2 impl + 1 audit)
**Dependências:** Sealed Secrets infra (PR #51 — já em prod-ready)

## Goal

Cada workspace configura seu próprio Identity Provider (Google Workspace, Okta, Azure AD). User com email `@empresa-a.com` é redirecionado pro IdP da Empresa A. Sem isso, agentserver é não-vendável para enterprise.

## Why now

- Compliance SOC2 / ISO 27001 / LGPD para enterprise exige BYO-IdP
- Empresas grandes não compartilham IdP com terceiros
- Sales blocker pra contas top-tier

## Required reading

- [`../workspace-auth-design.md`](../workspace-auth-design.md) Opção B
- [`../sealed-secrets.md`](../sealed-secrets.md) — secret management
- `internal/auth/oidc.go` (se houver — verificar OIDC existente, PR #57 incluiu OIDC subdomain stamp)
- `internal/server/server.go` busque `OIDC` handlers existentes
- Spec OIDC 1.0 + OAuth 2.0 Authorization Code Flow with PKCE
- SAML 2.0 spec (caso vá suportar)

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/043_workspace_sso_configs.sql` | Tabelas SSO config + audit |
| `internal/db/workspace_sso.go` | CRUD config |
| `internal/auth/sso.go` (novo) | Discover/initiate/callback logic |
| `internal/auth/sso_providers.go` (novo) | Presets Google/Okta/Azure |
| `internal/server/handlers_sso.go` (novo) | 7 handlers |
| `internal/server/server.go` | routes |
| `web/src/pages/SSOConfig.tsx` (novo) | UI admin |
| `web/src/components/Login.tsx` | Email-first flow |
| `web/src/lib/api.ts` | SSO API functions |
| `docs/api/openapi.{yaml,json}` | regen |
| `docs/workspace-auth-sso.md` (novo) | guia operacional |

## Migration 043

```sql
-- 043_workspace_sso_configs.sql
CREATE TABLE IF NOT EXISTS workspace_sso_configs (
    workspace_id          TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    provider              TEXT NOT NULL,        -- 'google' | 'okta' | 'azure' | 'generic_oidc' | 'saml'
    issuer_url            TEXT NOT NULL,        -- OIDC discovery URL
    client_id             TEXT NOT NULL,
    client_secret_ref     TEXT NOT NULL,        -- ref pro Sealed Secret (ex: 'sso/ws-uuid/secret')
    allowed_email_domains TEXT[],               -- ['@empresa-a.com', '@subsidiary.com']
    button_label          TEXT DEFAULT 'Sign in with SSO',
    enabled               BOOLEAN NOT NULL DEFAULT false,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workspace_sso_audit_log (
    id           BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_email   TEXT,
    event        TEXT NOT NULL,                 -- 'initiate' | 'callback.success' | 'callback.failure' | 'config.changed'
    ip           TEXT,
    error_msg    TEXT,
    at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sso_audit_workspace_at
    ON workspace_sso_audit_log(workspace_id, at DESC);
```

## API endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/auth/sso/discover?email=<email>` | none | Resolve workspace por email domain → retorna info SSO ou null |
| GET | `/api/auth/sso/initiate?wid=<id>` | none | 302 → IdP authorize URL com PKCE |
| GET | `/api/auth/sso/callback` | none | OAuth callback handler |
| POST | `/api/workspaces/{wid}/sso` | owner | Create/update config |
| GET | `/api/workspaces/{wid}/sso` | owner | Read config (secret_ref retorna mascarado) |
| DELETE | `/api/workspaces/{wid}/sso` | owner | Disable + delete config |
| POST | `/api/workspaces/{wid}/sso/test` | owner | Test connection (resolve issuer + ping) |

## Fluxo de login com SSO

```
1. User abre apex/login OR <slug>.<base>/login
2. Form pede só email primeiro
3. Frontend chama GET /api/auth/sso/discover?email=alice@empresa-a.com
4. Backend:
   a. Domain = "empresa-a.com"
   b. Procura workspace_sso_configs onde 'empresa-a.com' = ANY(allowed_email_domains) AND enabled=true
   c. Se achar: returna { sso_provider, sso_initiate_url, workspace_name }
   d. Se não: returna null → frontend mostra form de senha
5. Se SSO: frontend redireciona pro initiate_url
6. Backend GET /initiate:
   a. Gera state + PKCE code_verifier; guarda em sessão temporária (cookie curto)
   b. 302 pra IdP authorize URL
7. User loga no IdP, IdP redireciona pra /callback?code=...&state=...
8. Backend GET /callback:
   a. Valida state
   b. Troca code por id_token + access_token via PKCE
   c. Valida id_token (signature, iss, aud, exp)
   d. Extrai email do claim
   e. Verifica email domain está em allowed_email_domains
   f. Cria/atualiza user (se não existe, cria com password_hash = NULL — só SSO)
   g. Add como membro do workspace se não for já
   h. Cria session token com active_workspace_id = workspace
   i. Redireciona pra <slug>.<base>/
9. Audit log entrada
```

## Sealed Secret pro client_secret

Por workspace, criar SealedSecret:

```yaml
apiVersion: bitnami.com/v1alpha1
kind: SealedSecret
metadata:
  name: sso-ws-{wid}
  namespace: agentserver
spec:
  encryptedData:
    client_secret: <kubeseal-encrypted>
```

Backend lê via `kubectl get secret sso-ws-{wid} -o jsonpath='{.data.client_secret}' | base64 -d`. **Não** colocar plain text no DB.

`client_secret_ref` no DB guarda só o nome do K8s secret (ex: `sso-ws-22707304-...`).

## Provider presets (UX)

```go
// internal/auth/sso_providers.go
var Presets = map[string]Preset{
    "google": {
        DiscoveryURL: "https://accounts.google.com/.well-known/openid-configuration",
        Scopes:       []string{"openid", "email", "profile"},
        ButtonLabel:  "Sign in with Google",
        Icon:         "google",
    },
    "okta": {
        // user fornece subdomain Okta (ex: empresa-a)
        DiscoveryURLTemplate: "https://{{.Subdomain}}.okta.com/.well-known/openid-configuration",
        Scopes:               []string{"openid", "email", "profile"},
    },
    "azure": {
        DiscoveryURLTemplate: "https://login.microsoftonline.com/{{.TenantID}}/v2.0/.well-known/openid-configuration",
        Scopes:               []string{"openid", "email", "profile"},
    },
}
```

## Frontend (Login.tsx mudanças)

```tsx
const [step, setStep] = useState<'email' | 'password' | 'sso'>('email');
const [email, setEmail] = useState('');
const [ssoInfo, setSSOInfo] = useState(null);

async function onEmailSubmit() {
  const info = await api.discoverSSO(email);
  if (info) {
    setSSOInfo(info);
    setStep('sso');
  } else {
    setStep('password');
  }
}

// step='sso': mostra "Sign in with <provider>" button → redirect
// step='password': form senha original (fallback)
```

## Acceptance criteria

- [ ] Migration 043 aplica
- [ ] Admin pode configurar SSO Google/Okta/Azure via UI
- [ ] `client_secret` armazenado via Sealed Secret (não plain text)
- [ ] Discovery endpoint resolve workspace por email domain
- [ ] Initiate gera state + PKCE corretamente
- [ ] Callback valida state, PKCE, id_token signature, iss, aud, exp
- [ ] User auto-created se primeira vez via SSO
- [ ] User auto-added como member do workspace
- [ ] Session created com active_workspace_id correto
- [ ] Audit log entrada em todos eventos (initiate, callback.{success,failure}, config.changed)
- [ ] Test connection endpoint valida config antes salvar
- [ ] Refusa email domain não-allowlisted
- [ ] CSRF protection (state + cookie sameSite=Lax)
- [ ] Tests unit + integration (mock IdP via httptest)

## Test plan

```bash
# Unit
go test -tags goolm ./internal/auth/sso/... ./internal/server/handlers_sso/...

# Integration com mock OIDC provider
go test -tags goolm ./internal/auth/sso/... -tags integration

# E2E DEV (requer IdP teste — Google workspace de teste ou Okta dev account)
# 1. Config SSO via UI
# 2. Login com email do domain configurado
# 3. Verifica redirect → IdP → callback → session criada
# 4. Verifica audit log
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| SAML 2.0 support v1? | NÃO — só OIDC v1; SAML v2 |
| JIT provisioning de roles | role default = `developer`; mapping de groups → roles v2 |
| User existe local + SSO | Permite — login via SSO atualiza last_login mas mantém password_hash |
| Logout: SP-initiated logout no IdP | v2; v1 só limpa session local |
| Token refresh | Usar refresh_token se IdP der; expira sessão senão |
| Email domain wildcard | NÃO (não permitir `@gmail.com`); só corporate domains |

## Anti-patterns

- ❌ Plain client_secret no DB
- ❌ Skip de validação do id_token signature
- ❌ Reusar state OAuth (CSRF)
- ❌ Trust de email sem verificar `email_verified` claim
- ❌ Permitir SSO sem `allowed_email_domains` (anyone-can-join)
- ❌ Storage de access_token (não precisa após callback)

## Out of scope

- SAML 2.0
- SP-initiated logout
- Group/Role JIT mapping
- Multi-IdP por workspace
- Workspaces.com-domain auto-mapping (admin tem que configurar manual)

## Definition of done

- PR mergeado em main
- Pelo menos 1 SSO provider (Google) funciona E2E em DEV
- Docs: `docs/workspace-auth-sso.md` + admin runbook
- Tests passam (incluindo mock IdP)
- Audit log integration confirmada
- CI verde
