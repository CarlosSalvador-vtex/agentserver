# Centralized CronJob Scheduler for Agent Sandboxes

**Date:** 2026-04-05
**Status:** Draft
**Scope:** agentserver (primary), nanoclaw (minor changes)

## Current State: NanoClaw's Built-in Scheduler

NanoClaw already has a full-featured cron/scheduling system running **inside each sandbox pod**:

### Architecture (per-sandbox)

```
NanoClaw Pod
├─ task-scheduler.ts        60s tick loop, evaluates due tasks
├─ db.ts (SQLite)           scheduled_tasks + task_run_logs tables
├─ ipc.ts                   File-based IPC for schedule_task/pause/resume/cancel/update
├─ container-runner.ts      Spawns agent container per task execution
└─ container/agent-runner/
   └─ ipc-mcp-stdio.ts     MCP tools: schedule_task, list_tasks, pause_task, etc.
```

### Key Features

| Feature | Implementation |
|---------|---------------|
| **Schedule types** | `cron` (5-field + timezone), `interval` (ms), `once` (local timestamp) |
| **Storage** | SQLite `scheduled_tasks` table with `next_run` index |
| **Execution** | `runContainerAgent()` spawns agent with full tool access |
| **Context modes** | `isolated` (fresh session) or `group` (with chat history) |
| **Task management** | Create, pause, resume, cancel, update via IPC + MCP tools |
| **Execution logs** | `task_run_logs` table: duration, status, result, error |
| **Agent self-scheduling** | MCP `schedule_task` tool — agents create their own crons |
| **Authorization** | Main group can schedule cross-group; others self-only |
| **Drift prevention** | Interval anchors to scheduled time, not wall clock |
| **Pre-scripts** | Optional bash script runs before agent; JSON `{wakeAgent}` gate |

### Data Model (SQLite)

```sql
-- nanoclaw/src/db.ts
scheduled_tasks:
  id, group_folder, chat_jid, prompt, script,
  schedule_type ('cron'|'interval'|'once'), schedule_value,
  context_mode ('isolated'|'group'),
  next_run, last_run, last_result,
  status ('active'|'paused'|'completed'), created_at

task_run_logs:
  id, task_id, run_at, duration_ms,
  status ('success'|'error'), result, error
```

## Problem

The per-sandbox scheduler works but has fundamental limitations:

### 1. Paused Sandbox = Silent Cron Death

NanoClaw's `startSchedulerLoop()` runs inside the pod. When `IdleWatcher` pauses the sandbox (1-min idle timeout), the scheduler loop **stops**. All crons silently miss their fire times. When the sandbox is eventually resumed, `getDueTasks()` picks up the overdue tasks, but:

- For `interval` tasks, `computeNextRun()` skips missed intervals (correct), but there's **no record** of the missed fires
- For `cron` tasks, only the first overdue fire runs; intermediate missed fires are lost
- For `once` tasks, they fire late with no notification to the user

### 2. State Locked Inside the Pod

Schedules live in SQLite at `/app/store/*.db`. Agentserver has **zero visibility** into what's scheduled:

- No API to list all schedules across sandboxes
- No way to monitor which crons fired or failed
- Dashboard can't show "this sandbox has 3 active crons"
- No cross-sandbox budget tracking for cron-triggered LLM usage

### 3. Terminated Sandbox = Permanent Data Loss

If a sandbox is terminated (user deletes it, or K8s evicts the pod without PVC), all schedules and execution history are gone. There is no backup mechanism.

### 4. No Cross-Sandbox Coordination

Each sandbox is an island. You can't:

- Schedule a task on Sandbox B from Sandbox A's API
- Create a workspace-level dashboard of all recurring tasks
- Set workspace-wide limits on cron frequency or LLM budget

### 5. Resource Waste for Cron-Only Sandboxes

A sandbox that exists solely to run a daily 9am task must either:
- Stay running 24/7 (wastes resources), or
- Get paused by `IdleWatcher` and miss its crons (breaks functionality)

## Proposal

Extract the scheduling concern into a centralized **`cronbridge`** service — same architectural pattern as `imbridge`. The key insight: **imbridge solved the exact same problem for IM polling**.

