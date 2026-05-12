package codexexecgateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/agentserver/agentserver/internal/codexexecgateway/handlers"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server bundles the chi router with its dependencies.
// store may be nil for smoke tests that don't exercise DB paths; registry and revoked are always constructed.
type Server struct {
	config   Config
	store    *Store
	registry *ConnRegistry
	revoked  *RevokedSet
	logger   *slog.Logger
}

func NewServer(cfg Config, store *Store) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	return &Server{
		config:   cfg,
		store:    store,
		registry: NewConnRegistry(),
		revoked:  NewRevokedSet(10000),
		logger:   logger,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Get("/codex-exec/{exe_id}", s.handleInbound)
	r.Get("/bridge/{exe_id}", s.handleBridge)

	// *Store satisfies handlers.Store, handlers.BindingStore, and
	// handlers.InternalConnectedStore directly — no adapter needed because
	// all three interfaces now use execmodel types, which *Store also uses
	// (via the type aliases in models.go).
	r.Post("/api/codex-exec/register", handlers.Register(s.store))

	r.Route("/api/codex-exec/workspaces/{wid}/executors", func(r chi.Router) {
		r.Post("/", handlers.PostBinding(s.store))
		r.Get("/", handlers.ListBinding(s.store))
		r.Delete("/{exe_id}", handlers.DeleteBinding(s.store))
	})

	r.Route("/api/exec-gateway", func(r chi.Router) {
		r.Use(handlers.RequireSharedSecret(s.config.InternalSharedSecret))
		r.Get("/connected", handlers.Connected(s.store, s.registry))
		r.Post("/revoke-turn", handlers.RevokeTurn(s.revoked))
	})

	// More routes added in later tasks.
	return r
}

// (real ConnRegistry lives in registry.go; real RevokedSet in revocation.go)
