package codexexecgateway

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/handlers"
	"github.com/agentserver/agentserver/internal/codexexecgateway/relay"
	sdkpkg "github.com/agentserver/agentserver/internal/codexexecgateway/sdk"
	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/nameresolver"
	"github.com/agentserver/agentserver/internal/envtools/processes"
	"github.com/agentserver/agentserver/internal/envtools/tools"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server bundles the chi router with its dependencies.
// Server wires the routes for codex-exec-gateway. Production must
// always be constructed with a real *Store; tests that exercise only
// auth-rejection paths may use newServerNoStoreForTesting.
type Server struct {
	config        Config
	store         *Store
	registry      *ConnRegistry
	revoked       *RevokedSet
	relayRegistry *relay.Registry // nil if PublicHTTPSBaseURL unset (dev/disabled)
	sdkServer     *sdkpkg.Server  // nil if AgentserverInternalURL unset (dev/disabled)
	sdkSessions   *processes.Manager
	logger        *slog.Logger
}

// NewServer is the production constructor. Refuses a nil store so a
// misconfigured deploy can't silently bypass the /bridge ownership
// check (which falls back to "skip + warn" when store is nil for the
// sake of test wiring).
func NewServer(cfg Config, store *Store) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if store == nil {
		return nil, fmt.Errorf("codexexecgateway: store is required")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	var relayReg *relay.Registry
	if cfg.PublicHTTPSBaseURL != "" {
		relayReg = relay.NewRegistry(cfg.RelayMaxPerWorkspace, cfg.RelayDefaultTTL, logger)
	}

	registry := NewConnRegistry()

	// Build the SDK REST surface.  Enabled when AgentserverInternalURL is
	// set; disabled (nil sdkServer) in dev/test environments where the
	// agentserver validate-proxy-token endpoint is not available.
	var sdkSrv *sdkpkg.Server
	var sdkSessions *processes.Manager
	if cfg.AgentserverInternalURL != "" {
		sdkSessions = processes.NewManager(30 * time.Minute)
		sdkSessions.Run() // starts the idle-session GC goroutine

		sdkAuth := sdkpkg.NewProxyTokenAuth(
			cfg.AgentserverInternalURL,
			cfg.AgentserverInternalSecret,
			5*time.Minute,
			30*time.Second,
		)

		// Bridge pool: dial the gateway's own /bridge/<exe_id> over ws.
		// PublicWSBaseURL is "wss://…" in production; fall back to the
		// loopback if only SelfHTTPBaseURL is set (dev mode).
		bridgeBaseURL := cfg.PublicWSBaseURL
		if bridgeBaseURL == "" && cfg.SelfHTTPBaseURL != "" {
			bridgeBaseURL = strings.Replace(cfg.SelfHTTPBaseURL, "https://", "wss://", 1)
			bridgeBaseURL = strings.Replace(bridgeBaseURL, "http://", "ws://", 1)
		}
		sdkPool := bridge.NewPool(bridgeBaseURL+"/bridge", "", logger)

		// Name resolver: calls the gateway's own /internal/sdk/connected
		// loopback with X-Loopback-Token == InternalSharedSecret so env-name
		// → exe_id resolution works for tool calls without an extra HTTP hop.
		var resolverURL string
		if cfg.SelfHTTPBaseURL != "" {
			resolverURL = strings.TrimRight(cfg.SelfHTTPBaseURL, "/") + "/internal/sdk/connected"
		}
		sdkResolver := nameresolver.NewResolver(resolverURL, cfg.InternalSharedSecret, logger)

		// Tool registry keyed by the name the SDK sends in tool/call requests.
		// UnifiedExecTool owns a SessionStore shared with the process manager;
		// for the SDK path the processes.Manager handles session lifecycle
		// (sdkSessions) while the tools.SessionStore handles the bridge-level
		// write_stdin/read_output/terminate sub-calls.
		unifiedSessions := tools.NewSessionStore()
		relayClient := bridge.NewRelayClient(cfg.PublicHTTPSBaseURL, cfg.InternalSharedSecret, "", logger)

		toolRegistry := map[string]tools.Tool{
			"shell":        tools.NewShellTool(sdkPool, sdkResolver),
			"read_file":    tools.NewReadFileTool(sdkPool, sdkResolver),
			"apply_patch":  tools.NewApplyPatchTool(sdkPool, sdkResolver),
			"copy_path":    tools.NewCopyPathTool(sdkPool, sdkResolver, relayClient),
			"exec_command": tools.NewUnifiedExecTool(sdkPool, unifiedSessions, sdkResolver),
		}

		sdkSrv = &sdkpkg.Server{
			Auth:     sdkAuth,
			Pool:     sdkPool,
			Resolver: sdkResolver,
			Sessions: sdkSessions,
			Registry: sdkConnectedAdapter{store: store, registry: registry},
			Tools:    toolRegistry,
		}
		logger.Info("sdk REST surface enabled", "agentserver_url", cfg.AgentserverInternalURL)
	} else {
		logger.Warn("sdk REST surface disabled: CXG_AGENTSERVER_INTERNAL_URL not set")
	}

	return &Server{
		config:        cfg,
		store:         store,
		registry:      registry,
		revoked:       NewRevokedSet(10000),
		relayRegistry: relayReg,
		sdkServer:     sdkSrv,
		sdkSessions:   sdkSessions,
		logger:        logger,
	}, nil
}

