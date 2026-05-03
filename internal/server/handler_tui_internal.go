package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleTurnFinished(w http.ResponseWriter, r *http.Request) {
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		if r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	sid := chi.URLParam(r, "sid")
	var body struct {
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.SessionID != sid {
		http.Error(w, "session_id mismatch", http.StatusBadRequest)
		return
	}
	_ = s.DB.ClearActiveTurn(r.Context(), sid, body.TurnID)
	w.WriteHeader(http.StatusOK)
}
