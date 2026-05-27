# Cursor Handoff — B10: Reserved Slugs ampliada

**Backlog:** B10 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~5 (trivial)
**Tempo:** 30 min
**Dependências:** nenhuma

## Goal

Ampliar lista de reserved slugs no validador para cobrir hostnames operacionais que SaaS comumente expõe (`mail`, `support`, `status`, `docs`, `billing` etc.). Hoje a lista cobre só o mínimo.

## Why now

- Squatting: usuário cria workspace com slug `mail`, depois ops quer expor `mail.<base>` e colide
- Confusion attack: workspace `support` faz phishing parecer oficial
- Custo de adicionar depois é alto se já há tenants usando o slug

## Required reading

Antes de tocar código, leia (rápido — são pequenos):

- [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md) seção B10
- `internal/db/slug.go` (lista atual)
- `internal/db/slug_test.go` (tests existentes)

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/slug.go` | Adicionar slugs ao map `reservedSlugs` |
| `internal/db/slug_test.go` | Adicionar test cases p/ cada nova categoria |

## Lista a adicionar

```go
// Operacional/email
"mail", "email", "mailbox", "mx", "smtp", "imap", "pop",

// Suporte
"support", "help", "helpdesk", "kb", "faq", "contact",

// Status/monitoring
"status", "health", "metrics", "dashboard", "grafana", "prometheus",

// Docs
"docs", "documentation", "wiki", "blog", "news",

// Infra
"cdn", "proxy", "ingress", "lb", "node", "pod", "k8s",

// Comercial
"billing", "pay", "payments", "pricing", "enterprise", "sales",

// Marketing
"signup", "trial", "demo", "marketing", "landing",

// Compliance/legal
"legal", "terms", "privacy", "tos", "gdpr", "lgpd", "compliance",

// Recursos
"media", "images", "files", "download", "upload",

// Hosts especiais
"localhost", "internal", "external", "public", "private",
```

## Implementation steps (TDD)

### Step 1: Test que vai falhar

Em `internal/db/slug_test.go` adicione casos:

```go
func TestValidateSlugReservedExpanded(t *testing.T) {
    reserved := []string{
        "mail", "support", "status", "docs", "billing", "legal",
        "signup", "cdn", "localhost",  // amostra das novas categorias
    }
    for _, s := range reserved {
        t.Run(s, func(t *testing.T) {
            if err := ValidateSlug(s); err == nil {
                t.Fatalf("expected %q to be rejected as reserved, got nil", s)
            }
        })
    }
}
```

Run: `cd internal/db && go test -tags goolm -run TestValidateSlugReservedExpanded -v`
Expected: FAIL — slugs ainda não estão na lista.

### Step 2: Adicionar slugs ao map

Em `internal/db/slug.go`, expandir `reservedSlugs`. Manter ordem semântica + comentário por categoria.

### Step 3: Test passa

Run: `cd internal/db && go test -tags goolm -run TestValidateSlug -v`
Expected: PASS em TODOS os testes (originais + novos).

### Step 4: Verificar workspaces existentes não conflitam

```bash
DEV="--context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
kubectl $DEV exec -n agentserver deploy/agentserver -c agentserver -- \
  sh -c 'psql "$DATABASE_URL" -tc "SELECT slug FROM workspaces"' | sort
```

Cruzar com nova lista. Se algum workspace existente colide, **abortar e ajustar**: migration de rename + comunicação com tenant afetado.

### Step 5: Commit

```bash
git add internal/db/slug.go internal/db/slug_test.go
git commit -m "feat(db): expand reserved slugs (mail, support, status, docs, billing, legal, etc.)"
```

## Acceptance criteria

- [ ] Lista expandida cobre 9 categorias mencionadas
- [ ] Comentários no código separam por categoria
- [ ] Test cobre amostra de cada categoria nova
- [ ] Build passa: `go build -tags goolm ./...`
- [ ] Tests passam: `go test -tags goolm ./internal/db/...`
- [ ] Nenhum workspace existente em DEV/PROD tem slug colidindo
- [ ] PR aberto com diff < 50 LOC

## Test plan

```bash
# Unit
cd internal/db && go test -tags goolm -run TestValidateSlug -v

# Integration via API (DEV após deploy)
curl -X POST https://default-workspace.agentserver.analytics.vtex.com/api/workspaces \
  -b /tmp/cj_admin \
  -d '{"name":"Mail Co","slug":"mail"}'
# expected: 400 "mail" is reserved
```

## Anti-patterns / Decisões pendentes

- **NÃO** adicionar slugs que sejam nomes de produtos do user (`acme`, `vtex`) — opção é blacklist + processo de release
- **NÃO** confundir com `reservedPrefixes` (`claw-`, `hermes-`) — esses são prefixos, não slugs exatos

## Out of scope

- Slugs internacionais (Unicode/IDN) — fora do escopo, slug ASCII-only é regra atual
- Processo de revisão de novos slugs reservados — virar item próprio (B10.1?) se necessário

## Definition of done

- PR mergeado em main
- DEV smoke: POST /api/workspaces com slug reservado → 400
- Tests passam no CI
