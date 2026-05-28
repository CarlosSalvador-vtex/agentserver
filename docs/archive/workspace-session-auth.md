# Workspace-Scoped Session Auth

> **Status:** v1 (backend only) — sessão de usuário carrega workspace ativo.
> **Migration:** `039_token_active_workspace.sql`
> **Branch:** `worktree-auth`

## Motivação

agentserver evoluiu para SaaS multi-tenant (cada empresa = um workspace).
Usuários podem pertencer a N workspaces (membership via `workspace_members`),
mas até agora o cookie de sessão não carregava qual workspace estava "ativo".
Consequência: toda chamada precisava receber `workspace_id` explicitamente no
path (`/api/workspaces/{id}/...`) ou request, abrindo brechas para erros
cross-workspace e impossibilitando audit logs por workspace na camada de
sessão.

Esta v1 introduz **workspace ativo por sessão** — o cookie carrega o
workspace selecionado e middleware injeta no context.

Outras opções consideradas e rejeitadas para v1:

| Opção | Por que não agora |
|---|---|
| User pertence a 1 workspace | Quebra membros multi-org existentes |
| SSO/OIDC por workspace | Requer UI de admin, maior superfície |
| Subdomínio por workspace | White-label — v3 |

## Modelo de dados

### Migration 039

```sql
ALTER TABLE auth_tokens
    ADD COLUMN IF NOT EXISTS active_workspace_id TEXT
        REFERENCES workspaces(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_auth_tokens_active_workspace
    ON auth_tokens(active_workspace_id)
    WHERE active_workspace_id IS NOT NULL;
```

**Decisões:**

- `ON DELETE SET NULL` (não `CASCADE`): deletar um workspace não pode invalidar
  todas as sessões dos seus membros. NULL força re-seleção no próximo request.
- Index parcial (`WHERE active_workspace_id IS NOT NULL`): maioria das sessões
  novas começa NULL; index só cobre linhas relevantes.
- Coluna `NULL` significa "fresh login, sem workspace selecionado" — caller
  deve detectar e redirecionar para tela de seleção (frontend v2).

## API

### `GET /api/auth/me`

Resposta estendida com `active_workspace_id`:

```json
{
  "id": "u-abc",
  "email": "alice@empresa-a.com",
  "name": "Alice",
  "picture": null,
  "role": "developer",
  "active_workspace_id": "ws-empresa-a"
}
```

`active_workspace_id: null` quando a sessão ainda não selecionou workspace.

### `POST /api/auth/session/workspace`

Define o workspace ativo da sessão atual.

**Request:**

```json
{ "workspace_id": "ws-empresa-a" }
```

Envie `"workspace_id": ""` para limpar (volta a NULL).

**Validação:** o handler chama `IsWorkspaceMember(workspaceID, userID)`. Caller
não membro recebe `403 not a workspace member`.

**Response:**

```json
{ "active_workspace_id": "ws-empresa-a" }
```

**Códigos:**

| Status | Significado |
|---|---|
| 200 | OK — workspace bound (ou limpo) |
| 400 | `invalid request` — body malformado |
| 401 | sem cookie de sessão |
| 403 | usuário não é membro do workspace |
| 500 | falha de DB |

## Fluxo

```
Login (password / OIDC)
   │
   ▼
CreateToken (active_workspace_id = NULL)
   │
   ▼
GET /api/auth/me  →  active_workspace_id: null
   │
   ▼  (frontend mostra picker)
POST /api/auth/session/workspace { workspace_id: "ws-x" }
   │  (handler valida membership)
   ▼
UPDATE auth_tokens SET active_workspace_id = 'ws-x' WHERE token = ?
   │
   ▼
Requests subsequentes:
   Middleware → ctx[activeWorkspaceID] = "ws-x"
   Handlers   → auth.ActiveWorkspaceFromContext(r.Context())
```

## Como handlers consomem

```go
import "github.com/agentserver/agentserver/internal/auth"

func (s *Server) someHandler(w http.ResponseWriter, r *http.Request) {
    userID  := auth.UserIDFromContext(r.Context())
    activeWS := auth.ActiveWorkspaceFromContext(r.Context())

    if activeWS == "" {
        http.Error(w, "no workspace selected", http.StatusPreconditionRequired)
        return
    }
    // ... operar em activeWS, ignorando workspace_id do path se quiser
}
```

### Helpers expostos por `internal/auth`

