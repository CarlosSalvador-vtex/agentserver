package codexappgateway

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

// handleInternalConnected serves GET /internal/connected, called by the
// env-mcp child process running inside the same pod over loopback. It
// returns the same payload as codex-exec-gateway's /api/exec-gateway/
// connected, scoped to the workspace identified by the supplied
// X-Loopback-Token (issued per codex app-server spawn).
//
// Auth has two independent gates:
//   - The HTTP listener is the pod-wide gateway. We additionally reject
//     any RemoteAddr that's not a loopback IP, so even if the gateway
//     accidentally exposes this route over the LB it can't be reached.
//   - The X-Loopback-Token resolves to a workspace_id via the supervisor's
//     per-spawn token map (constant-time compare).
//
// Returns:
//   - 200 + JSON array on success (may be empty)
//   - 401 if the token header is missing or doesn't match any live spawn
//   - 403 if RemoteAddr is not loopback
//   - 500 on internal/connected upstream failure
func (s *Server) handleInternalConnected(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		s.logger.Warn("internal/connected: rejecting non-loopback caller", "remote", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tok := r.Header.Get("X-Loopback-Token")
	if tok == "" {
		http.Error(w, "missing X-Loopback-Token", http.StatusUnauthorized)
		return
	}
	wid, ok := s.sup.LookupWorkspaceForLoopbackToken(tok)
	if !ok {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	list, err := s.execClient.Connected(r.Context(), wid)
	if err != nil {
		s.logger.Warn("internal/connected: upstream fetch failed", "workspace_id", wid, "err", err)
		http.Error(w, "list", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []execmodel.ConnectedExecutor{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// isLoopbackRemote reports whether addr's host portion is a loopback IP.
// addr is in the net.RemoteAddr format "ip:port" (or "[ipv6]:port").
func isLoopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