// Stop releases background goroutines (currently: the SDK session GC).
// Call from main's signal handler after http.Server.Shutdown returns.
func (s *Server) Stop() {
	if s.sdkSessions != nil {
		s.sdkSessions.Stop()
	}
}

// newServerNoStoreForTesting constructs a Server with a nil store. ONLY
// for tests in this package that exercise routes which fail before
// reaching the store. The /bridge handler logs an explicit warning and
// skips the workspace-ownership check when store is nil.
func newServerNoStoreForTesting(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	var relayReg *relay.Registry
	if cfg.PublicHTTPSBaseURL != "" {
		relayReg = relay.NewRegistry(cfg.RelayMaxPerWorkspace, cfg.RelayDefaultTTL, logger)
	}
	return &Server{
		config:        cfg,
		store:         nil,
		registry:      NewConnRegistry(),
		revoked:       NewRevokedSet(10000),
		relayRegistry: relayReg,
		logger:        logger,
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

	// HTTP relay public endpoints — ticket Bearer is auth; no other
	// middleware. Registered even when relayRegistry is nil so the
	// handlers can return a clear 404 to misconfigured callers.
	r.Put("/relay/{ticket}", s.handleRelayPut)
	r.Get("/relay/{ticket}", s.handleRelayGet)

	// Upstream codex `exec-server --remote` compat: clients POST here
	// with bearer auth, get back the ws URL above.
	r.Post("/cloud/executor/{exe_id}/register", handlers.CloudRegister(s.store, s.config.PublicWSBaseURL))

	// *Store satisfies handlers.Store, handlers.BindingStore, and
	// handlers.InternalConnectedStore directly — no adapter needed because
	// all three interfaces now use execmodel types, which *Store also uses
	// (via the type aliases in models.go).
	r.Route("/api/codex-exec", func(r chi.Router) {
		r.Use(handlers.RequireAgentserverSecret(s.config.AgentserverInternalSecret))
		r.Post("/register", handlers.Register(s.store))
		// Used by agentserver to clean up an orphaned executor after a
		// register-then-bind failure (v0.54.2). CASCADE on
		// workspace_executors handles any leftover binding rows.
		r.Delete("/executors/{exe_id}", handlers.DeleteExecutor(s.store))
		r.Route("/workspaces/{wid}/executors", func(r chi.Router) {
			r.Post("/", handlers.PostBinding(s.store))
			r.Get("/", handlers.ListBinding(s.store))
			r.Delete("/{exe_id}", handlers.DeleteBinding(s.store))
		})
	})

	r.Route("/api/exec-gateway", func(r chi.Router) {
		r.Use(handlers.RequireSharedSecret(s.config.InternalSharedSecret))
		r.Get("/connected", handlers.Connected(s.store, s.registry))
		r.Post("/revoke-turn", handlers.RevokeTurn(s.revoked))
		r.Post("/relay/create", s.handleRelayCreate)
	})

	// Loopback endpoint for the SDK name-resolver (nameresolver.Resolver).
	// Called by the in-process bridge.Pool/tools when they need to map an
	// environment name → exe_id.  Auth: X-Loopback-Token == InternalSharedSecret
	// (same value, different header than RequireSharedSecret's Bearer check).
	r.Get("/internal/sdk/connected", s.handleSDKConnectedLoopback)

	// SDK REST surface (/api/sdk/*). Mounted last so SDK routes don't
	// shadow any existing paths.
	if s.sdkServer != nil {
		s.sdkServer.Mount(r)
	}

	return r
}

// handleSDKConnectedLoopback serves GET /internal/sdk/connected for the
// SDK name-resolver. It verifies X-Loopback-Token == InternalSharedSecret,
// reads workspace_id from the query string, and returns the connected
// executor list in the same JSON shape as /api/exec-gateway/connected.
func (s *Server) handleSDKConnectedLoopback(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get("X-Loopback-Token")
	if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.config.InternalSharedSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := r.URL.Query().Get("workspace_id")
	if wid == "" {
		http.Error(w, "workspace_id required", http.StatusBadRequest)
		return
	}
	rows, err := s.store.ConnectedExecutorsForWorkspace(r.Context(), wid, s.registry.ConnectedIDs())
	if err != nil {
		s.logger.Warn("sdk loopback connected: store error", "workspace_id", wid, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []ConnectedExecutor{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// (real ConnRegistry lives in registry.go; real RevokedSet in revocation.go)
