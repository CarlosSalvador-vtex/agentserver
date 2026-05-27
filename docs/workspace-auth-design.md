# Workspace-Scoped Authentication — Design

> **Status:** design only — not implemented. Decide between A/B/C before scheduling.
> **Predecessor:** PR #53 (workspace-scoped session via `active_workspace_id`)
> **Related:** `docs/workspace-session-auth.md`

## Problem

agentserver é SaaS multi-tenant onde **workspace = empresa**. Hoje o login é
global (`/login` aceita qualquer user do banco); só **depois** o usuário
escolhe workspace via `POST /api/auth/session/workspace` (PR #53).

Limitações:

- Empresas querem branding próprio (URL com nome da empresa, logo no login)
- Empresas querem SSO próprio (Google Workspace, Okta, Azure AD) — não compartilhar IdP
- Compliance (LGPD, SOC2) pede isolamento de credenciais por tenant
- UX: usuário acessa `agentserver.com` e não sabe em qual empresa entrar antes de logar

## 3 abordagens

### Opção A — Subdomínio por workspace

Cada empresa vira `<slug>.agentserver.com`.

```
empresa-a.agentserver.com/login  → login locked to workspace-A
empresa-b.agentserver.com/login  → login locked to workspace-B
agentserver.com/login            → marketing page OR pede slug
```

Frontend lê `hostname.split('.')[0]` → manda `workspace_slug` no login →
backend valida user é membro do workspace → seta `active_workspace_id` no
token automaticamente.

**Mudanças:**

| Item | Detalhe |
|---|---|
| Migration 040 | `ALTER TABLE workspaces ADD slug TEXT UNIQUE NOT NULL`; backfill com `slugify(name)` |
| `POST /api/auth/login` | aceita `workspace_slug` opcional |
| Backend | valida `IsWorkspaceMember(user.id, workspace.id)` antes de criar sessão; seta `active_workspace_id` no token recém-criado |
| Frontend | lê subdomínio do hostname; injeta no login; esconde workspace switcher quando subdomínio detectado |
| Workspace create UI | gera slug auto + valida (kebab-case, único, palavras reservadas: `www`, `api`, `admin`, `app`) |
| Ingress/cert | DEV já tem wildcard `*.agentserver.analytics.vtex.com` |
| Root domain | redireciona pra `<slug>.agentserver.com` ou marketing page |

**LOC estimado:** ~150 (migration + 2 handlers + 1 component).

**Trade-offs:**

| ✅ | ❌ |
|---|---|
| Baixo custo | Multi-workspace user precisa logar separado em cada subdomínio |
| Branding por empresa via subdomínio | Compartilhar URL fica menos prático (sempre prefixo) |
| Workspace context óbvio na URL | Não resolve compliance de IdP próprio |
| Compatible com PR #53 (subdomínio → seta active_workspace_id) | |

---

### Opção B — SSO por workspace (Google / Okta / Azure)

Cada empresa configura seu próprio IdP. User com email `@empresa-a.com` é
redirecionado pro IdP da Empresa A.

```
GET /login → "Digite seu email"
user digita alice@empresa-a.com
backend: SELECT * FROM workspace_sso_configs WHERE 'empresa-a.com' = ANY(allowed_email_domains)
         → redirect pro IdP da Empresa A
SAML/OIDC callback → cria/atualiza user → bind active_workspace_id da Empresa A
```

**Mudanças:**

| Item | Detalhe |
|---|---|
| Migration 040 | Tabela `workspace_sso_configs` (workspace_id, provider, issuer_url, client_id, client_secret_ref, allowed_email_domains[], created_at) |
| Migration 041 | Tabela `workspace_sso_audit_log` (login attempts, failures) |
| Backend | Handlers `/api/auth/sso/initiate`, `/api/auth/sso/callback`; resolve workspace por email domain; OAuth/SAML dance |
| Admin UI | Página `/workspace/{wid}/settings/sso` — configurar IdP, testar conexão |
| Secret management | `client_secret` via Sealed Secrets (PR #51) ou external secret |
| Fallback | User com email não mapeado → erro "no SSO configured for your domain" |

**LOC estimado:** ~600 (migration + OAuth/SAML handlers + admin UI + tests).

**Trade-offs:**

| ✅ | ❌ |
|---|---|
| Compliance enterprise (SOC2, ISO) | LOC alto |
| Zero senha local (mais seguro) | Quebra signup self-service de membros novos |
| Onboarding self-service via IdP | Custos: cada workspace precisa de admin pra setup |
| Audit log centralizado por workspace | Complexidade de SAML é alta (XML, certs, metadata) |

---

### Opção C — Híbrido (subdomínio + SSO opcional)

A + B juntos. Subdomínio força workspace; SSO opcional por workspace; senha
local como fallback.

```
empresa-a.agentserver.com → workspace-A locked
  └─ se workspace-A tem SSO config → redirect pro IdP
  └─ se não → tela de email+senha (mas só users de workspace-A passam)
```

Padrão da indústria (Slack, Notion fazem isso).

**LOC estimado:** ~700 (A + B + lógica de fallback + UI de admin pra escolher).

---

## Comparação

| Critério | A — Subdomínio | B — SSO | C — Híbrido |
|---|---|---|---|
| LOC | ~150 | ~600 | ~700 |
| UX corporativa | OK | Excelente | Excelente |
| Bring-your-own-IdP | Não | Sim | Sim |
| Compliance SAML required | Não | Sim | Sim |
| Multi-workspace user UX | Quebra (1 login por subdomínio) | OK (1 login global, IdP escolhe) | Misto |
| Velocidade pra entregar | 1 sprint | 3 sprints | 4 sprints |
| Reuso com PR #53 | Direto (subdomínio → active_workspace_id) | Direto (callback → active_workspace_id) | Direto |

---

## Recomendação

**Começar com Opção A.** Depois evoluir pra C se compliance exigir.

**Por quê:**

1. Resolve o problema "auth por workspace" com fração do custo
2. Build-up pra C: B encaixa em cima de A sem rework
3. DEV já tem wildcard cert configurado (`values-dev-eks.yaml`:60 — 2 ACM certs cobrem subdomínios)
4. Multi-workspace user é raro em SaaS B2B real (regra: maioria pertence a 1 só)
5. Não bloqueia ninguém — quem precisa SSO espera B; quem só quer branding tem A

## Plano S6-PR1 (se for Opção A)

| Step | Arquivo | LOC |
|---|---|---|
| 1 | `internal/db/migrations/040_workspace_slug.sql` (ALTER + backfill) | 10 |
| 2 | `internal/db/workspaces.go::GetWorkspaceBySlug` | 15 |
| 3 | `internal/server/auth_login.go` — aceitar `workspace_slug` no request | 30 |
| 4 | `internal/auth/auth.go::CreateTokenForWorkspace` (variant que já popula active_workspace_id) | 20 |
| 5 | `web/src/lib/api.ts` — adicionar `workspace_slug` ao login req | 5 |
| 6 | `web/src/components/Login.tsx` — ler `window.location.hostname` + injetar | 20 |
| 7 | `web/src/components/WorkspaceCreateModal.tsx` — campo slug auto-gerado | 30 |
| 8 | Validador de slug — kebab-case, único, reserved words | 20 |
| 9 | `docs/workspace-auth.md` user-facing doc | 50 |
| 10 | Tests | 60 |

**Total:** ~260 LOC. **Sprint:** 1 semana.

## Considerações de segurança

- **Slug squatting:** validar reserved words (`www`, `api`, `admin`, `app`, `claw-*`, `hermes-*` já usados por sandbox subdomains)
- **Slug enumeration:** 404 pra slug inexistente NÃO deve dizer "workspace not found" — usar mensagem genérica pra não enumerar tenants
- **Cookie scope:** cookie de sessão deve ser scoped por subdomínio (não cross-tenant) — `Domain=.agentserver.com` é INSEGURO. Usar `Domain=empresa-a.agentserver.com` (host-only)
- **CORS:** restringir `Access-Control-Allow-Origin` ao subdomínio do workspace, não wildcard

## Considerações operacionais

- **DNS:** wildcard já existe em DEV. Pra prod precisa wildcard cert ACM + Route53 record
- **Subdomain conflicts:** sandboxes usam `claw-<sid>` e `hermes-<sid>` — reserved namespace
- **Migration custo:** workspaces existentes precisam slug. Backfill via `slugify(name)` + dedup (`empresa-a`, `empresa-a-2`, ...)
- **Sandbox URLs:** ficam onde estão (`claw-<sid>.agentserver.com`) — não viram subworkspace porque são per-sandbox
- **Email links:** todos os links em emails (reset password, invite) precisam usar subdomínio do workspace destinatário

## Referências

- PR #53 + `docs/workspace-session-auth.md` — predecessor (session-level workspace binding)
- Slack docs sobre workspace URLs — referência de UX
- Auth0 / WorkOS — para Opção B (SSO multi-tenant), considerar SaaS em vez de roll-your-own
