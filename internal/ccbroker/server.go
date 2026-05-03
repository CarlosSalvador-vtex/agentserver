package ccbroker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/agentserver/agentserver/internal/ccbroker/tools"
)

// storer abstracts the database operations needed by the Server. The concrete
// implementation is *Store (backed by Postgres); tests inject a fakeStore.
type storer interface {
	GetSession(ctx context.Context, id string) (*Session, error)
	CreateSession(ctx context.Context, id, workspaceID, title, source string, externalID *string) error
	GetSessionEpoch(ctx context.Context, sessionID string) (int, error)
	InsertEvents(ctx context.Context, sessionID string, epoch int, events []EventInput) ([]InsertedEvent, error)
}

type Server struct {
	config   Config
	store    storer
	sse      *SSEBroker
	turnLock *TurnLock
	logger   *slog.Logger
	gate     *tools.Gate // permission gate, initialized in NewServer

	// Task 12: TUI control endpoints
	activeTurns  *activeTurnRegistry
	compactQueue *compactQueue
}

func NewServer(cfg Config, store *Store) *Server {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	s := &Server{
		config:   cfg,
		store:    store,
		sse:      NewSSEBroker(),
		turnLock: NewTurnLock(),
		logger:   logger,
	}
	s.gate = tools.NewGate(func(sid string, e tools.Event) {
		// emit-to-SSE wiring — for Phase 1 Task 7, leave as a noop logger.
		// Task 12 will wire this to the SSE broadcast path.
		s.logger.Debug("permission event (no SSE wiring yet)",
			"session_id", sid, "type", e.Type, "pid", e.PermissionID)
	})
	s.activeTurns = newActiveTurnRegistry()
	s.compactQueue = newCompactQueue()
	return s
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// External API
	r.Post("/api/turns", s.handleProcessTurn)

	// Session lifecycle
	r.Post("/v1/sessions", s.handleCreateSession)

	// Task 12: TUI control endpoints
	r.Post("/api/sessions/{sid}/turns/{tid}/cancel",       s.handleCancelTurn)
	r.Post("/api/sessions/{sid}/permissions/{pid}/decide", s.handleDecidePermission)
	r.Post("/api/sessions/{sid}/compact",                  s.handleCompactNow)
	r.Get("/api/sessions/{sid}/turns/active",              s.handleGetActiveTurn)

	return r
}

// Helpers
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
