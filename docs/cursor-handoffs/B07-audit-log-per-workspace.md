# Cursor Handoff — B07: Audit log por workspace

**Backlog:** B07 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~150
**Tempo:** 2 dias
**Dependências:** PR #53 (active_workspace_id na session) — já mergeado

## Goal

Registrar todas ações sensíveis com `(user_id, active_workspace_id, event, details, ip, ua, at)` numa tabela imutável. Async via channel + worker. Não bloqueia request path.

## Why now

- SOC2 controle 7.3 ("monitoring of access") exige auditoria
- Debug: "por que esse usuário deletou esse sandbox?"
- Detection: "tentou bind workspace que não é membro 50x em 1h"
- Pré-requisito pra compliance enterprise + venda corporate

## Required reading

- `internal/db/migrations/034_draft_audit.sql` (PR #43) — schema similar
- `internal/server/playground_handlers.go` busque `audit` — pattern existente do playground
- `internal/auth/auth.go::Middleware` — injeta `active_workspace_id` no context (PR #53)
- `internal/server/operations.go` — tem mecanismo de audit existente; avaliar reuso

## Files to touch

| Path | Mudança |
|---|---|
| `internal/db/migrations/045_session_audit_events.sql` | nova tabela |
| `internal/db/audit.go` (novo ou expandido) | InsertAuditEvent, ListByWorkspace |
| `internal/audit/audit.go` (novo) | service com channel + worker |
| `internal/audit/audit_test.go` | tests |
| `internal/server/middleware_audit.go` (novo) | middleware http opcional |
| Handlers que precisam logar | sandbox.{create,delete}, workspace.{create,member_added,member_removed,sso_changed}, auth.login.{success,failure,workspace_set} |
| `internal/server/handlers_audit.go` (novo) | GET /api/workspaces/{wid}/audit (list) |
| `web/src/components/WorkspaceDetail.tsx` | tab "Audit log" |
| `docs/api/openapi.{yaml,json}` | regen |

## Migration 045

```sql
-- 045_session_audit_events.sql
CREATE TABLE IF NOT EXISTS session_audit_events (
    id              BIGSERIAL PRIMARY KEY,
    user_id         TEXT REFERENCES users(id) ON DELETE SET NULL,
    workspace_id    TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,        -- 'login.success', 'sandbox.created', etc.
    details         JSONB,                -- arbitrary metadata
    request_method  TEXT,
    request_path    TEXT,
    response_status INTEGER,
    ip              TEXT,
    user_agent      TEXT,
    error_msg       TEXT,
    at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_workspace_at
    ON session_audit_events(workspace_id, at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_user_at
    ON session_audit_events(user_id, at DESC)
    WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_event_at
    ON session_audit_events(event_type, at DESC);
```

## Service pattern (async via channel)

```go
// internal/audit/audit.go
package audit

type Event struct {
    UserID         string
    WorkspaceID    string
    EventType      string
    Details        map[string]any
    RequestMethod  string
    RequestPath    string
    ResponseStatus int
    IP, UserAgent  string
    ErrorMsg       string
}

type Service struct {
    db   *db.DB
    ch   chan Event
    wg   sync.WaitGroup
    quit chan struct{}
}

func NewService(database *db.DB, bufferSize int) *Service {
    s := &Service{
        db:   database,
        ch:   make(chan Event, bufferSize),
        quit: make(chan struct{}),
    }
    s.wg.Add(1)
    go s.worker()
    return s
}

func (s *Service) Log(ctx context.Context, event string, details map[string]any) {
    e := Event{
        UserID:      auth.UserIDFromContext(ctx),
        WorkspaceID: auth.ActiveWorkspaceFromContext(ctx),
        EventType:   event,
        Details:     details,
        // RequestMethod/Path/Status/IP/UA podem vir via middleware
    }
    select {
    case s.ch <- e:
    default:
        log.Printf("audit channel full, dropping event %s", event)
    }
}

func (s *Service) worker() {
    defer s.wg.Done()
    for {
        select {
        case e := <-s.ch:
            if err := s.db.InsertAuditEvent(e); err != nil {
                log.Printf("audit insert failed: %v", err)
            }
        case <-s.quit:
            return
        }
    }
}

func (s *Service) Shutdown() {
    close(s.quit)
    s.wg.Wait()
}
```

## Middleware HTTP opcional

```go
func AuditMiddleware(svc *audit.Service, eventForPath func(method, path string) string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            rw := &statusRecorder{ResponseWriter: w, status: 200}
            next.ServeHTTP(rw, r)
            event := eventForPath(r.Method, r.URL.Path)
            if event != "" {
                svc.Log(r.Context(), event, map[string]any{
                    "method": r.Method,
                    "path":   r.URL.Path,
                    "status": rw.status,
                    "ip":     r.Header.Get("X-Forwarded-For"),
                    "ua":     r.UserAgent(),
                })
            }
        })
    }
}
```

## Eventos prioritários (v1)

| Event | Onde dispara |
|---|---|
| `auth.login.success` | handleLogin OK |
| `auth.login.failure` | handleLogin 401 |
| `auth.workspace_set` | handleSetActiveWorkspace OK |
| `workspace.created` | handleCreateWorkspace OK |
| `workspace.deleted` | handleDeleteWorkspace OK |
| `workspace.member_added` | handleAddMember OK |
| `workspace.member_removed` | (a criar) |
| `workspace.sso_changed` | handleSSOConfig OK |
| `sandbox.created` | handleCreateSandbox OK |
| `sandbox.deleted` | handleDeleteSandbox OK |
| `invite.created` | handleCreateInvite OK (vide B01) |
| `invite.accepted` | handleAcceptInvite OK |

## Endpoint de leitura

```
GET /api/workspaces/{wid}/audit
Query: ?event_type=...&from=...&to=...&limit=100&offset=0
RBAC: owner/maintainer
Response: { events: [...], total }
```

## Retention

90 dias default (igual playground audit do PR #43). Cleanup via cron job ou função SQL `purge_audit_older_than(days int)`.

## Acceptance criteria

- [ ] Migration 045 aplica
- [ ] Service inicia/encerra ordenadamente (graceful shutdown)
- [ ] Channel buffer não bloqueia request path (overflow → log + drop, não 500)
- [ ] Eventos prioritários instrumentados
- [ ] Middleware HTTP opcional registrado em routes sensíveis
- [ ] GET /api/workspaces/{wid}/audit retorna paginado
- [ ] RBAC: developer NÃO vê audit (apenas owner/maintainer)
- [ ] UI tab "Audit log" no workspace detail
- [ ] Tests cobrem: insert success, drop on overflow, RBAC, retention query
- [ ] Cron de retention configurado em chart

## Test plan

```bash
go test -tags goolm ./internal/audit/... ./internal/server/...

# DEV smoke
# 1. Login → audit row entry com event_type=auth.login.success
# 2. Criar sandbox → audit row entry com event_type=sandbox.created
# 3. GET /api/workspaces/{wid}/audit retorna histórico
# 4. Developer chama GET /audit → 403
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Reuso de `draft_audit_events` (PR #43) | Separar (escopos diferentes — drafts vs session) |
| Buffer size do channel | 1000 (overflow logado, não pânico) |
| Detalhes em JSONB vs colunas | JSONB — flexibilidade vs query speed; índice GIN se virar gargalo |
| Export pra SIEM externo (Splunk, etc) | v2 — endpoint Webhook ou syslog |
| Encrypt at rest fields sensíveis | RDS já tem encryption at rest — suficiente v1 |

## Anti-patterns

- ❌ Audit síncrono no request path — latência
- ❌ Log de PII completa (senha, token full) — só hash/refs
- ❌ Permitir UPDATE/DELETE em session_audit_events (imutável)
- ❌ Workspace_id NULL em todos eventos — sempre stamp se houver

## Out of scope

- Stream para SIEM externo
- Real-time alerts (separate alerting service)
- Anomaly detection ML
- Audit log encryption per-tenant key

## Definition of done

- PR mergeado
- 10+ eventos prioritários instrumentados
- UI tab funcional
- Tests passam
- Retention cron rodando em DEV
- Doc `docs/audit-log.md` (novo) explicando schema + retention
