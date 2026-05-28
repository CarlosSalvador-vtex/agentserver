# Workspace Auth — Pendências pós PR #60

**Última atualização:** 2026-05-28
**Contexto:** após merge dos PRs #57 (subdomain auth), #58 (sandboxproxy fallback), #59 + #60 (smoke docs).
**Estado DEV:** Opção A do design totalmente operacional.

Documento de tracking. Para detalhes de cada item ver os docs linkados.

---

## Quick-reference de docs relacionados

| Doc | Conteúdo |
|---|---|
| [workspace-auth-design.md](workspace-auth-design.md) | Design A/B/C (escolheu A) |
| [workspace-session-auth.md](workspace-session-auth.md) | PR #53 — `active_workspace_id` na session |
| [pr-57-workspace-subdomain-auth-status.md](pr-57-workspace-subdomain-auth-status.md) | Status pré-merge do #57 |
| [pr-57-pr-58-e2e-smoke-2026-05-27.md](pr-57-pr-58-e2e-smoke-2026-05-27.md) | Smoke E2E |
| [plans/2026-05-27-workspace-subdomain-auth.md](plans/2026-05-27-workspace-subdomain-auth.md) | Plano TDD (superseded) |
| [plans/cursor_workspace-subdomain-auth.md](plans/cursor_workspace-subdomain-auth.md) | Plano canônico |
| [saas-multitenancy-roadmap.md](saas-multitenancy-roadmap.md) | Roadmap multi-tenancy geral |

---

## 🔴 Bloqueadores pra PROD

