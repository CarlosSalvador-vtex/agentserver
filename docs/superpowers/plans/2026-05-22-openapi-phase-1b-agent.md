# Phase 1.b — OpenAPI: Agent tag implementation plan

**Branch:** `feat/openapi-phase-1b-agent`  
**Stacks on:** PR #157 (Codex Browser Sessions)  
**Date:** 2026-05-22

## Recon summary

Grep turned up **13 endpoints** across three sub-areas. All are pure REST CRUD — no
WebSocket, no SSE, no `/api/internal/*` RPC. The sub-areas are:

| # | Sub-area | Endpoint count |
|---|----------|---------------|
| 1 | Agent Discovery | 4 |
| 2 | Agent Tasks | 7 |
| 3 | Agent Mailbox | 2 |

**Tag decision:** single `Agent` tag (swag `@Tags Agent`). Sub-area is already
clear from the path prefix (`/discovery/`, `/tasks/`, `/mailbox/`). Three separate
tags would create visual noise without adding navigability.

---

## Endpoint inventory

### Discovery (4 endpoints)

| Method | Path | Handler | Auth |
|--------|------|---------|------|
| POST | `/api/agent/register` | `handleAgentRegister` | Bearer (OAuth Hydra, `agent:register` scope) |
| POST | `/api/agent/discovery/cards` | `handleRegisterAgentCard` | Bearer proxy\_token |
| GET | `/api/workspaces/{wid}/agents` | `handleListAgentCards` | CookieAuth |
| GET | `/api/agents/{sandboxId}` | `handleGetAgentCard` | CookieAuth |
| GET | `/api/agent/discovery/agents` | `handleAgentDiscoverAgents` | Bearer proxy\_token |

Discovery total: **5** (not 4 — recount after full read includes `handleAgentDiscoverAgents`).

### Tasks (6 endpoints)

| Method | Path | Handler | Auth |
|--------|------|---------|------|
| POST | `/api/workspaces/{wid}/tasks` | `handleCreateTask` | CookieAuth |
| GET | `/api/workspaces/{wid}/tasks` | `handleListTasks` | CookieAuth |
| GET | `/api/tasks/{id}` | `handleGetTask` | CookieAuth |
| POST | `/api/tasks/{id}/cancel` | `handleCancelTask` | CookieAuth |
| GET | `/api/agent/tasks/poll` | `handlePollTasks` | Bearer proxy\_token |
| PUT | `/api/agent/tasks/{id}/status` | `handleUpdateTaskStatus` | Bearer proxy\_token |
| POST | `/api/agent/tasks` | `handleAgentCreateTask` | Bearer proxy\_token |
| GET | `/api/agent/tasks/{id}` | `handleAgentGetTask` | Bearer proxy\_token |

Tasks total: **8** (includes the 2 proxy-token mirrors).

### Mailbox (2 endpoints)

| Method | Path | Handler | Auth |
|--------|------|---------|------|
| POST | `/api/agent/mailbox/send` | `handleSendMessage` | Bearer proxy\_token |
| GET | `/api/agent/mailbox/inbox` | `handleReadInbox` | Bearer proxy\_token |

**Grand total: 15 endpoints.**

---

## Tasks

### Task 0 — Plan (this document)

- [x] Recon all routes in `server.go`, handler files
- [x] Commit plan as `docs(plan): Phase 1.b — Agent tag implementation plan`

### Task 1 — DTOs in `api_types.go`

Add three sub-sections after `--- Codex Browser Sessions ---`:

**`// --- Agent Discovery ---`**
- `AgentRegisterRequest` — body for `POST /api/agent/register`
- `AgentRegisterResponse` — 201 body (sandbox\_id, tunnel\_token, proxy\_token, workspace\_id, short\_id)
- `AgentCardRegisterRequest` — body for `POST /api/agent/discovery/cards`
- `AgentCardRegisterResponse` — 200 body (`{"status":"ok"}`)
- `AgentCardItem` — one entry in discovery/agents lists (agent\_id, display\_name, description, agent\_type, status, card, version)

**`// --- Agent Tasks ---`**
- `AgentTaskCreateRequest` — body for POST tasks (target\_id, skill, prompt, system\_context, max\_turns, max\_budget\_usd, timeout\_seconds, delegation\_chain, requester\_id)
- `AgentTaskCreateResponse` — 201 body (task\_id, session\_id, status)
- `AgentTaskItem` — one entry in task list (task\_id, target\_id, requester\_id, skill, status, prompt, num\_turns, total\_cost\_usd, created\_at, completed\_at)
- `AgentTaskDetail` — full task (all fields including result, failure\_reason, output, workspace\_id, session\_id)
- `AgentTaskStatusRequest` — body for `PUT /api/agent/tasks/{id}/status`
- `AgentTaskCancelResponse` — body for cancel (`{"status":"cancelled"}`)
- `AgentTaskPollItem` — one entry from poll (task\_id, prompt, system\_context, max\_turns, max\_budget\_usd, session\_id)

**`// --- Agent Mailbox ---`**
- `AgentMailboxSendRequest` — body for send (to, text, msg\_type)
- `AgentMailboxSendResponse` — 201 body (message\_id, status)
- `AgentMailboxMessage` — one message in inbox (id, from, text, msg\_type, created\_at)

Commit: `feat(openapi): Agent DTOs in api_types.go`

### Task 2 — Annotate handlers

Files: `agent_register.go`, `agent_discovery.go`, `agent_proxy_routes.go`,
`agent_tasks.go`, `agent_mailbox.go`.

All get `@Tags Agent`. Inline structs replaced with named DTOs.  
Regenerate: `make openapi`  
Gate: `make openapi-check`

Commit: `feat(openapi): annotate Agent handlers (15 endpoints)`

### Task 3 — Frontend migration

No existing agent/task/mailbox types found in `web/src/lib/api.ts` — **no migration
needed.** The generated `apiClient` will provide fresh types for any future callers.

Task 3 is effectively a no-op; skip the commit.

### Task 4 — Verify + PR

```
make openapi-check
go test ./internal/server/...
cd web && pnpm build && git checkout -- dist/
git push -u origin feat/openapi-phase-1b-agent
gh pr create --title "feat(openapi): Phase 1.b — Agent tag (15 endpoints)" ...
```
