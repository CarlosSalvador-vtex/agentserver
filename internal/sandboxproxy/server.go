package sandboxproxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/sbxstore"
	"github.com/agentserver/agentserver/internal/tunnel"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type contextKey string

const matchedDomainKey contextKey = "matchedBaseDomain"

// matchedBaseDomain returns the base domain that matched the current request,
// falling back to the first configured domain.
func (s *Server) matchedBaseDomain(r *http.Request) string {
	if d, ok := r.Context().Value(matchedDomainKey).(string); ok {
		return d
	}
	if len(s.BaseDomains) > 0 {
		return s.BaseDomains[0]
	}
	return ""
}

// Server is the sandbox-proxy HTTP server that handles subdomain traffic
// proxying and WebSocket tunnel connections.
type Server struct {
	Auth                  *auth.Auth
	DB                    *db.DB
	Sandboxes             *sbxstore.Store
	TunnelRegistry        *tunnel.Registry
	BaseDomains           []string
	OpenclawSubdomainPrefix string
	HermesSubdomainPrefix   string
	// AgentserverFallback reverse-proxies tenant slug hosts (non-claw, non-hermes)
	// to the agentserver service. nil disables fallthrough.
	AgentserverFallback *httputil.ReverseProxy

	activityMu   sync.Mutex
	activityLast map[string]time.Time
}

// New creates a new sandbox-proxy server.
func New(cfg Config, authSvc *auth.Auth, database *db.DB, sandboxStore *sbxstore.Store, tunnelReg *tunnel.Registry) *Server {
	s := &Server{
		Auth:                    authSvc,
		DB:                      database,
		Sandboxes:               sandboxStore,
		TunnelRegistry:          tunnelReg,
		BaseDomains:             cfg.BaseDomains,
		OpenclawSubdomainPrefix: cfg.OpenclawSubdomainPrefix,
		HermesSubdomainPrefix:   cfg.HermesSubdomainPrefix,
		activityLast:            make(map[string]time.Time),
	}
	if cfg.AgentserverUpstream != "" {
		if u, err := url.Parse(cfg.AgentserverUpstream); err == nil && u.Host != "" {
			rp := httputil.NewSingleHostReverseProxy(u)
			// Preserve the original Host header so agentserver can extract the
			// workspace slug from the subdomain (e.g. empresa-a.<base>).
			origDirector := rp.Director
			rp.Director = func(req *http.Request) {
				origHost := req.Host
				origDirector(req)
				req.Host = origHost
			}
			s.AgentserverFallback = rp
		}
	}
	return s
}

// throttledActivity updates activity at most once per 30 seconds per sandbox.
func (s *Server) throttledActivity(sandboxID string) {
	s.activityMu.Lock()
	last, ok := s.activityLast[sandboxID]
	now := time.Now()
	if ok && now.Sub(last) < 30*time.Second {
		s.activityMu.Unlock()
		return
	}
	s.activityLast[sandboxID] = now
	s.activityMu.Unlock()
	s.Sandboxes.UpdateActivity(sandboxID)
}

// Router returns the HTTP handler for the sandbox-proxy service.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	if len(s.BaseDomains) > 0 {
		r.Use(func(next http.Handler) http.Handler {
			type domainEntry struct {
				suffix string
				domain string
			}
			entries := make([]domainEntry, len(s.BaseDomains))
			for i, d := range s.BaseDomains {
				entries[i] = domainEntry{suffix: "." + d, domain: d}
			}
			clawPrefix := s.OpenclawSubdomainPrefix + "-"
			hermesPrefix := s.HermesSubdomainPrefix + "-"
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host := r.Host
				if idx := strings.LastIndex(host, ":"); idx != -1 {
					host = host[:idx]
				}
				for _, e := range entries {
					if !strings.HasSuffix(host, e.suffix) {
						continue
					}
					sub := strings.TrimSuffix(host, e.suffix)
					ctx := context.WithValue(r.Context(), matchedDomainKey, e.domain)
					r = r.WithContext(ctx)

					if strings.HasPrefix(sub, clawPrefix) {
						sandboxID := sub[len(clawPrefix):]
						s.handleOpenclawSubdomainProxy(w, r, sandboxID)
						return
					}
					if strings.HasPrefix(sub, hermesPrefix) {
						sandboxID := sub[len(hermesPrefix):]
						s.handleHermesSubdomainProxy(w, r, sandboxID)
						return
					}
					// Tenant slug host (non-sandbox subdomain): reverse-proxy to
					// agentserver so workspace-aware login/UI serves these hosts.
					if sub != "" && s.AgentserverFallback != nil {
						s.AgentserverFallback.ServeHTTP(w, r)
						return
					}
				}
				next.ServeHTTP(w, r)
			})
		})
	}

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.HandleFunc("/api/tunnel/{sandboxId}", s.handleTunnel)

	return r
}