| Func | Retorna |
|---|---|
| `UserIDFromContext(ctx)` | `userID` (já existia) |
| `ActiveWorkspaceFromContext(ctx)` | `workspaceID` ou `""` |
| `SessionTokenFromContext(ctx)` | cookie value (raw) — usado pelo próprio handler de set |
| `ContextWithUserID(ctx, id)` | tests bypass |
| `ContextWithActiveWorkspace(ctx, id)` | tests bypass |
| `Auth.SetActiveWorkspace(token, userID, wsID)` | `(ok bool, err)` — `ok=false` se não-membro |
| `Auth.ValidateTokenWithWorkspace(token)` | `(userID, activeWS, ok)` |

## Coexistência com outros caminhos de auth

| Caminho | Workspace ativo |
|---|---|
| **Cookie session** (`Auth.Middleware`) | lê `auth_tokens.active_workspace_id` |
| **Hydra Bearer** (`BearerMiddleware` — TUI/CLI) | **não suporta workspace ativo** — token Hydra escopo por usuário; handler deve receber workspace explicitamente |
| **Workspace API key** | já é workspace-scoped por construção (tabela `workspace_api_keys.workspace_id`) — não precisa de active |
| **codexauth (PKCE)** | escopo por usuário; trate igual ao Bearer |

**Regra:** se `auth.ActiveWorkspaceFromContext(ctx) == ""`, não assuma
contexto multi-tenant — handlers que **dependem** de workspace ativo devem
retornar 412 (precondition required) ou aceitar workspace explícito como
fallback.

## Cross-subdomain SSO

`AGENTSERVER_COOKIE_DOMAIN=.agent.cs.ac.cn` permite o cookie atravessar
subdomínios (codex-auth shim). O `active_workspace_id` viaja junto — todas
as origens enxergam o mesmo workspace ativo. Se isso virar problema (ex.:
codex-auth não deve ver o mesmo workspace que dashboard), gerar tokens
separados por subdomínio.

## Compatibilidade

- **Backward compatible:** `ValidateToken` (assinatura antiga) continua
  funcionando — internamente usa `ValidateTokenWithWorkspace` e descarta o
  segundo retorno.
- **Sessões pré-migration:** column padrão NULL → continuam válidas, sem
  workspace ativo, até o usuário chamar `POST /api/auth/session/workspace`.
- **Workspace deletado:** `ON DELETE SET NULL` zera o campo; usuário cai no
  picker no próximo `/api/auth/me`.

## Não escopo (v2+)

- Frontend de seleção de workspace pós-login.
- Migração de handlers `/api/workspaces/{id}/...` para preferir
  `ActiveWorkspaceFromContext` sobre path param.
- SSO/OIDC config por workspace.
- Workspace ativo no `BearerMiddleware` (Hydra).
- Audit log de troca de workspace ativo.

## Arquivos modificados

| Arquivo | Mudança |
|---|---|
| `internal/db/migrations/039_token_active_workspace.sql` | nova migration |
| `internal/db/tokens.go` | `ValidateTokenWithWorkspace`, `SetTokenActiveWorkspace` |
| `internal/auth/auth.go` | context keys, helpers, `SetActiveWorkspace`, middleware injeção |
| `internal/server/api_types.go` | `AuthMeResponse.ActiveWorkspaceID`, novos request/response types |
| `internal/server/server.go` | `handleSetSessionWorkspace`, route registration, `handleMe` populado |

## Como testar manual

```bash
# 1. Login
curl -c cookies.txt -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"..."}'

# 2. /me retorna active_workspace_id: null
curl -b cookies.txt http://localhost:8080/api/auth/me

# 3. Set workspace ativo
curl -b cookies.txt -X POST http://localhost:8080/api/auth/session/workspace \
  -H 'Content-Type: application/json' \
  -d '{"workspace_id":"ws-empresa-a"}'

# 4. /me agora retorna active_workspace_id: "ws-empresa-a"
curl -b cookies.txt http://localhost:8080/api/auth/me

# 5. Tentar workspace que usuário não é membro
curl -b cookies.txt -X POST http://localhost:8080/api/auth/session/workspace \
  -H 'Content-Type: application/json' \
  -d '{"workspace_id":"ws-empresa-b"}'
# → 403 not a workspace member

# 6. Limpar
curl -b cookies.txt -X POST http://localhost:8080/api/auth/session/workspace \
  -H 'Content-Type: application/json' \
  -d '{"workspace_id":""}'
```

## Próximos passos (recomendados)

1. Regenerar OpenAPI spec: `make openapi` (handlers anotados com swag).
2. Regenerar tipos frontend: `cd web && pnpm openapi:gen`.
3. UI mínima de picker pós-login que chama `POST /api/auth/session/workspace`.
4. Adicionar testes em `internal/server/` cobrindo: membro válido, não-membro
   (403), workspace inexistente (FK retorna erro → 500 ou 403 — decidir).