| Aspect | Before imbridge | After imbridge | Before cronbridge | After cronbridge |
|--------|----------------|---------------|------------------|-----------------|
| **Where logic runs** | Each sandbox polls its own IM | Central service polls all IMs | Each sandbox runs its own cron loop | Central service evaluates all crons |
| **Paused sandbox** | Misses messages | Bridge forwards, wakes sandbox | Misses cron fires | Bridge fires, wakes sandbox |
| **Storage** | Local per-sandbox | PostgreSQL (shared) | SQLite per-sandbox | PostgreSQL (shared) |
| **Visibility** | None | Full API | None | Full API |

### What Changes for NanoClaw

NanoClaw keeps its MCP tools (`schedule_task`, `list_tasks`, etc.) for backward compatibility, but they become **thin clients** that call agentserver's cronbridge API instead of writing to local SQLite:

```
Before:  MCP tool → IPC file → nanoclaw reads → SQLite insert → local scheduler loop
After:   MCP tool → HTTP POST to cronbridge API → PostgreSQL → central scheduler
```

The local SQLite `scheduled_tasks` table and `startSchedulerLoop()` become dead code for sandboxes managed by cronbridge.

## Open Source Landscape

| System | Architecture | Key Insight for Us |
|--------|-------------|-------------------|
| **K8s CronJob** | Built-in controller, creates Jobs/Pods | Thundering-herd at minute boundaries; no multi-tenant awareness |
| **Furiko** | K8s operator + CRDs, H-token scheduling | Hash-randomized offsets prevent hot spots; back-scheduling on recovery |
| **Dkron** | Raft consensus, server-agent, gRPC dispatch | Overkill for our scale; demonstrates "central brain + remote execution" |
| **Ofelia/Chadburn** | Single daemon, Docker exec/run | Label-based auto-discovery is elegant but not K8s-native |
| **Monzo crons** | Central Go microservice, HTTP endpoint calls | Deploy-time registration + simple HTTP dispatch — closest to our pattern |
| **Slack Conductor** | Leader-elected scheduler + Kafka queue | Enqueue-then-execute decoupling; dedup table prevents double-fire |
| **Google SRE cron** | Paxos-based distributed cron | Explicit idempotent vs. at-most-once semantics |
| **robfig/cron** | Go library | De facto standard for cron parsing in Go |

**Chosen pattern**: Monzo-style central scheduler with Slack-style dedup, using `robfig/cron` for expression parsing, dispatching through `agent_tasks`.

## Architecture

```
                      cronbridge (port 8084)
                      ┌─────────────────────────────────────┐
                      │  Scheduler Engine                    │
                      │  ├─ robfig/cron v3 (expression eval) │
                      │  ├─ PostgreSQL (schedule storage)    │
                      │  └─ Job Dispatcher                   │
                      │     ├─ Creates agent_tasks           │
                      │     ├─ Resumes paused sandboxes      │
                      │     └─ Tracks execution history      │
                      │                                      │
                      │  HTTP API (reverse-proxied by main)  │
                      │  ├─ CRUD for cron schedules          │
                      │  ├─ Execution history                │
                      │  └─ Manual trigger                   │
                      └────────────┬────────────────────────┘
                                   │
                    ┌──────────────┼──────────────┐
                    │              │              │
                    ▼              ▼              ▼
              ┌──────────┐ ┌──────────┐ ┌──────────┐
              │ Sandbox A │ │ Sandbox B │ │ Sandbox C │
              │ (running) │ │ (paused)  │ │ (running) │
              │           │ │   ↓       │ │           │
              │ task_     │ │ resume →  │ │ task_     │
              │ worker    │ │ task_     │ │ worker    │
              │ polls     │ │ worker    │ │ polls     │
              │ & runs    │ │ polls     │ │ & runs    │
              └──────────┘ └──────────┘ └──────────┘
```

### Component Relationships

```
agentserver (port 8080)
├─ /api/workspaces/{id}/cron/*     →  reverse proxy  →  cronbridge:8084
├─ /api/sandboxes/{id}/cron/*      →  reverse proxy  →  cronbridge:8084
├─ /api/internal/cron/*            →  reverse proxy  →  cronbridge:8084
│
├─ /api/workspaces/{id}/im/*       →  reverse proxy  →  imbridge:8083   (existing)
│
├─ agent_tasks table (shared)      ←  cronbridge writes tasks here
├─ sandboxes table (shared)        ←  cronbridge reads status, triggers resume
└─ task_worker (in sandbox)        ←  polls & executes tasks as usual

NanoClaw Pod (updated)
├─ MCP tools (schedule_task etc.)  →  HTTP calls to cronbridge API
├─ task_worker                     ←  polls agent_tasks, executes
└─ SQLite scheduler loop           ←  REMOVED (dead code)
```

