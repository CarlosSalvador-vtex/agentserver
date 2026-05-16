package codexexecgateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/wsbridge"
	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"
)

// handleBridge accepts a ws connection from an env-mcp child binary
// (codex-app-gateway env-mcp ...) and pairs it with the registered
// inbound /codex-exec/{exe_id} conn. Auth is verified once at connect
// time (cap-token verify BEFORE registry lookup so unauthenticated callers
// don't learn which exe_ids exist); thereafter forwarding is unconditional
// until either side closes.
//
// HTTP error codes:
//   401 — bad/expired cap token, or revoked turn_id
//   403 — URL exe_id not in token allow-list
//   503 — exe_id not in registry (no inbound connection)
func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
	exeID := chi.URLParam(r, "exe_id")
	// /bridge is only ever dialed by the env-mcp child binary, which sends
	// Authorization: Bearer <cap-token>. No URL-query fallback.
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		http.Error(w, "missing Bearer", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authz, "Bearer ")
	if exeID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// 1. Verify cap token BEFORE registry lookup to prevent exe_id enumeration.
	payload, err := VerifyCapabilityToken(token, s.config.CapTokenHMACSecret)
	if err != nil {
		s.logger.Warn("bridge: auth failed", "exe_id", exeID, "error", err, "remote", r.RemoteAddr)
		switch {
		case errors.Is(err, ErrExpired):
			http.Error(w, "token expired", http.StatusUnauthorized)
		case errors.Is(err, ErrBadSignature), errors.Is(err, ErrMalformed):
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
		return
	}

	// 2. Check revocation — TurnID is only available from the decoded
	// payload, so this must come after signature verification. Cheap
	// in-memory check; runs before the DB ownership query.
	if s.revoked.Contains(payload.TurnID) {
		s.logger.Warn("bridge: rejected revoked turn", "exe_id", exeID, "turn_id", payload.TurnID)
		http.Error(w, "turn revoked", http.StatusUnauthorized)
		return
	}

	// 3. Workspace ownership check. Cap tokens are workspace-scoped
	// (2026-05-16 redesign); /bridge enforces that the URL exe_id is
	// bound to the token's workspace via the workspace_executors table.
	// Production wiring always has a store; the nil-store branch
	// supports the auth-rejection-focused tests in newBridgeNoDBServer
	// that don't carry a DB but still need to reach later steps.
	if s.store == nil {
		s.logger.Warn("bridge: skipping ownership check — store is nil (test wiring)")
	} else {
		ownsCtx, ownsCancel := context.WithTimeout(r.Context(), 2*time.Second)
		owns, err := s.store.OwnsExecutor(ownsCtx, payload.WorkspaceID, exeID)
		ownsCancel()
		if err != nil {
			s.logger.Error("bridge: ownership check failed",
				"workspace_id", payload.WorkspaceID, "exe_id", exeID, "error", err)
			http.Error(w, "ownership check failed", http.StatusInternalServerError)
			return
		}
		if !owns {
			s.logger.Warn("bridge: forbidden",
				"workspace_id", payload.WorkspaceID, "exe_id", exeID,
				"reason", "exe_id_not_in_workspace", "turn_id", payload.TurnID)
			http.Error(w, "exe_id not in workspace", http.StatusForbidden)
			return
		}
	}

	// 4. Acquire per-exe bridge mutex — prevents two concurrent bridge sessions
	// from both calling Read on the same inbound conn (nhooyr's Read is not safe
	// for concurrent use: frames would be stolen between the two pumps).
	if !s.registry.AcquireBridge(exeID) {
		s.logger.Warn("bridge: concurrent session rejected", "exe_id", exeID, "turn_id", payload.TurnID)
		http.Error(w, "another bridge session is active for this executor", http.StatusConflict)
		return
	}
	defer s.registry.ReleaseBridge(exeID)

	// 5. Look up registered inbound conn.
	inbound, ok := s.registry.Lookup(exeID)
	if !ok {
		s.logger.Warn("bridge: no inbound conn", "exe_id", exeID, "turn_id", payload.TurnID)
		http.Error(w, "executor not connected", http.StatusServiceUnavailable)
		return
	}

	// 6. Upgrade caller to ws.
	bridge, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // skip HTTP Origin check; auth is enforced by token verification above
	})
	if err != nil {
		s.logger.Error("bridge: ws accept", "exe_id", exeID, "error", err)
		return
	}
	bridge.SetReadLimit(-1) // codex exec-server streams large process/read responses
	s.logger.Info("bridge: paired", "exe_id", exeID, "turn_id", payload.TurnID)

	// 7. Run paired frame pumps. Cancel propagates to both pumps so the
	// second one exits when the first returns. Derived from r.Context() so
	// graceful shutdown (httpServer.Shutdown) drains active sessions instead
	// of leaking pump goroutines.
	pumpCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)
	// Diagnostic pumps log each frame's direction + truncated payload.
	// Strictly debugging the MCP startup flow; gated on log level.
	go func() { errCh <- s.pumpFramesDebug(pumpCtx, bridge, inbound, "env-mcp→exec", exeID) }()
	go func() { errCh <- s.pumpFramesDebug(pumpCtx, inbound, bridge, "exec→env-mcp", exeID) }()
	// Keep both sides alive through middlebox idle timeouts (~240s for
	// istio, ~300s common). LLM-bound waits often exceed that.
	go wsbridge.KeepAlive(pumpCtx, bridge, 30*time.Second)
	go wsbridge.KeepAlive(pumpCtx, inbound, 30*time.Second)

	// Wait for either pump to return; cancel so the other pump unblocks.
	first := <-errCh
	cancel()
	// Close the bridge side; inbound side is intentionally left open so the
	// executor conn can be re-paired by a subsequent /bridge/{exe_id} request.
	if err := bridge.Close(websocket.StatusNormalClosure, "peer closed"); err != nil {
		s.logger.Warn("bridge: close bridge conn", "exe_id", exeID, "error", err)
	}
	if second := <-errCh; second != nil {
		s.logger.Warn("bridge: second pump ended with error", "exe_id", exeID, "error", second)
	}

	if first != nil {
		s.logger.Warn("bridge: pump ended with error", "exe_id", exeID, "error", first)
	} else {
		s.logger.Info("bridge: pump ended cleanly", "exe_id", exeID)
	}
}

// pumpFramesDebug wraps pumpFrames with per-frame logging. Each frame's
// direction (env-mcp→exec / exec→env-mcp), type, byte length, and first
// 240 bytes of payload are logged at INFO level. Temporary diagnostic
// for the MCP-startup-timeout investigation; remove (or gate behind a
// config flag) after the root cause is found.
func (s *Server) pumpFramesDebug(ctx context.Context, src, dst *websocket.Conn, dir, exeID string) error {
	for {
		mt, data, err := src.Read(ctx)
		if err != nil {
			closeErr := websocket.CloseStatus(err)
			if closeErr == websocket.StatusNormalClosure || closeErr == websocket.StatusGoingAway {
				return nil
			}
			return err
		}
		preview := data
		if len(preview) > 240 {
			preview = preview[:240]
		}
		s.logger.Info("bridge: frame",
			"dir", dir,
			"exe_id", exeID,
			"type", mt.String(),
			"len", len(data),
			"preview", string(preview),
		)
		if err := dst.Write(ctx, mt, data); err != nil {
			return err
		}
	}
}
