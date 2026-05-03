package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleValidateProxyToken is an internal API for the LLM proxy to validate
// proxy tokens. Returns workspace + status info that the proxy uses to apply
// per-workspace RPD limits and per-sandbox status checks.
//
// Both sandbox-scoped and workspace-scoped tokens live in the same
// proxy_tokens table; the response's token_type tells the proxy which kind
// of validation to apply.
func (s *Server) handleValidateProxyToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyToken string `json:"proxy_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProxyToken == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	pt, err := s.DB.GetProxyToken(req.ProxyToken)
	if err != nil {
		log.Printf("validate-proxy-token: db error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if pt == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	resp := map[string]interface{}{
		"token_type":   string(pt.TokenType),
		"workspace_id": pt.WorkspaceID,
	}

	switch pt.TokenType {
	case "sandbox":
		// Sandbox tokens are only authoritative when the sandbox is alive.
		// Look up the sandbox to surface its current status.
		if !pt.SandboxID.Valid {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		sbx, err := s.DB.GetSandbox(pt.SandboxID.String)
		if err != nil {
			log.Printf("validate-proxy-token: get sandbox: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if sbx == nil {
			// Sandbox was deleted but token row lingered (CASCADE should
			// have prevented this; treat as invalid).
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		resp["sandbox_id"] = sbx.ID
		resp["status"] = sbx.Status
	case "workspace":
		// Workspace tokens have no sandbox; status is constant.
		resp["status"] = "active"
	}

	// Optional modelserver upstream — same logic for both token types.
	if s.ModelserverProxyURL != "" {
		hasMSConn, _ := s.DB.HasModelserverConnection(pt.WorkspaceID)
		if hasMSConn {
			resp["modelserver_upstream_url"] = s.ModelserverProxyURL
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