## Database Schema

### New Table: `cron_schedules`

Maps closely to nanoclaw's existing `scheduled_tasks` for easy migration:

```sql
CREATE TABLE cron_schedules (
    id              TEXT PRIMARY KEY DEFAULT generate_short_id(),
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    sandbox_id      TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,

    -- Schedule definition (preserves nanoclaw's model)
    name            TEXT,                                     -- optional human-readable label
    prompt          TEXT NOT NULL,                             -- agent prompt
    script          TEXT,                                      -- optional bash pre-script
    schedule_type   TEXT NOT NULL CHECK (schedule_type IN ('cron', 'interval', 'once')),
    schedule_value  TEXT NOT NULL,                             -- cron expr | ms | ISO timestamp
    timezone        TEXT NOT NULL DEFAULT 'UTC',               -- IANA timezone
    context_mode    TEXT NOT NULL DEFAULT 'isolated'           -- 'isolated' | 'group'
                    CHECK (context_mode IN ('isolated', 'group')),

    -- Execution config
    max_turns          INTEGER DEFAULT 0,                     -- 0 = unlimited
    max_budget_usd     REAL DEFAULT 0,                        -- 0 = no limit
    timeout_seconds    INTEGER NOT NULL DEFAULT 300,
    concurrency_policy TEXT NOT NULL DEFAULT 'skip'            -- 'skip' | 'queue' | 'replace'
                       CHECK (concurrency_policy IN ('skip', 'queue', 'replace')),
    max_retries        INTEGER NOT NULL DEFAULT 0,

    -- State
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'paused', 'completed')),
    next_run        TIMESTAMPTZ,                              -- precomputed next fire time
    last_run        TIMESTAMPTZ,
    last_result     TEXT,                                      -- first 200 chars of last output

    -- Nanoclaw-specific (for group routing)
    chat_jid        TEXT,                                      -- target group JID
    group_folder    TEXT,                                      -- nanoclaw group folder

    -- Audit
    created_by      TEXT,                                      -- user ID or "agent:<sandbox_id>"
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cron_schedules_due ON cron_schedules (next_run)
    WHERE status = 'active' AND next_run IS NOT NULL;
CREATE INDEX idx_cron_schedules_sandbox ON cron_schedules (sandbox_id);
CREATE INDEX idx_cron_schedules_workspace ON cron_schedules (workspace_id);
```

### New Table: `cron_executions`

```sql
CREATE TABLE cron_executions (
    id              TEXT PRIMARY KEY DEFAULT generate_short_id(),
    schedule_id     TEXT NOT NULL REFERENCES cron_schedules(id) ON DELETE CASCADE,
    task_id         TEXT REFERENCES agent_tasks(id),          -- linked agent_task (null if skipped)

    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'dispatched', 'running', 'completed', 'failed', 'skipped')),
    fire_time       TIMESTAMPTZ NOT NULL,                     -- scheduled fire time
    dispatched_at   TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,

    duration_ms     INTEGER,                                  -- execution duration
    result          TEXT,                                      -- output summary
    failure_reason  TEXT,
    retry_count     INTEGER NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_cron_exec_schedule ON cron_executions (schedule_id, created_at DESC);
CREATE INDEX idx_cron_exec_active ON cron_executions (status)
    WHERE status IN ('pending', 'dispatched', 'running');
```

## Core Interfaces

