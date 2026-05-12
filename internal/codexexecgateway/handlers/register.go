// Package handlers contains HTTP handler functions for the codex-exec gateway.
// It must not import the parent codexexecgateway package to avoid import cycles;
// shared DTOs are imported from execmodel instead.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Store is the subset of storage required by the register handler.
type Store interface {
	CreateExecutor(ctx context.Context, e execmodel.Executor, registrationTokenHash string) error
}

type registerRequest struct {
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	DefaultCwd  string `json:"default_cwd"`
}

type registerResponse struct {
	ExeID             string `json:"exe_id"`
	RegistrationToken string `json:"registration_token"`
}

// Register returns an http.HandlerFunc that creates a new executor row and
// returns the freshly-minted (raw) registration token. The DB only stores
// the bcrypt hash — the raw token is never persisted or logged.
func Register(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing user"})
			return
		}
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		raw, err := generateToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		exe := execmodel.Executor{
			ExeID:        "exe_" + uuid.NewString(),
			UserID:       userID,
			DisplayName:  req.DisplayName,
			Description:  req.Description,
			DefaultCwd:   req.DefaultCwd,
			RegisteredAt: time.Now().UTC(),
		}
		if err := store.CreateExecutor(r.Context(), exe, string(hash)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create executor"})
			return
		}
		writeJSON(w, http.StatusCreated, registerResponse{
			ExeID:             exe.ExeID,
			RegistrationToken: raw,
		})
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
