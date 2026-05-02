# CC-Broker Part 1: Bridge API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build cc-broker's Bridge API — the server that CC workers connect to via `--sdk-url` for session management, context replay, and event persistence. This is the foundation that Part 2 (Tool Router MCP) and Part 3 (Worker Management) build on.

**Architecture:** cc-broker exposes a CCR v2-compatible bridge API. CC workers connect via `--sdk-url http://cc-broker:8080/v1/sessions/{id}`, attach as workers (epoch bump + JWT), replay conversation history via SSE, and write events back via HTTP POST. The bridge reuses the existing `agent_session_events` table in the shared PostgreSQL database.

**Tech Stack:** Go 1.26, chi/v5, database/sql + lib/pq, crypto/hmac (JWT), slog

**Spec Reference:** `/root/agentserver/docs/superpowers/specs/2026-04-15-stateless-cc-design.md` Sections 4, 6

**Existing Reference:** `/root/agentserver/internal/bridge/` — the existing agentserver bridge implementation. cc-broker's bridge is a simplified, self-contained version of this.

**Context:** This is Part 1 of 3 for the cc-broker service. After this plan, cc-broker can:
- Create and manage sessions
- Accept CC worker connections via `--sdk-url`
- Replay conversation history via SSE
- Persist new events from CC workers
- Track worker heartbeats and state

It does NOT yet have: Tool Router MCP Server, CC process spawning, OpenViking integration, or the external `/api/turns` endpoint. Those come in Parts 2 and 3.

---

## File Structure

```
/root/agentserver/
├── cmd/cc-broker/
│   └── main.go                              # Service entry point
├── internal/ccbroker/
│   ├── config.go                            # Config struct + LoadConfigFromEnv()
│   ├── server.go                            # Server struct, NewServer(), Routes()
│   ├── store.go                             # DB: sessions, events, workers, epoch
│   ├── models.go                            # Domain types
│   ├── jwt.go                               # Worker JWT issuance + validation
│   ├── sse.go                               # SSE broker (in-memory pub/sub)
│   ├── dedup.go                             # BoundedUUIDSet for event dedup
│   ├── handler_session.go                   # POST /v1/sessions (create session)
│   ├── handler_bridge.go                    # POST /v1/sessions/{id}/bridge (worker attach)
│   ├── handler_events.go                    # GET .../worker/events/stream (SSE), POST .../worker/events (batch)
│   ├── handler_worker.go                    # PUT .../worker (state), POST .../worker/heartbeat
│   ├── handler_internal_events.go           # POST/GET .../worker/internal-events
│   ├── middleware.go                        # Worker JWT auth middleware
│   ├── migrations/
│   │   └── 001_initial.sql                  # Reuses existing agent_session tables (CREATE IF NOT EXISTS)
│   └── integration_test.go                  # Tests
├── Dockerfile.cc-broker                     # Docker build
└── docker-compose.yml                       # Add cc-broker service
```

---

## Task 1: Project Skeleton

**Files:**
- Create: `internal/ccbroker/config.go`
- Create: `internal/ccbroker/server.go`
- Create: `internal/ccbroker/models.go`
- Create: `cmd/cc-broker/main.go`

- [ ] **Step 1: Create config.go**

```go
package ccbroker

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   []byte
	LogLevel    slog.Level
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Port:        envOr("CCBROKER_PORT", "8085"),
		DatabaseURL: os.Getenv("CCBROKER_DATABASE_URL"),
		LogLevel:    slog.LevelInfo,
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("CCBROKER_DATABASE_URL is required")
	}
	secret := os.Getenv("CCBROKER_JWT_SECRET")
	if secret == "" {
		return cfg, fmt.Errorf("CCBROKER_JWT_SECRET is required (32+ chars)")
	}
	cfg.JWTSecret = []byte(secret)
	if v := os.Getenv("CCBROKER_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		}
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: Create models.go**

```go
package ccbroker

import (
	"encoding/json"
	"time"
)