```go
package cronbridge

// Schedule maps to the cron_schedules table.
// Preserves all nanoclaw fields (chat_jid, group_folder, script, context_mode).
type Schedule struct {
    ID                string
    WorkspaceID       string
    SandboxID         string
    Name              string
    Prompt            string
    Script            string     // bash pre-script (nanoclaw feature)
    ScheduleType      string     // "cron" | "interval" | "once"
    ScheduleValue     string
    Timezone          string
    ContextMode       string     // "isolated" | "group"
    MaxTurns          int
    MaxBudgetUSD      float64
    TimeoutSeconds    int
    ConcurrencyPolicy string
    MaxRetries        int
    Status            string     // "active" | "paused" | "completed"
    NextRun           *time.Time
    LastRun           *time.Time
    LastResult        string
    ChatJID           string     // nanoclaw group JID
    GroupFolder       string     // nanoclaw group folder
    CreatedBy         string
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

// Execution maps to the cron_executions table.
type Execution struct {
    ID            string
    ScheduleID    string
    TaskID        string
    Status        string
    FireTime      time.Time
    DispatchedAt  *time.Time
    CompletedAt   *time.Time
    DurationMs    int
    Result        string
    FailureReason string
    RetryCount    int
}

// SchedulerDB abstracts database operations (same pattern as imbridge.BridgeDB).
type SchedulerDB interface {
    // Tick loop
    ListDueSchedules(ctx context.Context, now time.Time) ([]Schedule, error)
    UpdateScheduleAfterFire(ctx context.Context, id string, lastRun, nextRun *time.Time) error
    CreateExecution(ctx context.Context, exec *Execution) error
    UpdateExecution(ctx context.Context, id string, updates map[string]any) error
    GetActiveExecution(ctx context.Context, scheduleID string) (*Execution, error)
    ListStaleExecutions(ctx context.Context, timeout time.Duration) ([]Execution, error)

    // CRUD
    CreateSchedule(ctx context.Context, s *Schedule) error
    GetSchedule(ctx context.Context, id string) (*Schedule, error)
    ListSchedulesBySandbox(ctx context.Context, sandboxID string) ([]Schedule, error)
    ListSchedulesByWorkspace(ctx context.Context, workspaceID string) ([]Schedule, error)
    UpdateSchedule(ctx context.Context, id string, updates map[string]any) error
    DeleteSchedule(ctx context.Context, id string) error
    ListExecutions(ctx context.Context, scheduleID string, limit int) ([]Execution, error)
}

// SandboxManager abstracts sandbox lifecycle operations.
type SandboxManager interface {
    GetSandboxStatus(ctx context.Context, sandboxID string) (string, error)
    ResumeSandbox(ctx context.Context, sandboxID string) error
    GetSandboxPodIP(ctx context.Context, sandboxID string) (string, error)
}

// TaskDispatcher creates agent_tasks for execution.
type TaskDispatcher interface {
    CreateTask(ctx context.Context, opts CreateTaskOpts) (taskID string, err error)
    GetTaskStatus(ctx context.Context, taskID string) (string, error)
}
```

## Scheduler Engine

```go
// Scheduler is the central cron engine. Analogous to imbridge.Bridge.
type Scheduler struct {
    db         SchedulerDB
    sbxMgr     SandboxManager
    dispatcher TaskDispatcher
    parser     cron.Parser       // robfig/cron v3
    stop       chan struct{}
}

func NewScheduler(db SchedulerDB, sbxMgr SandboxManager, disp TaskDispatcher) *Scheduler

func (s *Scheduler) Start(ctx context.Context)  // 10s tick loop
func (s *Scheduler) Stop()
```

### Tick Loop

Every 10 seconds:

```
1. SELECT * FROM cron_schedules WHERE status='active' AND next_run <= NOW()

2. For each due schedule:
   a. Concurrency check:
      - "skip":    if active execution exists → create execution with status="skipped"
      - "queue":   create execution with status="pending" (dispatch when active completes)
      - "replace": cancel active execution, proceed
   b. Sandbox status:
      - "running":    proceed
      - "paused":     POST /api/sandboxes/{id}/resume, wait up to 2min for pod IP
      - "terminated": mark execution "failed", disable schedule
   c. Script gate (if schedule has pre-script):
      - Execute bash script via K8s exec
      - Parse JSON output: if wakeAgent=false → skip execution
   d. Create agent_task targeting sandbox (reuses existing agent_tasks + task_worker)
   e. Create cron_execution record (status="dispatched")
   f. Compute next_run:
      - cron: robfig/cron parser with timezone
      - interval: anchor to last fire time, skip missed beats (same as nanoclaw's computeNextRun)
      - once: set next_run=NULL, status="completed"

3. Sync stale executions: dispatched >timeout ago → mark "failed", retry if allowed
4. Sync completed executions: check linked agent_task status → update cron_execution
```

### Next Fire Time (ported from nanoclaw)

