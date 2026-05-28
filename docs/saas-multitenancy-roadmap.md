# SaaS Multi-Tenancy Roadmap

> **Contexto:** agentserver está sendo evoluído de single-tenant para SaaS — um deploy, N empresas isoladas por workspace. Este documento mapeia todos os gaps, opções e recomendações.

---

## Shipped / Closed Gaps

Backlog items from the docs sprint (B01–B10 in `CURSOR_CONTEXT.md`) and related playground work **already merged to `main`**. Do not re-implement; use this when planning v2/v3.

| Backlog / theme | What shipped | PR / evidence |
|---|---|---|
| **B01** — Email subdomain URLs (invite links on tenant host) | Workspace invites API/UI, accept flow, email templates path | [#71](https://github.com/CarlosSalvador-vtex/agentserver/pull/71) (`feat/workspace-invites`); subdomain login context in [#57](https://github.com/CarlosSalvador-vtex/agentserver/pull/57) |
| **B07** — Workspace slug validation | Reserved slugs, login sets `active_workspace_id`, session audit on login | [#57](https://github.com/CarlosSalvador-vtex/agentserver/pull/57) (`internal/db/slug.go`); session audit in [#71](https://github.com/CarlosSalvador-vtex/agentserver/pull/71) |
| Tenant-scoped catalog | Playground souls/skills filtered by workspace membership | [#17](https://github.com/CarlosSalvador-vtex/agentserver/pull/17); baseline in `docs/playground-marketplace-v2-backlog.md` |
| OpenClaw plugin-sdk symlink (Tier 4 #16) | initContainer symlink + native `plugin-sdk` imports for cobrança skill | [#47](https://github.com/CarlosSalvador-vtex/agentserver/pull/47), [#33](https://github.com/CarlosSalvador-vtex/agentserver/pull/33); sandbox fix `56b7187` on `main` |
| Playground + Marketplace | Metrics, diff view, promote polling, soul dry-run, marketplace MVP, tenant catalog | [#6](https://github.com/CarlosSalvador-vtex/agentserver/pull/6)–[#9](https://github.com/CarlosSalvador-vtex/agentserver/pull/9), [#17](https://github.com/CarlosSalvador-vtex/agentserver/pull/17), [#18](https://github.com/CarlosSalvador-vtex/agentserver/pull/18); playground epic [#64](https://github.com/CarlosSalvador-vtex/agentserver/pull/64)–[#70](https://github.com/CarlosSalvador-vtex/agentserver/pull/70) |
| Publish draft (DB-only, no git PR) | `status='published'`; sandbox manager resolves workspace published draft before git system template; partial unique index per `(name, workspace_id)`; migration 043 | [#107](https://github.com/CarlosSalvador-vtex/agentserver/pull/107) |

**Related SaaS gaps in this doc (not the B01–B10 list):** BYOK and workspace quotas are largely in place (see “Estado atual”); Gap 1 webhook URL per workspace and Gap 6 onboarding wizard remain **open** in v1 below.

---

## Estado atual (o que já é multi-tenant)

O agentserver tem uma base sólida. Muita coisa já funciona por workspace:

| Componente | Status | Arquivo |
|---|---|---|
| `workspace_im_channels` — canais IM por workspace | ✅ existe | `migrations/011_workspace_im_channels.sql` |
| Roteamento de mensagem por `phone_number_id → workspace` | ✅ existe | `im_channels.go:173` `FindIMChannelByProviderBot` |
| Junction `sandbox_channel_bindings` (N:M) | ✅ existe | `migrations/031_multi_channel_routing.sql` |
| Estratégia de roteamento por workspace (`shared/per_agent/hybrid`) | ✅ existe | `handlers.go:1116` |
| LLM BYOK por workspace | ✅ existe | `db/llm_config.go`, `migrations/004_workspace_llm_config.sql` |
| Quotas por workspace/usuário | ✅ existe | admin handlers |
| Sandbox isolation por namespace k8s | ✅ existe | `sandbox/manager.go` |
| API de configuração WhatsApp por workspace | ✅ existe | `handlers.go:1282` `POST /api/workspaces/{id}/im/whatsapp/configure` |

**Conclusão:** a camada de dados já é multi-tenant. O problema está na camada de entrada (webhook) e em configurações globais no Helm que deveriam estar na DB.

---

## Gaps para SaaS real

### Gap 1 — `webhookVerifyToken` é global

**Situação atual:**

O Meta exige um `verify_token` para validar o webhook durante o cadastro da URL. Hoje há um único token para toda a instalação:

```
values.yaml:257  →  whatsapp.webhookVerifyToken
imbridge.yaml:64 →  env WHATSAPP_WEBHOOK_VERIFY_TOKEN
handlers.go:1212 →  os.Getenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN")
```

O endpoint de verificação:
```
GET /webhook/whatsapp?hub.verify_token=X&hub.challenge=Y
```

**Problema:** Todas as empresas compartilham o mesmo `verify_token`. Se uma empresa quiser usar uma URL de webhook diferente ou o token vazar, afeta todas.

**Opção A — URL de webhook por workspace (recomendada)**

Cada empresa registra sua própria URL no Meta:
```
GET /webhook/whatsapp/{workspace_id}?hub.verify_token=X&hub.challenge=Y
POST /webhook/whatsapp/{workspace_id}
```

O `verify_token` vira uma coluna em `workspace_im_channels` (ou tabela separada). O handler valida contra o token do workspace específico.

- **Prós:** Isolamento total. Se uma empresa tem token comprometido não afeta as outras. URL única por empresa.
- **Contras:** Empresas precisam reconfigurar a URL no Meta. Breaking change para instalações existentes.

**Opção B — URL global, token por workspace em query param**

```
GET /webhook/whatsapp?workspace={workspace_id}&hub.verify_token=X
```

O Meta não suporta bem parâmetros customizados na URL de verificação. **Inviável na prática.**

**Opção C — URL global, token global (manter como está)**

Funciona para um único tenant ou quando o operator controla todas as empresas. **Não escala para SaaS.**

**→ Recomendação: Opção A**

Mudanças necessárias:
- Migration: adicionar `verify_token TEXT` em `workspace_im_channels` (gerado no `configure`)
- `internal/imbridgesvc/server.go:56` — adicionar rota `GET /webhook/whatsapp/{workspace_id}`
- `internal/imbridgesvc/handlers.go:1212` — lookup do token por `workspace_id` na DB em vez de `os.Getenv`
- `handlers.go:1282` — gerar e persistir `verify_token` no `handleWorkspaceWhatsAppConfigure`
- Frontend: mostrar URL de webhook `https://{domain}/webhook/whatsapp/{workspace_id}` no dashboard

---

### Gap 2 — `appSecret` (HMAC) é global

**Situação atual:**

O `X-Hub-Signature-256` que a Meta envia usa um App Secret da Meta App. Hoje é global:

```
values.yaml       →  whatsapp.appSecret
imbridge.yaml     →  env WHATSAPP_APP_SECRET
handlers.go       →  os.Getenv("WHATSAPP_APP_SECRET")
```

**Problema:** Em SaaS, cada empresa pode ter sua própria Meta App (e portanto seu próprio `app_secret`). Um token único não valida webhooks de múltiplas Meta Apps.

**Opção A — `app_secret` por workspace (recomendada)**

Armazenar no `workspace_im_channels` junto com `bot_token`. O `handleWorkspaceWhatsAppConfigure` já recebe o token — adicionar campo `app_secret` no mesmo request.

**Opção B — Meta App compartilhada**

Todas as empresas usam a mesma Meta App gerenciada pelo operador da plataforma. Um único `app_secret` basta. Empresas não têm acesso direto à Meta App.

- **Prós:** Simples. Não requer mudança de código para multi-tenancy.
- **Contras:** Operador é responsável por compliance da Meta App. Limites da Meta App são compartilhados.

**Opção C — HMAC desativado por workspace**

Workspace sem `app_secret` configurado aceita webhook sem verificação HMAC (modo dev).

**→ Recomendação: Opção B para v1, Opção A para v2**

Meta App compartilhada é suficiente para o modelo SaaS controlado. Empresas não precisam ter conta de desenvolvedor na Meta — só fornecem o `phone_number_id` e `access_token`. A Meta App é do operador da plataforma.

Mudanças necessárias (v1):
- Nenhuma — manter `WHATSAPP_APP_SECRET` global.
- Documentar que a plataforma usa Meta App compartilhada.

Mudanças necessárias (v2, opcional):
- Migration: adicionar `app_secret TEXT` em `workspace_im_channels`
- `handlers.go` — lookup `app_secret` por workspace na verificação HMAC

---

### Gap 3 — `whatsappAllowedUsers` é global por plataforma

**Situação atual:**

Lista de números E.164 autorizados a falar com sandboxes OpenClaw/Hermes. Vem do Helm:

```
values.yaml:102  →  sandbox.openclaw.whatsappAllowedUsers: ["+55..."]
values.yaml:109  →  sandbox.hermes.whatsappAllowedUsers: ["+55..."]
deployment.yaml  →  env OPENCLAW_WHATSAPP_ALLOWED / HERMES_WHATSAPP_ALLOWED
sandbox/config.go:74  →  lido pelo sandbox manager
```

**Problema:** Cada empresa tem seus próprios usuários autorizados. Uma lista global não faz sentido em SaaS.

**Opção A — Coluna em `workspace_im_channels` (recomendada)**

```sql
ALTER TABLE workspace_im_channels
  ADD COLUMN allowed_users TEXT[] NOT NULL DEFAULT '{}';
```

`sandbox/manager.go` lê `allowed_users` do canal ao criar o sandbox, em vez de ler do env.

**Opção B — Remover a allowlist completamente**

A verificação de autorização já existe em outra camada (sandbox binding). A allowlist é uma proteção extra redundante em ambiente multi-tenant onde o workspace já isola.

**Opção C — Tabela separada `workspace_im_allowed_users`**

Permite gerenciamento via API REST. Mais flexível mas mais complexo.

**→ Recomendação: Opção B para curto prazo, Opção A se houver requisito de compliance**

Em SaaS, o workspace já é a barreira de isolamento. A allowlist por número é um controle extra que faz mais sentido quando o bot é público (qualquer número pode tentar falar). Se os bots são internos (empresa → funcionários), o workspace binding já garante isolamento.

Mudanças necessárias (Opção B):
- `sandbox/config.go:74` — remover `HermesWhatsappAllowed` e `OpenclawWhatsappAllowed`
- `sandbox/manager.go` — remover leitura do env e injeção no pod
- `deployment.yaml` — remover env vars `*_WHATSAPP_ALLOWED`
- `values.yaml` — remover `whatsappAllowedUsers` de openclaw e hermes

---

### Gap 4 — LLM keys globais sem fallback explícito

**Situação atual:**

BYOK já existe na DB (`workspace_llm_config`). Mas o fluxo de fallback não está documentado:

```
values.yaml:37  →  models.anthropicApiKey (global)
deployment.yaml →  env ANTHROPIC_API_KEY
llmproxy        →  usa BYOK se existe para o workspace, senão usa global
```

**Problema:** O comportamento de fallback (workspace sem BYOK → usa chave global da plataforma) não está explícito. Em SaaS, isso implica um modelo de negócio: a plataforma subsidia o LLM ou cobra por uso.

**Opção A — Fallback implícito (atual)**

llmproxy já faz: se workspace tem BYOK, usa; senão, usa chave global.

- **Prós:** Zero mudança de código.
- **Contras:** Plataforma paga pelo LLM de workspaces sem BYOK. Precisa de quota para controlar custo.

**Opção B — Sem fallback, BYOK obrigatório**

Workspace sem BYOK não consegue usar LLM. Retorna 402 ou mensagem clara.

- **Prós:** Sem custo inesperado para o operador.
- **Contras:** Fricção no onboarding.

**Opção C — Fallback com quota e billing (recomendada para produção)**

Plataforma tem créditos LLM. Workspace sem BYOK consome créditos (cobrado). Workspace com BYOK usa sua própria chave.

**→ Recomendação: Opção A para v1 (com quota), Opção C para monetização**

Quota por workspace (`workspaceMaxLLMTokens`) já existe parcialmente. Suficiente para controlar custo no curto prazo.

Mudanças necessárias (v1): nenhuma além de garantir quotas configuradas.

---

### Gap 5 — Subdomain de sandbox é global

**Situação atual:**

Sandboxes são acessados por subdomínio:
```
claw-{sandboxID}.agentserver.vtex.com
hermes-{sandboxID}.agentserver.vtex.com
```

`sandboxproxy/server.go:83` extrai o prefixo e roteia para o pod correto.

**Problema:** Em SaaS, não há necessidade de subdomínio por empresa — o sandbox já pertence a um workspace. O subdomínio é técnico (por sandbox), não por empresa.

**Opção A — Manter subdomínio por sandbox (atual, recomendada)**

`claw-{id}.agentserver.vtex.com` já isola por sandbox. O `workspace_id` é verificado no servidor ao rotear — um usuário não pode acessar sandbox de outro workspace mesmo conhecendo o ID.

**Opção B — Subdomínio por empresa**

`empresa-a.agentserver.vtex.com/claw-{id}` — requer wildcard de segundo nível, infra adicional.

**→ Recomendação: Opção A — sem mudança**

Subdomínio por empresa é white-label, não SaaS básico.

---

### Gap 6 — Onboarding de empresa não existe (UI/UX)

**Situação atual:**

A API de configuração existe (`POST /api/workspaces/{id}/im/whatsapp/configure`) mas o fluxo de onboarding não está completo:

1. Admin cria workspace ✅
2. Admin configura WhatsApp (cola token + phone_number_id) — **existe na API, sem UI completa**
3. Meta valida webhook (verify_token) — **quebra em SaaS (Gap 1)**
4. Sandbox é criado e vinculado ao canal — **existe**

**→ Recomendação:** Após resolver Gap 1 (URL por workspace), construir wizard de onboarding no frontend:

```
Passo 1: Nome da empresa (workspace)
Passo 2: Configurar canal IM (WhatsApp: phone_number_id + access_token)
Passo 3: Copiar URL de webhook → instruções para configurar na Meta
Passo 4: Testar conexão (enviar mensagem de teste)
```

---

## Resumo de prioridades

### v1 — Mudanças para SaaS funcional (impacto alto, custo baixo)

| # | Item | Gap | Mudança | Arquivos | Status |
|---|---|---|---|---|
| 1 | URL webhook por workspace | Gap 1 | Novo endpoint `GET/POST /webhook/whatsapp/{workspace_id}` + coluna `verify_token` | `imbridgesvc/server.go:56`, `handlers.go:1212`, `migrations/` | Open |
| 2 | Remover `whatsappAllowedUsers` global | Gap 3 | Deletar config do helm + sandbox manager | `sandbox/config.go:74`, `deployment.yaml:189` | Open |
| 3 | Garantir quotas LLM configuradas | Gap 4 | Sem código — só operação | `values-dev-eks.yaml` | Ops (BYOK/quotas exist — see Estado atual) |

### v2 — Hardening e monetização

| # | Item | Gap | Mudança |
|---|---|---|---|
| 4 | `app_secret` por workspace (HMAC) | Gap 2 | Coluna em `workspace_im_channels` + handler update |
| 5 | BYOK obrigatório ou billing | Gap 4 | 402 handler ou integração billing |
| 6 | Wizard de onboarding | Gap 6 | Frontend React multi-step |

### v3 — White-label (opcional)

| # | Item |
|---|---|
| 7 | Subdomínio customizado por empresa |
| 8 | Branding por workspace (logo, cores) |
| 9 | Meta App por empresa (HMAC isolado) |

---

## Diagrama: fluxo de mensagem pós-v1

```
Empresa A configura:
  POST /api/workspaces/ws-a/im/whatsapp/configure
  → cria workspace_im_channels(workspace_id=ws-a, bot_id=+55..., verify_token=abc123)
  → UI mostra: configure na Meta → URL: https://agentserver.vtex.com/webhook/whatsapp/ws-a

Meta valida:
  GET /webhook/whatsapp/ws-a?hub.verify_token=abc123&hub.challenge=XYZ
  → handler lookup verify_token onde workspace_id=ws-a
  → responde hub.challenge

Meta envia mensagem:
  POST /webhook/whatsapp/ws-a
  → lookup channel por (workspace_id=ws-a, phone_number_id=+55...)
  → dispatch → sandbox da empresa A
  → empresa B nunca vê esta mensagem
```

---

## Arquivos-âncora para implementação

| Arquivo | Relevância |
|---|---|
| `internal/imbridgesvc/server.go:56` | Registro das rotas de webhook |
| `internal/imbridgesvc/handlers.go:1212` | Leitura do verify_token (mudar de env para DB) |
| `internal/imbridgesvc/handlers.go:1282` | Configure endpoint (adicionar geração de verify_token) |
| `internal/imbridgesvc/handlers.go:1383` | Handler de inbound (adicionar `workspace_id` na rota) |
| `internal/db/im_channels.go:173` | `FindIMChannelByProviderBot` (adicionar filtro por workspace_id) |
| `internal/db/migrations/011_workspace_im_channels.sql` | Schema base (adicionar coluna `verify_token`) |
| `internal/sandbox/config.go:74` | Remover `whatsappAllowed*` |
| `deploy/helm/agentserver/templates/deployment.yaml:189` | Remover env vars de allowlist |