type Session struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title,omitempty"`
	Status      string    `json:"status"`
	Epoch       int       `json:"epoch"`
	ExternalID  *string   `json:"external_id,omitempty"`
	Source      string    `json:"source,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type SessionEvent struct {
	ID          int64           `json:"id"`           // sequence_num (BIGSERIAL)
	SessionID   string          `json:"session_id"`
	EventID     string          `json:"event_id"`     // UUID from payload
	EventType   string          `json:"event_type"`
	Source      string          `json:"source"`
	Epoch       int             `json:"epoch"`
	Payload     json.RawMessage `json:"payload"`
	Ephemeral   bool            `json:"ephemeral"`
	CreatedAt   time.Time       `json:"created_at"`
}

type StreamClientEvent struct {
	EventID     string          `json:"event_id"`
	SequenceNum int64           `json:"sequence_num"`
	EventType   string          `json:"event_type"`
	Source      string          `json:"source"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   string          `json:"created_at"`
}

type WorkerJWTClaims struct {
	SessionID   string `json:"sid"`
	WorkspaceID string `json:"wid"`
	Epoch       int    `json:"epoch"`
	Exp         int64  `json:"exp"`
}

type BridgeResponse struct {
	WorkerJWT   string `json:"worker_jwt"`
	APIBaseURL  string `json:"api_base_url"`
	ExpiresIn   int    `json:"expires_in"`
	WorkerEpoch int    `json:"worker_epoch"`
}

type EventBatchRequest struct {
	WorkerEpoch int `json:"worker_epoch"`
	Events      []struct {
		Payload   json.RawMessage `json:"payload"`
		Ephemeral bool            `json:"ephemeral"`
	} `json:"events"`
}

type InternalEventBatchRequest struct {
	WorkerEpoch int `json:"worker_epoch"`
	Events      []struct {
		Payload      json.RawMessage `json:"payload"`
		IsCompaction bool            `json:"is_compaction"`
		AgentID      string          `json:"agent_id,omitempty"`
	} `json:"events"`
}

type WorkerStateRequest struct {
	WorkerStatus          string          `json:"worker_status"`
	WorkerEpoch           int             `json:"worker_epoch"`
	ExternalMetadata      json.RawMessage `json:"external_metadata,omitempty"`
	RequiresActionDetails json.RawMessage `json:"requires_action_details,omitempty"`
}

type HeartbeatRequest struct {
	WorkerEpoch int `json:"worker_epoch"`
}
```

- [ ] **Step 3: Create server.go with health endpoint**

```go
package ccbroker

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	config Config
	store  *Store
	sse    *SSEBroker
	dedup  *DedupRegistry
	logger *slog.Logger
}