```go
func (s *Scheduler) computeNextRun(sched Schedule, after time.Time) (*time.Time, error) {
    switch sched.ScheduleType {
    case "once":
        return nil, nil // one-shot, mark completed
    case "cron":
        loc, _ := time.LoadLocation(sched.Timezone)
        parsed, _ := s.parser.Parse(sched.ScheduleValue)
        next := parsed.Next(after.In(loc)).UTC()
        return &next, nil
    case "interval":
        ms, _ := strconv.ParseInt(sched.ScheduleValue, 10, 64)
        d := time.Duration(ms) * time.Millisecond
        // Anchor to scheduled time, skip missed intervals (same as nanoclaw)
        next := sched.NextRun.Add(d)
        for !next.After(after) {
            next = next.Add(d)
        }
        return &next, nil
    }
    return nil, fmt.Errorf("unknown schedule type: %s", sched.ScheduleType)
}
```

### Concurrency Policies

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `skip` | Previous still running → skip this fire, log it | Default. Idempotent monitoring tasks |
| `queue` | Queue execution, dispatch when previous completes | Ordered sequential tasks |
| `replace` | Cancel previous, start new | "Latest data" tasks where old results are stale |

## HTTP API

### Routes (reverse-proxied from main agentserver)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/workspaces/{wid}/cron/schedules` | Create schedule |
| `GET` | `/api/workspaces/{wid}/cron/schedules` | List all in workspace |
| `GET` | `/api/workspaces/{wid}/cron/schedules/{id}` | Get schedule detail |
| `PUT` | `/api/workspaces/{wid}/cron/schedules/{id}` | Update schedule |
| `DELETE` | `/api/workspaces/{wid}/cron/schedules/{id}` | Delete schedule |
| `POST` | `/api/workspaces/{wid}/cron/schedules/{id}/trigger` | Manual fire |
| `POST` | `/api/workspaces/{wid}/cron/schedules/{id}/pause` | Pause |
| `POST` | `/api/workspaces/{wid}/cron/schedules/{id}/resume` | Resume |
| `GET` | `/api/workspaces/{wid}/cron/schedules/{id}/executions` | Execution history |
| `GET` | `/api/sandboxes/{sid}/cron/schedules` | List for sandbox |
| `POST` | `/api/sandboxes/{sid}/cron/schedules` | Create (sandbox-scoped, proxy_token auth) |
| `POST` | `/api/internal/cron/sync-status` | Internal: task_worker reports completion |

### Sandbox-Internal API (proxy_token auth)

NanoClaw's MCP tools call this API instead of local SQLite:

```
POST /api/sandboxes/{sid}/cron/schedules          → schedule_task
GET  /api/sandboxes/{sid}/cron/schedules          → list_tasks
PUT  /api/sandboxes/{sid}/cron/schedules/{id}     → update_task
DELETE /api/sandboxes/{sid}/cron/schedules/{id}    → cancel_task
POST /api/sandboxes/{sid}/cron/schedules/{id}/pause  → pause_task
POST /api/sandboxes/{sid}/cron/schedules/{id}/resume → resume_task
```

