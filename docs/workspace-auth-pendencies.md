# Workspace Auth — Pendências pós PR #60

**Última atualização:** 2026-05-27
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
| F1 | PR #56 (docs/workspace-auth-design.md) | OPEN — só docs do design. Merge | 5 min |
| F2 | Atualizar status do design doc | mudar Opção A de "design only" para "implementado em PR #57+#58" + link pra smoke report | 10 min |
| F3 | Cleanup branch `chore/bump-image-auth-session` | branch + remote (não foi mergeada) | 2 min |
| F4 | Cleanup workspaces de teste | `empresa-custom-teste`, `auto-derive-me` no DEV — sem DELETE endpoint, fica via SQL ou Helm reset | 5 min via SQL |
| F5 | Cleanup user `tester-empresa-custom@example.com` | mesma situação | SQL |
| F6 | OIDC subdomain stamp validation | PR #57 tem código no callback (`internal/auth/*`). DEV não tem provider OIDC configurado pra testar end-to-end | requer setup IdP |

---

## 🟢 Backlog opcional (multi-tenancy nível 2)

| # | Item | Doc origem | Esforço |
|---|---|---|---|
| B1 | Endpoint invite por email (Cenário B do smoke) | "fluxo `POST /api/workspaces/{wid}/invites` + email link + accept-invite UI" | ~200 LOC |
| B2 | Endpoint DELETE workspace | sem isso só dá pra remover via SQL | ~50 LOC |
| B3 | Endpoint DELETE user (admin only) | mesmo | ~50 LOC |
| B4 | Opção B — SSO por workspace (Google/Okta/Azure) | [workspace-auth-design.md](workspace-auth-design.md) | ~600 LOC, 3 sprints |
| B5 | Opção C — híbrido SSO + senha local | depende de B4 | ~700 LOC, 4 sprints |
| B6 | URLs com subdomínio do workspace destinatário em emails | links de reset password, invites etc. | ~30 LOC |
| B7 | Audit log por workspace na camada de sessão (já tem o hook do active_workspace_id, falta integrar nos handlers) | reaproveitar tabela `draft_audit_events` PR #43 ou criar `session_audit_log` | ~150 LOC |
| B8 | Codex-auth cross-subdomain SSO vs cookie host-only | conflict documentado no plano cursor; decisão pendente | design + ~100 LOC |
| B9 | "Choose a workspace" UI no apex | fallback alternativo ao PR #53 picker — botão "Switch" leva pra subdomínio do workspace | ~80 LOC |
| B10 | Reservar mais slugs (`mail`, `support`, `status`, `help`, `docs`, etc.) | hoje só `www, api, admin, app, root, auth, login, register, static, assets, agentserver, openclaw, hermes` | trivial |

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
Hoje:
  F1 → F2 → F3 (limpeza rápida, 20 min total)

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

- [ ] PR #56 mergeado
- [ ] Design doc com status "implementado"
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
