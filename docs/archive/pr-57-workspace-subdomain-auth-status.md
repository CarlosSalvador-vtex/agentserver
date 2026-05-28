# PR #57 — Workspace subdomain auth (Opção A)

**Branch:** `feat/workspace-subdomain-auth`  
**PR:** https://github.com/CarlosSalvador-vtex/agentserver/pull/57  
**Base:** `main` (depende do merge de PR #53 — `active_workspace_id`)  
**Commits:** `85a1d0a` (feature) + `dd5779c` (fix testes CI)

Documento de status: o que foi entregue, validações feitas em DEV e pendências antes/do merge.

---

## Objetivo

Login por workspace via subdomínio `{slug}.<base-domain>`: ao autenticar, o token de sessão já carrega `active_workspace_id` (PR #53), sem round-trip extra de “escolher workspace”. Cookie de sessão **host-only** em hosts de tenant (sem vazar entre subdomínios via `AGENTSERVER_COOKIE_DOMAIN`).

Guia operacional: [`docs/workspace-auth.md`](workspace-auth.md).  
Plano de implementação: [`docs/plans/2026-05-27-workspace-subdomain-auth.md`](plans/2026-05-27-workspace-subdomain-auth.md).

---

## O que foi feito

### Banco de dados

| Item | Arquivo / detalhe |
|------|-------------------|
| Migration **040** | `internal/db/migrations/040_workspace_slug.sql` — coluna `workspaces.slug` NOT NULL, índice único, backfill `slugify(name)` com sufixo `-2`, `-3` em colisão |
| Slug helpers | `internal/db/slug.go` — `ValidateSlug`, `Slugify`, prefixos reservados (`claw`, `hermes`, `www`, etc.) |
| Queries | `GetWorkspaceBySlug`, `CreateWorkspaceWithSlug`, `EnsureWorkspace` (testes/seed) |

### Backend (auth + HTTP)

| Item | Detalhe |
|------|---------|
| `Auth.LoginWithWorkspace` | Valida membro do workspace quando `workspace_slug` está presente; emite token com `active_workspace_id` |
| `POST /api/auth/login` | Campo opcional `workspace_slug`; se vazio, infere do `Host` via `ResolveWorkspaceSlugFromHost` |
| Cookie | `auth.SetTokenCookieHostOnly` quando login em tenant (`HostOnlySessionCookie`) |
| `POST /api/workspaces` | `slug` opcional; derivado do `name` se omitido; 409 se slug duplicado |
| OIDC callback | Workspace inferido do host no callback (tenant) |
| OpenAPI / API reference | Regenerados (`make openapi`, `make api-docs`) |

### Frontend

| Item | Arquivo |
|------|---------|
| Slug no hostname | `web/src/lib/hostname.ts` + testes — `extractWorkspaceSlug`, `isTenantSubdomain`, `ROOT_HOSTS` |
| Login | `web/src/components/Login.tsx` — banner “Signing in to workspace …”, envia `workspace_slug` |
| Criar workspace | `web/src/components/CreateWorkspaceModal.tsx` — slug editável + preview |
| API client | `web/src/lib/api.ts` — `workspace_slug` no login; validação client-side de slug |
| TopBar tenant | Sem workspace switcher em subdomínio de tenant |

### Documentação

- `docs/workspace-auth.md` — guia operacional
- `docs/workspace-auth-design.md` — design (ainda marcado “design only”; atualizar após merge)
- `docs/plans/2026-05-27-workspace-subdomain-auth.md` — plano Tasks 1–11

### Testes e CI

| Verificação | Status |
|-------------|--------|
| `go test` (unit) auth, db, server | OK local e no CI |
| Fix CI `null value in column "slug"` | `dd5779c` — testes passam a usar `db.EnsureWorkspace` / inserts com slug |
| `pnpm build` / `web-build` job | OK no CI (run após push do fix) |
| OpenAPI drift check | OK (no escopo do PR) |
| Integração com `TEST_DATABASE_URL` | Coberto no CI (postgres service) |

**Arquivos de teste alterados no fix:**  
`codex_tokens_testhelper_test.go`, `credential_bindings_testhelper_test.go`, `agent_register_test.go`, `playground_provision_integration_test.go`, `composition_integration_test.go`, `workspace_api_keys_test.go`.

---

## Deploy manual em DEV (Task 10)

Feito fora do pipeline de CI (deploy de imagem só roda em `push` para `main`).

| Passo | Resultado |
|-------|-----------|
| Build/push ECR | `344729309528.dkr.ecr.us-east-1.amazonaws.com/agentserver:auth-slug` |
| Helm upgrade rev **62** | Cluster `dev-ti-eks-analytics-platform`, namespace `agentserver` |
| Rollout | Pod rodando imagem `auth-slug` |
| Migration 040 nos logs | `Applied migration: 040_workspace_slug.sql` |
| `BASE_DOMAIN` no deploy | `agentserver.analytics.vtex.com` |

**Alteração local não commitada:** `values-dev-eks.yaml` com `image.tag: auth-slug` (alinha cluster com o smoke; opcional commitar no PR ou só após merge).

---

## Smoke automatizado (feito)

| Cenário | Como | Resultado |
|---------|------|-----------|
| Health | `https://agentserver.analytics.vtex.com/healthz` | 200 |
| Login inválido (apex) | `POST /api/auth/login` credenciais falsas | 401, corpo `invalid credentials` (sem vazar slug) |
| Login inválido + `workspace_slug` no body | Mesmo host apex | 401, mensagem genérica |
| Login inválido (tenant simulado) | Port-forward + `Host: fake-slug.agentserver.analytics.vtex.com` | 401, mensagem genérica |
| Backend após deploy | Logs do pod | Sem erro pós-migration |

**Não foi possível** listar slugs via `psql` no pod (imagem sem cliente PostgreSQL) nem job ephemeral (taints nos nodes). O backfill da 040 é inferido pelo sucesso da migration.

---

## Pendências

### Bloqueador de UX em DEV/prod (ingress)

O Ingress wildcard `*.agentserver.analytics.vtex.com` aponta para **sandboxproxy**, não para o serviço **agentserver**:

```yaml
# deploy/helm/agentserver/templates/ingress.yaml (trecho)
- host: "*.{{ sandbox.baseDomain }}"
  → service: agentserver-sandboxproxy
```

Efeito observado:

| URL | Comportamento |
|-----|----------------|
| `https://agentserver.analytics.vtex.com/login` | OK (agentserver) |
| `https://<slug>.agentserver.analytics.vtex.com/healthz` | 200 (sandboxproxy) |
| `https://<slug>.agentserver.analytics.vtex.com/login` | **404** |
| `https://<slug>.agentserver.analytics.vtex.com/api/auth/login` | **404** |

Ou seja: a **lógica** de auth por subdomínio está no binário `agentserver`, mas o tráfego HTTP de `{slug}.agentserver.analytics.vtex.com` (fora de `claw-*` / `hermes-*`) **não chega** ao agentserver hoje.

**Workaround para smoke manual de auth:**

1. Login no **apex** com `workspace_slug` no JSON (o frontend já envia quando o hostname parseia slug — no apex use API ou UI após expor slug).
2. Port-forward + header `Host: <slug>.agentserver.analytics.vtex.com` (valida backend isoladamente).

**Follow-up sugerido (fora ou junto ao merge):**

- sandboxproxy encaminhar hosts `{slug}` (não `claw-*` / `hermes-*`) para upstream agentserver; ou
- regra de Ingress adicional / prioridade de roteamento para UI+API de workspace no wildcard; ou
- subdomínio dedicado só para auth (ex. documentar convenção diferente de `BASE_DOMAIN`).

### Smoke manual com credenciais reais (checklist PR)

- [ ] Login membro com `workspace_slug` válido → `GET /api/auth/me` com `active_workspace_id` correto
- [ ] Login de não-membro com slug de outro workspace → 401 genérico
- [ ] Apex sem slug: picker + `POST /api/auth/session/workspace` (regressão PR #53)
- [ ] Criar workspace com slug customizado; slug duplicado → 409
- [ ] OIDC no host tenant (se usado em DEV)
- [ ] Inspecionar cookie: sem atributo `Domain` em login tenant (host-only)
- [ ] Browser em `{slug}.…/login` — **só após** corrigir roteamento ingress/proxy

### Merge / repo

- [ ] PR #53 mergeado em `main` antes (ou rebase desta branch)
- [ ] Revisão de código + merge #57
- [ ] Após merge: pipeline publica imagem; atualizar tag em `values-dev-eks.yaml` pelo fluxo normal do time
- [ ] Atualizar `docs/workspace-auth-design.md` — status “implementado” / link para este doc
- [ ] Marcar checklist no corpo do PR #57 no GitHub

### Escopo v1 explicitamente fora / depois

| Item | Referência |
|------|------------|
| SSO por workspace (Opção B) | design doc |
| Register com `workspace_slug` | ignorado; usar apex |
| Cookie codex-auth cross-subdomain | plano Task 12 |
| Commit de `web/dist/index.html` | artefato de build; não incluir no PR |

### Segurança operacional

Credenciais AWS temporárias foram coladas no chat durante o deploy — tratar como expostas; não repetir em issues/docs; preferir `export` só no terminal local.

---

## Comandos úteis

```bash
# Testes locais (slug/auth)
go test -tags goolm ./internal/auth/... ./internal/server/... ./internal/db/...

# Smoke login com slug (apex + body) — substituir credenciais e slug
curl -s -c /tmp/cj -X POST https://agentserver.analytics.vtex.com/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"USER@example.com","password":"***","workspace_slug":"SEU-SLUG"}'
curl -s -b /tmp/cj https://agentserver.analytics.vtex.com/api/auth/me

# DEV: logs migration
DEV_CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
kubectl --context "$DEV_CTX" logs -n agentserver deploy/agentserver -c agentserver | grep 040
```

---

## Resumo executivo

| Área | Situação |
|------|----------|
| Código + testes + CI | Pronto para review/merge (após PR #53) |
| Migration + backend em DEV | Aplicado (`auth-slug`) |
| Login API no apex | Validado (401 genérico; fluxo feliz pendente credenciais) |
| Login UI/API em `{slug}.agentserver…` | **Pendente roteamento** wildcard → agentserver |
| Documentação PR | Este arquivo |
