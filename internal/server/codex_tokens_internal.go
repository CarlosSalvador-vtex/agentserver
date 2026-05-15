package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type verifyReq struct {
	Token string `json:"token"`
}

type verifyResp struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) handleVerifyCodexToken(w http.ResponseWriter, r *http.Request) {
	var req verifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	id, secret, err := parseCodexToken(req.Token)
	if err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	row, err := s.DB.GetCodexToken(r.Context(), id)
	if err != nil {
		log.Printf("verify codex token: get row: %v", err)
		writeVerifyUnauthorized(w)
		return
	}
	if row == nil {
		writeVerifyUnauthorized(w)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.TokenHash), []byte(secret)); err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	if row.RevokedAt != nil || time.Now().UTC().After(row.ExpiresAt) {
		writeVerifyUnauthorized(w)
		return
	}

	// Async best-effort touch — caller's response is not blocked on this.
	go func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.DB.TouchCodexToken(ctx, id); err != nil {
			log.Printf("verify codex token: touch %s: %v", id, err)
		}
	}(id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(verifyResp{
		UserID: row.UserID, WorkspaceID: row.WorkspaceID,
	})
}

func writeVerifyUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}
