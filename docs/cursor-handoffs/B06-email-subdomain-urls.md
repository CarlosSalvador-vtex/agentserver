# Cursor Handoff — B06: URLs com subdomínio em emails

**Backlog:** B06 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~30
**Tempo:** 1-2h
**Dependências:** B01 (mailer infra) ou outro mailer existente

## Status

- **State:** OPEN — unblocked
- **Dependencies:** B01 invite/mailer infra — shipped (PR #71)
- **Estimated PR size:** S (~30 LOC)

## Goal

Todos os links em emails (invites, reset password, notifications) usam `<slug>.<base>` em vez de `<base>` apex. User clica e cai no tenant certo, sem precisar escolher workspace.

## Why now

- UX consistente: usuário visualiza branding correto no link
- Reduz fricção: 1 clique até o tenant, sem picker intermediário
- Necessário para invites (B01) — link `agentserver.com/accept-invite?token=...` é genérico; `empresa-a.agentserver.com/accept-invite?...` é específico

## Required reading

- [`B01-invite-email.md`](B01-invite-email.md) — usa esse padrão
- `docs/workspace-auth.md` — base domain config
- `internal/notif/` (se existir após B01) — mailers

## Files to touch

| Path | Mudança |
|---|---|
| `internal/notif/mailer.go` | helper `BuildTenantURL(slug, path) string` |
| `internal/notif/invite_mailer.go` (vide B01) | usar helper |
| `internal/auth/password_reset.go` (se houver) | mesmo |
| Qualquer outro mailer existente | inspecionar via `grep -rn "agentserver.com\|baseDomain" internal/` |

## Implementation

### Helper centralizado

```go
// internal/notif/url.go
package notif

import "fmt"

// BuildTenantURL constructs a workspace-scoped URL.
// slug ex: "empresa-a"; baseDomain ex: "agentserver.analytics.vtex.com".
func BuildTenantURL(slug, baseDomain, path string) string {
    if slug == "" {
        return fmt.Sprintf("https://%s%s", baseDomain, path)
    }
    return fmt.Sprintf("https://%s.%s%s", slug, baseDomain, path)
}
```

### Usar em mailers

Antes:
```go
inviteURL := fmt.Sprintf("https://%s/accept-invite?token=%s", baseDomain, token)
```

Depois:
```go
inviteURL := BuildTenantURL(workspace.Slug, baseDomain, "/accept-invite?token="+token)
```

### Audit nas referências existentes

```bash
grep -rn "agentserver.com\|s.BaseDomain\|baseDomain" internal/ \
  | grep -iE "url|link|href" | head -20
```

Cada hit: avaliar se faz sentido usar subdomínio do tenant.

Casos esperados:
- ✅ Invites (B01)
- ✅ Reset password (se existir)
- ✅ Sandbox notification ("your sandbox is paused") — link pra UI do sandbox específico
- ✅ Workspace member added — link pra `/w/{wid}/members`
- ❌ Marketing / public docs links — usar apex
- ❌ Status page — usar `status.<base>` (reserved slug)

## Test

```go
func TestBuildTenantURL(t *testing.T) {
    cases := map[string]struct{ slug, path, want string }{
        "with slug":    {"empresa-a", "/login", "https://empresa-a.agentserver.com/login"},
        "no slug":      {"", "/login", "https://agentserver.com/login"},
        "deep path":    {"acme", "/w/abc/sandboxes/xyz", "https://acme.agentserver.com/w/abc/sandboxes/xyz"},
        "with query":   {"acme", "/accept-invite?token=xxx", "https://acme.agentserver.com/accept-invite?token=xxx"},
    }
    base := "agentserver.com"
    for name, tc := range cases {
        t.Run(name, func(t *testing.T) {
            got := BuildTenantURL(tc.slug, base, tc.path)
            if got != tc.want { t.Fatalf("got %q want %q", got, tc.want) }
        })
    }
}
```

## Acceptance criteria

- [ ] Helper `BuildTenantURL` centraliza construção de URL
- [ ] Todos mailers existentes refatorados para usar helper
- [ ] Invite URL (B01) usa subdomínio do workspace destino
- [ ] Tests cobrem casos com/sem slug, path, query string
- [ ] Doc menciona convenção

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Workspaces sem slug (legado) | impossível após migration 040 — slug obrigatório |
| Trailing slash | nunca — caller passa path com slash inicial |
| Forçar HTTPS | sempre — agentserver é HTTPS-only |

## Anti-patterns

- ❌ Hardcoded `https://agentserver.com` em mailers
- ❌ `fmt.Sprintf` ad-hoc — sempre via helper (DRY)
- ❌ Esquecer de codificar query params (usar `url.Values`)

## Out of scope

- HTTP fallback (não suportamos)
- Custom domains por workspace (`acme.com/login` em vez de `acme.agentserver.com`) — futuro

## Definition of done

- PR mergeado
- Tests passam
- DEV smoke: invite gerado tem URL com subdomínio correto
