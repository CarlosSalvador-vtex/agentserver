package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// internalValidateAPIKeyRequest is the body for the internal RPC.
// Not part of the public OpenAPI spec — this endpoint is X-Internal-Secret
// protected and exclusively called by codex-app-gateway.
type internalValidateAPIKeyRequest struct {
	Secret string `json:"secret"`
}

type internalValidateAPIKeyResponse struct {
	WorkspaceID string   `json:"workspace_id"`
	KeyID       string   `json:"key_id"`
	Scopes      []string `json:"scopes"`
}

// handleInternalValidateAPIKey accepts {"secret": "wak_<prefix>_<rest>"} and
// returns the workspace_id + key_id on a successful constant-time hash
// compare. Always returns 401 on any mismatch (no information leak about
// whether the prefix existed).
//
// Wired at POST /internal/workspace-api-keys/validate, guarded by the
// existing inline X-Internal-Secret check (INTERNAL_API_SECRET env var).
func (s *Server) handleInternalValidateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req internalValidateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	prefix, _, ok := splitAPIKey(req.Secret)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	row, err := s.DB.ValidateWorkspaceAPIKeySecret(r.Context(), prefix, req.Secret)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Best-effort last_used_at update — don't block on it. Use a fresh
	// background context because r.Context() is cancelled the moment we
	// return; the spawned goroutine would otherwise be killed mid-query.
	keyID := row.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.DB.TouchWorkspaceAPIKeyLastUsed(ctx, keyID)
	}()

	scopes := row.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(internalValidateAPIKeyResponse{
		WorkspaceID: row.WorkspaceID,
		KeyID:       row.ID,
		Scopes:      scopes,
	})
}

// splitAPIKey expects "wak_<8-char-prefix>_<rest>" and returns
// ("wak_<8-char-prefix>", full-original, true) or ("", "", false).
//
// The full original is what gets hashed; the prefix is the DB index key.
func splitAPIKey(full string) (prefix, secret string, ok bool) {
	if !strings.HasPrefix(full, "wak_") {
		return "", "", false
	}
	parts := strings.SplitN(full, "_", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	if len(parts[1]) != 8 {
		return "", "", false
	}
	prefix = "wak_" + parts[1]
	return prefix, full, true
}