func NewServer(cfg Config, store *Store) *Server {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	return &Server{
		config: cfg,
		store:  store,
		sse:    NewSSEBroker(),
		dedup:  NewDedupRegistry(),
		logger: logger,
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Session lifecycle (no auth for now — internal service)
	r.Post("/v1/sessions", s.handleCreateSession)
	r.Post("/v1/sessions/{sessionId}/bridge", s.handleBridge)

	// Worker endpoints (JWT auth)
	r.Route("/v1/sessions/{sessionId}/worker", func(r chi.Router) {
		r.Use(s.workerAuthMiddleware)
		r.Get("/events/stream", s.handleWorkerEventStream)
		r.Post("/events", s.handleWorkerEvents)
		r.Post("/internal-events", s.handleWorkerInternalEvents)
		r.Get("/internal-events", s.handleGetInternalEvents)
		r.Put("/", s.handleWorkerState)
		r.Post("/heartbeat", s.handleWorkerHeartbeat)
	})

	return r
}
```

Note: `SSEBroker`, `DedupRegistry`, and `Store` types don't exist yet. Create stubs so it compiles:

```go
// Temporary stubs (will be replaced in subsequent tasks)
type SSEBroker struct{}
func NewSSEBroker() *SSEBroker { return &SSEBroker{} }
type DedupRegistry struct{}
func NewDedupRegistry() *DedupRegistry { return &DedupRegistry{} }
type Store struct{ io.Closer }
```

And stub handler methods:
```go
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleWorkerEventStream(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleWorkerEvents(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleWorkerInternalEvents(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleGetInternalEvents(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleWorkerState(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) handleWorkerHeartbeat(w http.ResponseWriter, r *http.Request) { w.WriteHeader(501) }
func (s *Server) workerAuthMiddleware(next http.Handler) http.Handler { return next }
```

- [ ] **Step 4: Create main.go**

Follow the same pattern as `cmd/executor-registry/main.go` but with `ccbroker` package and `CCBROKER_*` env vars.

- [ ] **Step 5: Verify it compiles**

Run: `cd /root/agentserver && go build ./cmd/cc-broker/`

- [ ] **Step 6: Commit**

```bash
git add cmd/cc-broker/ internal/ccbroker/
git commit -m "feat(cc-broker): add project skeleton with config, server, models, and entry point"
```

---

## Task 2: Database Store (Sessions + Events + Workers)

**Files:**
- Create: `internal/ccbroker/migrations/001_initial.sql`
- Replace: `internal/ccbroker/store.go` (full implementation)

The cc-broker uses the **same PostgreSQL database** and **same tables** as agentserver (`agent_sessions`, `agent_session_events`, `agent_session_workers`, `agent_session_internal_events`). The migration uses `CREATE TABLE IF NOT EXISTS` to be idempotent — tables already exist if agentserver has run its migrations.

- [ ] **Step 1: Create migration**

```sql
-- Idempotent: tables may already exist from agentserver migrations
CREATE TABLE IF NOT EXISTS agent_sessions (
    id            TEXT PRIMARY KEY,
    sandbox_id    TEXT,
    workspace_id  TEXT NOT NULL,
    title         TEXT,
    status        TEXT DEFAULT 'active',
    epoch         INTEGER DEFAULT 0,
    external_id   TEXT,
    source        TEXT DEFAULT 'agent',
    tags          TEXT[],
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW(),
    archived_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS agent_session_events (
    id            BIGSERIAL PRIMARY KEY,
    session_id    TEXT NOT NULL,
    event_id      TEXT NOT NULL UNIQUE,
    event_type    TEXT DEFAULT 'client_event',
    source        TEXT DEFAULT 'client',
    epoch         INTEGER,
    payload       JSONB NOT NULL,
    ephemeral     BOOLEAN DEFAULT FALSE,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_session_events_session_id ON agent_session_events(session_id, id);

CREATE TABLE IF NOT EXISTS agent_session_workers (
    session_id              TEXT NOT NULL,
    epoch                   INTEGER NOT NULL,
    state                   TEXT DEFAULT 'idle',
    external_metadata       JSONB,
    requires_action_details JSONB,
    last_heartbeat_at       TIMESTAMPTZ,
    registered_at           TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (session_id, epoch)
);

CREATE TABLE IF NOT EXISTS agent_session_internal_events (
    id            BIGSERIAL PRIMARY KEY,
    session_id    TEXT NOT NULL,
    event_type    TEXT,
    payload       JSONB,
    is_compaction BOOLEAN DEFAULT FALSE,
    agent_id      TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_session_internal_events_session ON agent_session_internal_events(session_id, id);
```

- [ ] **Step 2: Implement store.go**

Store methods needed (follow the exact signatures from `/root/agentserver/internal/db/agent_sessions.go`):

```go
type Store struct { *sql.DB }

// Session lifecycle
func (s *Store) CreateSession(ctx, id, workspaceID, title, source string, externalID *string) error
func (s *Store) GetSession(ctx, id string) (*Session, error)
func (s *Store) BumpSessionEpoch(ctx, id string) (newEpoch int, err error)

// Events (append-only log)
func (s *Store) InsertEvents(ctx, sessionID string, epoch int, events []EventInput) ([]InsertedEvent, error)
func (s *Store) GetEventsSince(ctx, sessionID string, sinceSeqNum int64, limit int) ([]SessionEvent, error)

// Internal events
func (s *Store) InsertInternalEvents(ctx, sessionID string, events []InternalEventInput) error
func (s *Store) GetInternalEventsSince(ctx, sessionID string, sinceID int64, limit int) ([]SessionEvent, error)

// Worker state
func (s *Store) UpsertWorker(ctx, sessionID string, epoch int) error
func (s *Store) UpdateWorkerState(ctx, sessionID string, epoch int, state string, metadata, actionDetails json.RawMessage) error
func (s *Store) UpdateWorkerHeartbeat(ctx, sessionID string, epoch int) error
func (s *Store) GetWorker(ctx, sessionID string, epoch int) (*Worker, error)
```

Type `EventInput`:
```go
type EventInput struct {
    EventID   string          // extracted from payload "uuid" field
    Payload   json.RawMessage
    Ephemeral bool
}

type InternalEventInput struct {
    EventType    string
    Payload      json.RawMessage
    IsCompaction bool
    AgentID      string
}

type InsertedEvent struct {
    SeqNum  int64
    EventID string
}
```

Key: `InsertEvents` must use `ON CONFLICT (event_id) DO NOTHING RETURNING id` for deduplication and return the sequence numbers.

- [ ] **Step 3: Verify it compiles**

- [ ] **Step 4: Commit**

```bash
git add internal/ccbroker/migrations/ internal/ccbroker/store.go
git commit -m "feat(cc-broker): add database store with session, event, and worker operations"
```

---

## Task 3: JWT + Dedup + SSE Broker

**Files:**
- Create: `internal/ccbroker/jwt.go`
- Create: `internal/ccbroker/dedup.go`
- Create: `internal/ccbroker/sse.go`

These are utility components used by the bridge handlers.

- [ ] **Step 1: Create jwt.go**

HMAC-SHA256 JWT implementation. Follow the pattern from `/root/agentserver/internal/bridge/jwt.go`:

```go
func IssueWorkerJWT(secret []byte, claims WorkerJWTClaims) (string, error)
func ValidateWorkerJWT(secret []byte, token string) (*WorkerJWTClaims, error)
```

JWT format: `base64url(header).base64url(payload).base64url(hmac_sha256(header.payload, secret))`
Header: `{"alg":"HS256","typ":"JWT"}`

- [ ] **Step 2: Create dedup.go**

Bounded UUID set with FIFO eviction (capacity 2000). Follow `/root/agentserver/internal/bridge/dedup.go`:

```go
type BoundedUUIDSet struct { ... }
func NewBoundedUUIDSet(capacity int) *BoundedUUIDSet
func (s *BoundedUUIDSet) Add(uuid string) bool   // Returns false if duplicate
func (s *BoundedUUIDSet) Has(uuid string) bool

// Per-session dedup registry
type DedupRegistry struct { ... }
func NewDedupRegistry() *DedupRegistry
func (r *DedupRegistry) GetOrCreate(sessionID string) *BoundedUUIDSet
```

- [ ] **Step 3: Create sse.go**

In-memory SSE pub/sub broker. Follow `/root/agentserver/internal/bridge/sse.go`:

```go
type SSESubscriber struct {
    Ch   chan *StreamClientEvent   // buffered, capacity 256
    done chan struct{}
}

type SSEBroker struct { ... }
func NewSSEBroker() *SSEBroker
func (b *SSEBroker) Subscribe(sessionID string) *SSESubscriber
func (b *SSEBroker) Unsubscribe(sessionID string, sub *SSESubscriber)
func (b *SSEBroker) Publish(sessionID string, event *StreamClientEvent)
```

Publish with backpressure: if subscriber channel full, close subscriber.

- [ ] **Step 4: Remove stubs from server.go** (SSEBroker, DedupRegistry placeholders)

- [ ] **Step 5: Verify it compiles**

- [ ] **Step 6: Commit**

```bash
git add internal/ccbroker/jwt.go internal/ccbroker/dedup.go internal/ccbroker/sse.go internal/ccbroker/server.go
git commit -m "feat(cc-broker): add JWT, deduplication, and SSE broker utilities"
```

---

## Task 4: Session + Bridge Handlers

**Files:**
- Create: `internal/ccbroker/handler_session.go`
- Create: `internal/ccbroker/handler_bridge.go`

- [ ] **Step 1: Create handler_session.go**

`handleCreateSession` — POST /v1/sessions

Accept: `{"workspace_id": "...", "title": "...", "source": "...", "external_id": "..."}`
Generate session ID: `"cse_" + uuid.NewString()`
Call `store.CreateSession(...)`
Return 201: `{"session": {"id": "cse_..."}}`

- [ ] **Step 2: Create handler_bridge.go**

`handleBridge` — POST /v1/sessions/{sessionId}/bridge

1. Get session from DB
2. Bump epoch atomically: `store.BumpSessionEpoch(sessionID)` → newEpoch
3. Upsert worker record: `store.UpsertWorker(sessionID, newEpoch)`
4. Issue worker JWT: `IssueWorkerJWT(secret, claims)`
5. Build api_base_url from request scheme/host
6. Return: `{"worker_jwt": "...", "api_base_url": "...", "expires_in": 86400, "worker_epoch": N}`

- [ ] **Step 3: Verify it compiles**

- [ ] **Step 4: Commit**

```bash
git add internal/ccbroker/handler_session.go internal/ccbroker/handler_bridge.go
git commit -m "feat(cc-broker): add session creation and worker bridge attachment handlers"
```

---

## Task 5: Worker Auth Middleware + Event Handlers

**Files:**
- Create: `internal/ccbroker/middleware.go`
- Create: `internal/ccbroker/handler_events.go`

- [ ] **Step 1: Create middleware.go**

`workerAuthMiddleware` — extract JWT from `Authorization: Bearer {token}`, validate, set context values (sessionID, workspaceID, epoch).

Context key helpers:
```go
func SessionIDFromContext(ctx) string
func WorkspaceIDFromContext(ctx) string
func EpochFromContext(ctx) int
```

- [ ] **Step 2: Create handler_events.go**

Two handlers:

`handleWorkerEventStream` — GET .../worker/events/stream
1. Parse `from_sequence_num` from query or `Last-Event-ID` header
2. Replay events from DB (limit 1000)
3. Subscribe to SSE broker
4. Stream: replayed events + live events + keepalive every 15s
5. SSE format: `event: client_event\nid: {seq}\ndata: {json}\n\n`

`handleWorkerEvents` — POST .../worker/events
1. Validate epoch matches session epoch (409 if not)
2. Limit 100 events per batch
3. Extract event_id (uuid) from each payload
4. Dedup via BoundedUUIDSet
5. Insert into DB
6. Publish to SSE broker
7. Return 200

- [ ] **Step 3: Verify it compiles**

- [ ] **Step 4: Commit**

```bash
git add internal/ccbroker/middleware.go internal/ccbroker/handler_events.go
git commit -m "feat(cc-broker): add worker auth middleware and event stream/batch handlers"
```

---

## Task 6: Worker State + Heartbeat + Internal Events

**Files:**
- Create: `internal/ccbroker/handler_worker.go`
- Create: `internal/ccbroker/handler_internal_events.go`

- [ ] **Step 1: Create handler_worker.go**

`handleWorkerState` — PUT .../worker
Parse WorkerStateRequest, validate epoch, call `store.UpdateWorkerState(...)`

`handleWorkerHeartbeat` — POST .../worker/heartbeat
Parse HeartbeatRequest, validate epoch, call `store.UpdateWorkerHeartbeat(...)`

- [ ] **Step 2: Create handler_internal_events.go**

`handleWorkerInternalEvents` — POST .../worker/internal-events
Parse InternalEventBatchRequest, validate epoch, extract event_type from each payload, call `store.InsertInternalEvents(...)`

`handleGetInternalEvents` — GET .../worker/internal-events?from_sequence_num=0
Call `store.GetInternalEventsSince(...)`, return JSON array.

- [ ] **Step 3: Verify it compiles**

- [ ] **Step 4: Commit**

```bash
git add internal/ccbroker/handler_worker.go internal/ccbroker/handler_internal_events.go
git commit -m "feat(cc-broker): add worker state, heartbeat, and internal events handlers"
```

---

## Task 7: Dockerfile + docker-compose + Integration Test

**Files:**
- Create: `Dockerfile.cc-broker`
- Modify: `docker-compose.yml`
- Create: `internal/ccbroker/integration_test.go`

- [ ] **Step 1: Create Dockerfile.cc-broker**

Same pattern as `Dockerfile.executor-registry`:
```dockerfile
FROM golang:1.26-trixie AS builder
# ...
RUN CGO_ENABLED=0 go build -o cc-broker ./cmd/cc-broker
# ...
EXPOSE 8085
ENTRYPOINT ["cc-broker"]
```

- [ ] **Step 2: Add to docker-compose.yml**

```yaml
  cc-broker:
    build:
      context: .
      dockerfile: Dockerfile.cc-broker
    environment:
      CCBROKER_DATABASE_URL: postgres://agentserver:agentserver@postgres:5432/agentserver?sslmode=disable
      CCBROKER_PORT: "8085"
      CCBROKER_JWT_SECRET: "change-me-to-a-32-char-secret-key"
      CCBROKER_LOG_LEVEL: info
    ports:
      - "8085:8085"
    depends_on:
      - postgres
    restart: unless-stopped
```

- [ ] **Step 3: Create integration_test.go**

Tests:
- `TestCreateSessionAndBridge` — create session → attach bridge → verify epoch bump + JWT
- `TestEventBatchAndReplay` — write events → SSE replay → verify sequence + content
- `TestEpochMismatch` — write events with wrong epoch → expect 409
- `TestHeartbeat` — send heartbeat → verify 200

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/cc-broker/ && go vet ./internal/ccbroker/`

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.cc-broker docker-compose.yml internal/ccbroker/integration_test.go
git commit -m "feat(cc-broker): add Dockerfile, docker-compose, and integration tests"
```

---

## Verification

To verify cc-broker Bridge API end-to-end:

1. **Start services:**
   ```bash
   docker compose up -d postgres cc-broker
   ```

2. **Create session:**
   ```bash
   curl -X POST http://localhost:8085/v1/sessions \
     -H 'Content-Type: application/json' \
     -d '{"workspace_id":"ws_001","title":"Test Session"}'
   ```
   Expected: 201 with `{"session":{"id":"cse_..."}}`

3. **Attach bridge:**
   ```bash
   curl -X POST http://localhost:8085/v1/sessions/{session_id}/bridge
   ```
   Expected: 200 with `{"worker_jwt":"...","api_base_url":"...","worker_epoch":1}`

4. **Write events:**
   ```bash
   curl -X POST http://localhost:8085/v1/sessions/{session_id}/worker/events \
     -H 'Authorization: Bearer {worker_jwt}' \
     -H 'Content-Type: application/json' \
     -d '{"worker_epoch":1,"events":[{"payload":{"uuid":"evt_1","type":"user","content":"hello"},"ephemeral":false}]}'
   ```
   Expected: 200

5. **Replay via SSE:**
   ```bash
   curl -N http://localhost:8085/v1/sessions/{session_id}/worker/events/stream \
     -H 'Authorization: Bearer {worker_jwt}'
   ```
   Expected: SSE stream with the event from step 4

6. **Heartbeat:**
   ```bash
   curl -X POST http://localhost:8085/v1/sessions/{session_id}/worker/heartbeat \
     -H 'Authorization: Bearer {worker_jwt}' \
     -H 'Content-Type: application/json' \
     -d '{"worker_epoch":1}'
   ```
   Expected: 200
