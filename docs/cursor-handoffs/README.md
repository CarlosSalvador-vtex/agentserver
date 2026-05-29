# Cursor Handoffs — Workspace Auth Backlog

Self-contained specs for each backlog item from
[`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md).

Each doc is **executable by a Cursor agent without re-discovery**: contém
goal, required reading, files to touch, acceptance criteria, test plan,
constraints e decision points.

## Order of execution (recommended)

| # | File | Item | LOC | Status | Why this order |
|---|---|---|---|---|---|
| 1 | [B10](B10-reserved-slugs.md) | Reservar mais slugs | ~5 | OPEN | Warm-up — valida workflow |
| 2 | [B02](B02-delete-workspace.md) | DELETE workspace | ~80 | OPEN | Limpa ws de teste, valor imediato |
| 3 | [B01](B01-invite-email.md) | Invite por email | ~200 | ✅ PR #71 | Feature visível, desbloqueia B6 |
| 4 | [B06](B06-email-subdomain-urls.md) | URLs subdomínio em emails | ~30 | OPEN (unblocked) | B01 shipped — PR #71 |
| 5 | [B07](B07-audit-log-per-workspace.md) | Audit log por workspace | ~150 | ✅ PR #72 | Compliance pré-SOC2 |
| 6 | [B03](B03-delete-user.md) | DELETE user LGPD | ~120 | OPEN | B07 shipped (recommended) |
| 7 | [B09](B09-choose-workspace-apex.md) | "Choose workspace" UI no apex | ~130 | OPEN | UX cross-tenant |
| 8 | [B08](B08-codex-auth-vs-cookie.md) | codex-auth × cookie host-only | 0-200 | ❌ CANCELLED | Codex integration descartado (ver `docs/ops/codex-not-used.md`); Path A (status quo) é suficiente |
| 9 | [B04](B04-sso-per-workspace.md) | SSO por workspace (Opção B) | ~600 | ❌ CANCELLED | Fora de escopo do produto atual |
| 10 | [B05](B05-hybrid-sso-password.md) | Híbrido SSO + senha (Opção C) | +150 | ❌ CANCELLED | Dependia de B04 |

## Como usar com Cursor

1. Cole o conteúdo do handoff específico no chat do Cursor
2. Cursor lê os arquivos referenciados, implementa, abre PR
3. Reviewer humano valida acceptance criteria
4. Merge

## Constraints globais (válidos pra TODOS os handoffs)

- **Sempre TDD:** test falhando → impl → test passando → commit
- **Tag build:** `goolm` obrigatório em comandos Go
- **Branch naming:** `feat/<short>` ou `chore/<short>` ou `fix/<short>`
- **PR sempre aberto:** mesmo pra mudanças triviais (memory `feedback_always_open_pr`)
- **kubectl context:** prod é default; pra DEV use `--context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform`
- **OpenAPI regen:** se mudou handler ou request/response, rode `make openapi && make api-docs` e commit
- **Sem breaking changes** em endpoints existentes — usar novos endpoints ou campos opcionais
- **Migrations:** novo número sequencial em `internal/db/migrations/` (próximo após 043 — última é `043_publish_draft.sql`)
- **Não commitar:** `web/dist/` (build artifact)
- **Sealed Secrets** pra qualquer secret novo (vide `docs/sealed-secrets.md`)