| # | Item | Detalhe | Esforço |
|---|---|---|---|
| P1 | Promoção CI/CD dev → staging → prod | imagem `agentserver:auth-slug` + `sandboxproxy:tenant-fallback` foi build manual. Pipeline auto não roda em branches; só `main`. Precisa rebuild via CI após merges | 1 sprint (#15 staging cluster já existe) |
| P2 | Wildcard DNS + cert ACM em PROD | DEV tem `*.agentserver.analytics.vtex.com`. Prod precisa wildcard equivalente + cert ACM renovável | infra |
| P3 | Cookie scope final em PROD | confirmar `SameSite`, `Secure`, `HttpOnly`, sem `Domain` attr em hosts tenant | revisão handlers + smoke |
| P4 | Smoke staging antes prod | reproduzir o checklist do [smoke E2E](pr-57-pr-58-e2e-smoke-2026-05-27.md) no staging | 30 min |

---

## 🟡 Funcionais (curto prazo)

| # | Item | Detalhe | Esforço |
|---|---|---|---|
| F4 | Cleanup workspaces de teste | `empresa-custom-teste`, `auto-derive-me` no DEV — sem DELETE endpoint. **SQL cleanup — pending** (não resolvido) | 5 min via SQL |
| F5 | Cleanup user `tester-empresa-custom@example.com` | mesma situação. **SQL cleanup — pending** (não resolvido) | SQL |
| F6 | OIDC subdomain stamp validation | PR #57 tem código no callback (`internal/auth/*`). DEV não tem provider OIDC configurado pra testar end-to-end — **depende de setup IdP** | requer setup IdP |

### Resolved (arquivado)

Itens concluídos; removidos da fila ativa acima.

| # | Item | Resolução |
|---|---|---|
| F1 ✅ | PR #56 (`docs/workspace-auth-design.md`) | **Resolvido** — mergeado (PR #56) |
| F2 ✅ | Atualizar status do design doc | **Resolvido** — Opção A marcada como implementada em PR #57+#58; smoke em [pr-57-pr-58-e2e-smoke-2026-05-27.md](pr-57-pr-58-e2e-smoke-2026-05-27.md) (pós PR #57+#58) |
| F3 ✅ | Cleanup branch `chore/bump-image-auth-session` | **Resolvido** — branch e remote removidos (cleanup concluído) |

---

## 🟢 Backlog opcional (multi-tenancy nível 2)

### B1 — Endpoint invite por email (Cenário B do smoke)

**O que:** fluxo de onboarding self-service via convite.

**Por que:** hoje admin precisa adicionar membro com `POST /api/workspaces/{wid}/members` passando email — mas o user precisa **já existir** no banco. Sem isso, admin precisa fazer signup do user (vide segurança), ou chamar suporte. Em SaaS B2B, o padrão é admin gerar link de convite, mandar por email, user clica e cria conta + entra no workspace.

**Componentes:**

```
Migration 041:
  workspace_invites (id, workspace_id, email, token, role, expires_at, accepted_at, created_by, created_at)
  UNIQUE(workspace_id, email) WHERE accepted_at IS NULL

Backend:
  POST /api/workspaces/{wid}/invites  — admin gera invite (token random)
    body: { email, role }
    response: { invite_url: "https://{slug}.<base>/accept-invite?token=..." }
  GET  /api/auth/invite/{token}       — UI lê info do convite antes do user aceitar
    response: { workspace_name, workspace_slug, role, email }
  POST /api/auth/invite/{token}/accept — user cria conta + vira membro atomicamente
    body: { password }   # se já existe user com esse email, valida senha
    response: { status: "ok" } + Set-Cookie

Frontend:
  Workspace settings → "Invite member" modal (gerar link)
  /accept-invite?token=... — landing com signup form pré-preenchido
  Email enviado via novo job `internal/notif/invite_mailer.go` (SES ou similar)
```

**Dependências:** B2 (mailer config), B6 (URLs em subdomínio do tenant).
**LOC estimado:** ~200 (migration + 3 handlers + UI + mailer wiring).
**Decisão pendente:** SES vs Resend vs Mailgun pro outbound email. Custo + bounce handling.

---

### B2 — Endpoint DELETE workspace

**O que:** `DELETE /api/workspaces/{id}` que limpa workspace + membros + sandboxes + skill drafts + audit logs em cascata.

**Por que:** hoje sem endpoint, cleanup é SQL manual no DB de prod. Errar é facílimo. Workspaces órfãos acumulam.

**Implementação:**

```go
// internal/server/server.go
r.Delete("/api/workspaces/{id}", s.handleDeleteWorkspace)

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
    wsID := chi.URLParam(r, "id")
    if !s.requireWorkspaceRole(w, r, wsID, "owner") { return }
    // 1. Stop all running sandboxes (cleanup CRDs)
    // 2. Soft-delete em workspaces (workspaces.deleted_at = NOW())
    // 3. Cascade: token sessions com active_workspace_id viram NULL (já tem FK ON DELETE SET NULL)
    // 4. Audit entry
}
```

**Decisão pendente:** soft delete (default) ou hard delete (`?purge=true`)? Soft deixa rastros, hard quebra audits históricos.

**LOC estimado:** ~80 (handler + cascata + UI confirmation modal).

---

### B3 — Endpoint DELETE user (admin only)

**O que:** `DELETE /api/users/{id}` — global admin remove conta.

**Por que:** GDPR/LGPD direito ao esquecimento. Hoje só SQL.

**Cuidados:**
- User que é último `owner` de algum workspace → forçar transfer ou rejeitar
- Anonimizar (não deletar) se há audit logs referenciando — mudar email pra `deleted-<id>@anonymized.local`, mas manter id pra integridade referencial
- Tokens ativos são invalidados em cascata (FK)

**LOC estimado:** ~120 (handler + ownership-transfer logic + anonymization).
**Dependências:** B7 (audit) pra preservar history corretamente.

---

### B4 — Opção B: SSO por workspace (Google/Okta/Azure SAML+OIDC)

**O que:** cada workspace configura seu próprio IdP. User com email `@empresa-a.com` redirected pro SSO da Empresa A.

**Por que:** compliance enterprise (SOC2, ISO 27001) exige BYO-IdP. Empresas grandes não compartilham IdP com terceiros. Sem isso, agentserver é não-vendável para enterprise.

**Componentes:**

```
Migration 042:
  workspace_sso_configs (workspace_id, provider, issuer_url, client_id,
                          client_secret_ref, allowed_email_domains[],
                          enabled, created_at, updated_at)
  workspace_sso_audit_log (workspace_id, user_email, event, ip, error, at)

Backend (~400 LOC):
  GET  /api/auth/sso/discover?email=...    — resolve workspace por email domain
  GET  /api/auth/sso/initiate?wid=...      — redirect pro IdP (OAuth/SAML)
  GET  /api/auth/sso/callback              — OAuth callback handler
  POST /api/workspaces/{wid}/sso           — admin config
  GET  /api/workspaces/{wid}/sso           — read config
  DELETE /api/workspaces/{wid}/sso         — disable
  POST /api/workspaces/{wid}/sso/test      — admin testa conexão antes salvar

Frontend (~200 LOC):
  /workspace/{wid}/settings/sso  — UI de config (Google Workspace, Okta, Azure AD presets)
  /login flow modificado          — pede email → resolve SSO → redirect ou senha
```

**Secret management:** `client_secret` via Sealed Secrets (PR #51) ou external secret. Nunca em plaintext no DB.

**Dependências:** PR #51 (Sealed Secrets infra) já está em prod-ready.
**LOC estimado:** ~600 total.
**Sprint:** 3 sprints (1 design + 2 implementação + 1 audit).

---

### B5 — Opção C: híbrido SSO + senha local

**O que:** A (subdomain) + B (SSO) + fallback senha pra emergências.

**Por que:** workspace tem SSO configurado mas admin precisa logar quando IdP cai (break-glass). Também: usuários convidados que ainda não têm conta no IdP do workspace.

**Fluxo:**

```
{slug}.<base>/login →
  workspace.sso_enabled?
    sim → tenta SSO
            └─ fail/timeout → fallback se admin habilitou "allow_password_fallback"
                                └─ se sim, mostra form senha
                                └─ se não, mostra erro "contact your admin"
    não → form de senha
```

**Decisão pendente:** habilitar password fallback é por workspace (toggle no settings) ou por user (admin marca "break-glass user")?

**Dependências:** B4 completo.
**LOC estimado:** ~150 adicional sobre B4 (~750 total).

---

### B6 — URLs com subdomínio do workspace em emails

**O que:** invites, reset password, notifications usam `<slug>.<base>` em vez de apex.

**Por que:** user clica link e cai já no tenant certo, sem precisar escolher workspace depois. Branding consistente.

**Mudança:**

```go
// hoje:
inviteURL := fmt.Sprintf("https://%s/accept-invite?token=%s", baseDomain, token)

// depois:
inviteURL := fmt.Sprintf("https://%s.%s/accept-invite?token=%s", workspace.Slug, baseDomain, token)
```

**Lugares afetados:**
- `internal/notif/invite_mailer.go` (a criar — vide B1)
- `internal/auth/password_reset.go` (se houver — verificar)
- `internal/notif/notification_mailer.go` (sandbox alerts etc — verificar)

**LOC estimado:** ~30 distribuídos.
**Dependências:** B1 + qualquer outro mailer existente.

---

### B7 — Audit log por workspace na camada de sessão

**O que:** registrar quem fez o quê em qual workspace, com `active_workspace_id` injetado automaticamente.

**Por que:**
- Compliance (SOC2 controle 7.3 "monitoring of access")
- Debugging — "por que esse usuário deletou esse sandbox?"
- Detection — "alguém tentou bind workspace que não é membro 50 vezes em 1h"

**Componentes:**

```
Reuso: tabela draft_audit_events do PR #43 já tem schema parecido.
  Ou nova: session_audit_events (id, user_id, active_workspace_id,
           event_type, path, method, status, ip, ua, error, at)

Middleware (~50 LOC):
  Após Auth.Middleware, registrar entrada com (userID, activeWorkspaceID, request_summary).
  Async via channel + worker pra não bloquear request path.

Eventos prioritários:
  login.success, login.failure, login.workspace_set
  workspace.member_added, workspace.member_removed
  workspace.sso_config_changed
  sandbox.created, sandbox.deleted (já tem em outro lugar — consolidar)

Retention: 90 dias default (igual playground audit do PR #43).
```

**Dependências:** decidir reuso vs separação com `draft_audit_events`.
**LOC estimado:** ~150.

---

### B8 — Codex-auth cross-subdomain SSO vs cookie host-only

**O que:** resolver conflito entre cookie host-only no tenant (que NÃO atravessa subdomínios) e codex-auth (que precisa atravessar pra SSO entre tenants).

**Conflito documentado** em [plans/cursor_workspace-subdomain-auth.md](plans/cursor_workspace-subdomain-auth.md) §2.6:

> "codex-auth cross-subdomain SSO conflita com cookies host-only no tenant"

**Cenário problemático:**
1. User loga em `empresa-a.<base>` (cookie host-only)
2. Tenta abrir `empresa-b.<base>` numa aba — cookie A não vale → precisa relogar
3. Codex-auth quer "sign in once, access all your tenants" → contradição

**3 caminhos:**

| Caminho | Trade-off |
|---|---|
| A: Manter host-only — codex-auth re-login por tenant | Mais seguro, UX pior |
| B: Cookie compartilhado com `Domain=.<base>` | UX melhor, blast radius se vazar |
| C: Cookie host-only + token de "linked accounts" no localStorage | Complexidade alta, UX intermediária |

**LOC estimado:** depende — A é 0 LOC, B é ~30, C é ~200.
**Decisão pendente:** priorizar segurança (A) ou UX (B/C).

---

### B9 — "Choose a workspace" UI no apex

**O que:** quando user loga no `agentserver.<base>` (apex) sem subdomínio, mostrar picker de workspaces e redirecionar pro subdomínio do escolhido.

**Por que:** hoje apex login deixa `active_workspace_id=NULL` e a UI vai pro flow PR #53 (selecionar workspace via API). Subdomínio é mais consistente.

**Fluxo:**

```
1. apex/login → submit credenciais
2. backend: cria session, active_workspace_id=NULL
3. apex/ (raiz) → frontend detecta NULL → busca /api/workspaces
4. Mostra lista: "You belong to N workspaces. Pick one:"
   - empresa-a (developer)  → [Open] → https://empresa-a.<base>/
   - empresa-b (owner)      → [Open] → https://empresa-b.<base>/
5. Click → redirect pro subdomínio + auto-login (passar token via redirect)
```

**Cuidado:** token via URL é inseguro (vai pra logs). Alternativa: short-lived signed redirect token (~30s TTL) que é trocado pelo cookie do subdomínio.

**LOC estimado:** ~80 backend (redirect token) + ~50 frontend.
**Dependências:** decisão sobre redirect token security model.

---

### B10 — Reservar mais slugs

**O que:** ampliar lista de reserved slugs além dos atuais.

**Atual** (`internal/db/slug.go`):
```
www, api, admin, app, root, auth, login, register,
static, assets, agentserver, openclaw, hermes
```

**Sugestões adicionais** (operacional comum em SaaS):

| Categoria | Slugs |
|---|---|
| Email | `mail`, `email`, `mailbox`, `mx`, `smtp`, `imap`, `pop` |
| Suporte | `support`, `help`, `helpdesk`, `kb`, `faq`, `contact` |
| Status/monitoring | `status`, `health`, `metrics`, `dashboard`, `grafana`, `prometheus` |
| Docs | `docs`, `documentation`, `wiki`, `blog`, `news` |
| Infra | `cdn`, `proxy`, `ingress`, `lb`, `node`, `pod`, `k8s` |
| Comercial | `billing`, `pay`, `payments`, `pricing`, `enterprise`, `sales` |
| Marketing | `signup`, `trial`, `demo`, `marketing`, `landing` |
| Compliance/legal | `legal`, `terms`, `privacy`, `tos`, `gdpr`, `lgpd`, `compliance` |
| Resources | `cdn`, `media`, `images`, `files`, `download`, `upload` |
| Hosts especiais | `localhost`, `internal`, `external`, `public`, `private` |

**Trade-off:** lista grande aumenta segurança contra confusion-attack mas pode rejeitar nomes legítimos. Considerar opt-out para grandes contas.

**LOC estimado:** trivial (~5 LOC, append no map).
**Decisão pendente:** lista final + processo pra adicionar novos no futuro.

---

## ⚠️ Riscos / Decisões pendentes

| # | Risco | Mitigação proposta |
|---|---|---|
| R1 | Squatting de slug de empresas conhecidas | implementar lista de reserved corporate names ou aprovação manual via admin |
| R2 | Multi-workspace user precisa relogar a cada subdomínio | aceito como expected B2B (Slack faz igual). Considerar SSO (Opção B) se virar atrito |
| R3 | Sandbox subdomain (`claw-*`, `hermes-*`) colidir com slug | validador atual já bloqueia. Manter sincronizado se prefixos mudarem |
| R4 | Register habilitado em subdomínio = ataque cross-tenant | hoje register usa apex (correto). Documentar e nunca habilitar register direto em `{slug}.<base>/register` |
| R5 | Cookie cross-subdomínio leak por engano | revisar `SetTokenCookieHostOnly` em prod antes do roll-out |

---

## Ordem sugerida

```
Próxima sprint (S6):
  P1 staging CI/CD
  P4 smoke staging
  B1 invite por email (entrega visível)
  F6 OIDC subdomain (se houver provider)

Sprint+1:
  P2 + P3 PROD rollout
  B7 audit log
  B9 "choose a workspace" no apex

Backlog:
  B4 Opção B SSO (quando compliance pedir)
  B5 Opção C híbrido
```

---

## Checklist consolidado pré-PROD

- [x] PR #56 mergeado
- [x] Design doc com status "implementado"
- [ ] CI/CD publica image `auth-slug` (ou tag canônica) automaticamente
- [ ] Staging cluster com smoke verde
- [ ] Wildcard DNS + cert ACM em PROD
- [ ] Cookie attrs (Secure, HttpOnly, SameSite=Lax) revisados
- [ ] Cookie sem `Domain` attr em tenant subdomain — confirmado
- [ ] Audit log de login por workspace (B7) ou aceito risco
- [ ] Endpoint DELETE workspace (B2) ou processo de cleanup definido
- [ ] Reserved slugs ampliada (B10)
- [ ] Doc operacional `docs/workspace-auth.md` revisado por ops/SRE
- [ ] Runbook: "como criar tenant pra cliente novo" — passo a passo
- [ ] Runbook: "como rotacionar credenciais de tenant"