Auth: `Authorization: Bearer <proxy_token>` (same as task_worker's existing auth pattern).

### Request Example

```json
POST /api/sandboxes/sbx_xyz/cron/schedules
Authorization: Bearer <proxy_token>
{
    "prompt": "Check GitHub repo for new issues, triage by priority",
    "schedule_type": "cron",
    "schedule_value": "0 9 * * 1-5",
    "timezone": "Asia/Shanghai",
    "context_mode": "group",
    "chat_jid": "group123@im.wechat",
    "group_folder": "group-abc",
    "script": "curl -s https://api.github.com/repos/org/repo/issues?state=open | jq '{wakeAgent: (length > 0), data: length}'",
    "timeout_seconds": 600,
    "max_budget_usd": 1.0
}
```

## NanoClaw Migration

### Changes to nanoclaw (minimal)

The MCP tools in `container/agent-runner/src/ipc-mcp-stdio.ts` switch from IPC file writes to HTTP calls:

```typescript
// Before (ipc-mcp-stdio.ts)
writeIpcFile(TASKS_DIR, { type: 'schedule_task', ... });

// After
const resp = await fetch(
  `${NANOCLAW_BRIDGE_URL.replace('/im/send', '')}/cron/schedules`,
  {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${BRIDGE_SECRET}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ prompt, schedule_type, schedule_value, ... }),
  }
);
```

The `NANOCLAW_BRIDGE_URL` and `BRIDGE_SECRET` environment variables already exist for the IM bridge — we reuse the same auth mechanism.

### Migration of existing schedules

For sandboxes with existing SQLite schedules, a one-time migration:

1. On sandbox resume, nanoclaw reads local `scheduled_tasks` from SQLite
2. POSTs each to cronbridge API
3. Deletes local copy after successful sync
4. Sets a flag `CRON_MIGRATED=1` in `/app/data/` to skip future runs

### Feature parity checklist

| NanoClaw Feature | cronbridge Equivalent |
|-----------------|----------------------|
| `cron` / `interval` / `once` schedule types | Same — `schedule_type` + `schedule_value` |
| Timezone support | `timezone` field (IANA) |
| Context mode (isolated/group) | `context_mode` field — cronbridge passes to agent_task |
| Pre-script with `wakeAgent` gate | `script` field — cronbridge execs via K8s pod exec before dispatch |
| Group folder / chat JID | `group_folder` + `chat_jid` fields preserved |
| Authorization (main vs non-main) | Enforced at API level: non-main sandbox can only CRUD own schedules |
| Interval drift prevention | Same anchoring logic in `computeNextRun()` |
| Result forwarding to chat | cronbridge sends result to imbridge for chat delivery |
| `last_result` (200 chars) | `last_result` column in `cron_schedules` |
| Run logs (duration, status, error) | `cron_executions` table |

## File Structure

### New Files

| File | Purpose |
|------|---------|
| `cmd/cronbridge/main.go` | Service entry point (same pattern as `cmd/imbridge/main.go`) |
| `internal/cronbridge/scheduler.go` | Tick loop, fire logic, retry handling |
| `internal/cronbridge/dispatch.go` | TaskDispatcher: creates agent_tasks + resumes sandboxes |
| `internal/cronbridge/nextfire.go` | computeNextRun() ported from nanoclaw |
| `internal/cronbridge/script.go` | Pre-script execution via K8s exec |
| `internal/cronbridgesvc/server.go` | HTTP server (same pattern as `imbridgesvc/server.go`) |
| `internal/cronbridgesvc/handlers.go` | CRUD + trigger + history endpoints |
| `internal/cronbridgesvc/config.go` | Environment config loader |
| `internal/db/cron_schedules.go` | DB operations for `cron_schedules` |
| `internal/db/cron_executions.go` | DB operations for `cron_executions` |
| `internal/db/migrations/015_cron_schedules.sql` | Schema migration |
| `Dockerfile.cronbridge` | Docker build (same base as `Dockerfile.imbridge`) |
| `deploy/helm/agentserver/templates/cronbridge.yaml` | K8s Deployment + Service |

### Modified Files

| File | Change |
|------|--------|
| `internal/server/server.go` | Add `CronBridgeURL` field; register reverse-proxy routes `/api/.../cron/*` |
| `internal/sandbox/config.go` | Add `CRONBRIDGE_URL` to nanoclaw env config |
| `deploy/helm/agentserver/values.yaml` | Add `cronbridge:` section |

### NanoClaw Modified Files

| File | Change |
|------|--------|
| `container/agent-runner/src/ipc-mcp-stdio.ts` | MCP tools call cronbridge HTTP API instead of IPC file writes |
| `src/task-scheduler.ts` | Disable local scheduler when `CRONBRIDGE_URL` is set |
| `src/ipc.ts` | Skip local task IPC handling when cronbridge enabled |

## Deployment

### Helm values.yaml

```yaml
cronbridge:
  enabled: false
  image:
    repository: ghcr.io/agentserver/agentserver
    tag: latest
  port: 8084
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 200m
      memory: 128Mi
```

### Service Properties

| Property | Value | Rationale |
|----------|-------|-----------|
| Replicas | 1 | Single-leader; `FOR UPDATE SKIP LOCKED` if scaling to 2 |
| Database | Shared PostgreSQL | Same DB as agentserver/imbridge |
| Port | 8084 | Next in sequence after imbridge (8083) |
| Health check | `GET /healthz` | Standard probe |

## Execution Flow Walkthrough

### Happy path: Daily cron with paused sandbox

```
1. Agent in sandbox sbx_a creates schedule via MCP tool:
   POST /api/sandboxes/sbx_a/cron/schedules
   { schedule_type: "cron", schedule_value: "0 9 * * 1-5", timezone: "Asia/Shanghai", ... }

2. cronbridge stores in PostgreSQL, computes next_run = Mon 09:00 CST

3. sbx_a goes idle → IdleWatcher pauses it

4. Monday 09:00 — cronbridge tick detects next_run <= now()
   a. No active execution → proceed (concurrency_policy: "skip")
   b. Sandbox status = "paused"
   c. POST /api/sandboxes/sbx_a/resume → wait for pod IP (max 2 min)
   d. Pre-script: K8s exec `curl ... | jq '{wakeAgent: true}'` → proceed
   e. Create agent_task: { target_id: sbx_a, prompt: "Check issues...", ... }
   f. Insert cron_execution (status="dispatched")
   g. Update schedule: last_run=now(), next_run=Tue 09:00

5. sbx_a task_worker picks up task → executes via Agent SDK
   → result forwarded to chat_jid via imbridge

6. cronbridge syncs: task completed → execution status="completed"

7. sbx_a idles → auto-paused again until next fire
```

### Edge case: Script gate blocks execution

```
1. Schedule has script: "curl api/status | jq '{wakeAgent: (.status != \"ok\")}'"
2. Fire time arrives → cronbridge K8s exec runs script in sandbox pod
3. Output: {"wakeAgent": false}
4. Execution marked "skipped" (no agent_task created)
5. next_run advanced to next fire time
```

## Comparison: cronbridge vs imbridge vs nanoclaw-local

| Aspect | nanoclaw local | imbridge | cronbridge |
|--------|---------------|----------|------------|
| **Trigger** | 60s timer in pod | External IM message | Time-based schedule |
| **Storage** | SQLite in pod | PostgreSQL | PostgreSQL |
| **Survives pause** | No | Yes (stateless forward) | Yes (central scheduler) |
| **Visibility** | None | Full API | Full API |
| **Cross-sandbox** | No | Channel-level | Workspace-level |
| **Service port** | N/A (in-process) | 8083 | 8084 |

## Implementation Plan

### Phase 1: Core Scheduler (MVP)

1. Database migration `015_cron_schedules.sql`
2. DB operations: `internal/db/cron_schedules.go`, `cron_executions.go`
3. Scheduler engine: `internal/cronbridge/scheduler.go` (tick loop + dispatch + nextfire)
4. HTTP handlers: `internal/cronbridgesvc/` (CRUD + trigger + history)
5. Service entry point: `cmd/cronbridge/main.go`
6. Reverse proxy integration in `server.go`
7. Helm chart + Dockerfile

### Phase 2: NanoClaw Integration

8. Update MCP tools to call cronbridge HTTP API
9. Add `CRONBRIDGE_URL` to nanoclaw sandbox config
10. Disable local scheduler when cronbridge is configured
11. SQLite → PostgreSQL one-time migration logic
12. Pre-script execution via K8s exec

### Phase 3: Reliability

13. Sandbox auto-resume before dispatch
14. Retry with exponential backoff
15. Stale execution detection + cleanup
16. Execution status sync (poll agent_tasks)

### Phase 4: UX

17. Web UI for schedule management
18. Execution log streaming (link to agent session)
19. Result delivery to IM chat via imbridge
20. Workspace-level schedule dashboard

### Estimated Scope

- **Phase 1**: ~10 new files, ~1400 lines Go, 1 SQL migration
- **Phase 2**: ~200 lines TypeScript changes in nanoclaw, ~100 lines Go
- **Phase 3**: ~400 lines Go across existing files
- **Phase 4**: Frontend + ~300 lines Go

## Security Considerations

- **Auth**: Workspace routes use JWT/session; sandbox routes use proxy_token
- **Scope**: Non-main sandboxes can only CRUD their own schedules (matches nanoclaw's auth model)
- **Rate limits**: Max 50 schedules/workspace, min 1-minute cron interval
- **Budget**: Per-execution `max_budget_usd` to prevent runaway LLM spend
- **Script safety**: Pre-scripts run inside the sandbox pod (same trust boundary as agent tools)

## Future Considerations

- **Multi-replica HA**: `pg_advisory_lock` or `FOR UPDATE SKIP LOCKED` for leader election
- **Event-driven triggers**: Webhook/git-push triggers alongside time-based crons
- **Execution quotas**: Per-workspace monthly execution limits
- **Schedule templates**: Predefined schedule patterns (daily standup, weekly review, etc.)
