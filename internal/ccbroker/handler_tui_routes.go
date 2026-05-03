package ccbroker

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/ccbroker/tools"
)

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sid")
	tid := chi.URLParam(r, "tid")
	s.activeTurns.Cancel(sid, tid)
	s.gate.CancelTurn(tid)
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"cancelled":true}`))
}

func (s *Server) handleDecidePermission(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	var body struct {
		Verdict string `json:"verdict"`
		Scope   string `json:"scope"`
		By      string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"code":"invalid"}`, http.StatusBadRequest)
		return
	}
	if err := s.gate.Resolve(pid, tools.Decision{Verdict: body.Verdict, Scope: body.Scope, By: body.By}); err != nil {
		http.Error(w, `{"code":"already_resolved"}`, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"accepted":true}`))
}

func (s *Server) handleCompactNow(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sid")
	s.compactQueue.Set(sid)
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"queued":true}`))
}

func (s *Server) handleGetActiveTurn(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sid")
	w.Header().Set("Content-Type", "application/json")
	if tid, ok := s.activeTurns.Get(sid); ok {
		json.NewEncoder(w).Encode(map[string]string{"turn_id": tid})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"turn_id": nil})
}
