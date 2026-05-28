# Cursor Handoff — B08: codex-auth cross-subdomain vs cookie host-only

**Backlog:** B08 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** 0-200 (depende da decisão A/B/C)
**Tempo:** 1 sprint (incluindo design review)
**Tipo:** Decisão arquitetural — **NÃO** começar a implementar antes de aprovação humana

## Status

- **State:** OPEN — decision only (do not implement until human approves path A/B/C)
- **Dependencies:** none (blocks scaling B04 SSO if wrong choice)
- **Estimated PR size:** 0–200 LOC depending on chosen path

> **Update (2026-05-28):** The OpenAI **Codex** integration is out of scope for this
> product (see `docs/ops/codex-not-used.md`). The codex-auth "1 SSO token for all
> tenants" pressure that motivated half this conflict is gone. B08 now collapses
> toward **Path A (host-only, status quo)** as the safe default. **Path C
> (single-use redirect token)** is still worth doing for human multi-workspace UX,
> but no longer blocks any Codex requirement.

## Goal

Resolver o conflito entre:
- **Cookie host-only** (PR #57) — sessão de tenant fica isolada por subdomínio. Necessário pra evitar XSS cross-tenant.
- **codex-auth SSO** — espera "sign in once, access all your tenants". Precisa cookie compartilhado entre subdomínios.

## Why agora (ou não)

Não bloqueia produção. Mas:
- Usuários multi-workspace precisam fazer login N vezes (1 por subdomínio)
- codex-auth (CLI/IDE plugin) hoje provavelmente quebrado em tenants — verificar
- Decisão arquitetural — fechar antes de B04 (SSO) escalar

## Required reading

- [`../plans/cursor_workspace-subdomain-auth.md`](../plans/cursor_workspace-subdomain-auth.md) §2.6 — conflict doc
- [`../workspace-session-auth.md`](../workspace-session-auth.md) — cookie scope decisions
- `internal/auth/auth.go::SetTokenCookieHostOnly` vs `SetTokenCookie` (Domain attr)
- `internal/server/codex-auth/` (se existir — verificar paths)

## Cenário problemático

```
1. User loga em empresa-a.<base> (cookie A, host-only)
2. Tenta abrir empresa-b.<base> noutra aba → cookie A não vale lá → precisa relogar
3. codex-auth CLI quer "1 SSO token serve todos meus tenants" → contradição
```

## 3 caminhos possíveis

### Caminho A — Manter host-only puro (status quo)

- Multi-workspace user faz login por tenant
- codex-auth também re-loga por tenant (CLI fica chato)
- **0 LOC** — só formalizar como decisão
- Mais seguro

### Caminho B — Cookie compartilhado `Domain=.<base>`

```go
http.SetCookie(w, &http.Cookie{
    Name:     "agentserver-token",
    Value:    token,
    Domain:   "." + baseDomain,  // ! cross-subdomain
    Path:     "/",
    Secure:   true,
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
})
```

- 1 login serve todos os tenants do usuário
- XSS em qualquer subdomínio vaza pra todos
- ~30 LOC mudança em `SetTokenCookie*`
- **Pior segurança**

### Caminho C — Cookie host-only + "linked accounts" via short-lived redirect token

```
1. User loga em empresa-a.<base> (cookie A host-only)
2. Acessa empresa-b.<base> via menu/link
3. Frontend chama POST /api/auth/cross-tenant-redirect { target_slug: 'empresa-b' }
4. Backend valida user é membro de empresa-b
5. Backend gera 30s-TTL redirect token (signed, single-use)
6. Frontend redireciona pra https://empresa-b.<base>/api/auth/redirect-login?rt=<token>
7. empresa-b consumir token → cria session local + set cookie host-only
```

- Token nunca em URL persistente (só na redirect uma vez)
- Single-use evita replay
- ~150-200 LOC backend + frontend
- **Boa segurança + UX**

## Comparação

| Critério | A (status quo) | B (shared cookie) | C (redirect token) |
|---|---|---|---|
| Login N vezes? | Sim | Não | Não (1 clique entre tenants) |
| XSS blast radius | 1 tenant | TODOS tenants do user | 1 tenant |
| Cookie attrs | host-only | Domain=.base | host-only |
| Token em URL | Não | Não | Sim (30s TTL) |
| codex-auth amigável | Não (re-login) | Sim | Sim (CLI faz redirect dance) |
| LOC | 0 | ~30 | ~200 |
| Riscos | UX cansativa | Vazamento total se 1 tenant comprometido | Replay se TTL muito longo |
| Padrão indústria | Google Workspace (1 tenant = 1 acct) | Slack/Discord | Auth0/WorkOS |

## Recomendação inicial

**Caminho C (redirect token)** — equilibra segurança e UX.

Mas exige aprovação porque:
1. Mexer em fluxo de auth tem impacto sistêmico
2. Frontend tem mudança visível (botão "switch tenant")
3. CLI codex-auth precisa entender o dance
4. SSO Opção B (B04) deve usar mesma mecânica

## Required design review

Antes de implementar, gerar:

1. ADR (Architecture Decision Record): `docs/adr/0001-cross-tenant-session.md`
2. Sequence diagram pra Caminho C
3. Threat model: replay, race, CSRF
4. Estimativa LOC final
5. Aprovação humana

## Implementation (se Caminho C aprovado)

Skeleton:

```go
// internal/auth/cross_tenant.go
type RedirectToken struct {
    UserID       string
    TargetWSID   string
    ExpiresAt    time.Time
    Nonce        string  // single-use marker
}

func (a *Auth) IssueCrossTenantRedirect(callerToken, targetSlug string) (string, error) {
    // 1. Valida caller token
    userID, _, ok := a.ValidateTokenWithWorkspace(callerToken)
    if !ok { return "", ErrUnauthorized }

    // 2. Resolve target workspace + check membership
    ws, _ := a.db.GetWorkspaceBySlug(targetSlug)
    member, _ := a.db.IsWorkspaceMember(ws.ID, userID)
    if !member { return "", ErrNotMember }

    // 3. Gera nonce + signed JWT (HMAC com secret rotacionável)
    nonce, _ := secrets.RandomHex(16)
    claims := RedirectToken{ UserID: userID, TargetWSID: ws.ID, ExpiresAt: time.Now().Add(30*time.Second), Nonce: nonce }
    signed := signClaims(claims, a.crossTenantSecret)

    // 4. Guarda nonce em set (single-use) — Redis ou DB
    a.db.MarkNonceIssued(nonce, claims.ExpiresAt)

    return signed, nil
}

func (a *Auth) ConsumeCrossTenantRedirect(signedToken string) (sessionToken string, err error) {
    claims, err := verifyClaims(signedToken, a.crossTenantSecret)
    if err != nil { return "", err }
    if time.Now().After(claims.ExpiresAt) { return "", ErrExpired }

    // single-use check
    consumed, _ := a.db.ConsumeNonce(claims.Nonce)
    if !consumed { return "", ErrAlreadyUsed }

    // Cria session local com active_workspace_id stampado
    token, _ := a.IssueToken(claims.UserID)
    _, _ = a.SetActiveWorkspace(token, claims.UserID, claims.TargetWSID)
    return token, nil
}
```

```go
// internal/server/handlers_cross_tenant.go
r.Post("/api/auth/cross-tenant-redirect", s.handleCrossTenantRedirect)  // emite
r.Get("/api/auth/redirect-login", s.handleRedirectLogin)                  // consome
```

Frontend: hook `useCrossTenantSwitch(targetSlug)` que faz o dance.

## Acceptance criteria (se C escolhido)

- [ ] ADR aprovado por humano
- [ ] Token assinado com HMAC; secret via Sealed Secret + rotação documentada
- [ ] TTL ≤ 30s
- [ ] Single-use enforced via DB ou Redis
- [ ] Replay rejected (409 Conflict)
- [ ] Cross-tenant requires existing valid session no source
- [ ] Target workspace membership validado server-side
- [ ] Audit log entry pra cada cross-tenant switch
- [ ] UI: dropdown "Switch workspace" no header (ou Cmd+K palette)
- [ ] CLI codex-auth atualizado se aplicável

## Anti-patterns

- ❌ TTL longo (> 1 min) → replay window grande
- ❌ Token em URL persistente (history, logs) — só 30s redirect
- ❌ Skip de single-use enforcement
- ❌ Permitir issue sem session válida no source
- ❌ Reusar nonce após expiração

## Out of scope

- "Remember linked tenants" UX (sem re-login depois) — virou cookie compartilhado de facto (Caminho B disfarçado, rejeitar)
- Cross-tenant API calls (não precisa — sandbox isolation é por workspace)

## Definition of done

- ADR mergeado primeiro
- Se Caminho C: PR mergeado + tests + threat model + DEV smoke
- Se Caminho A: doc formaliza decisão e fecha B08 sem código
