package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	sid := chi.URLParam(r, "sid")
	tid := chi.URLParam(r, "tid")
	if s.CCBrokerURL == "" {
		writeAPIErr(w, http.StatusServiceUnavailable, "upstream", "cc-broker URL not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rq, _ := http.NewRequestWithContext(ctx, "POST",
		s.CCBrokerURL+"/api/sessions/"+sid+"/turns/"+tid+"/cancel", nil)
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		writeAPIErr(w, http.StatusBadGateway, "upstream", "cc-broker unreachable")
		return
	}
	resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"cancelled":true}`))
}

type decisionReq struct {
	Decision            string `json:"decision"`
	Scope               string `json:"scope"`
	ResponderExecutorID string `json:"responder_executor_id"`
}

func (s *Server) handlePermissionDecision(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	sid := chi.URLParam(r, "sid")
	pid := chi.URLParam(r, "pid")
	var req decisionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "invalid body")
		return
	}
	sess, err := s.DB.GetAgentSession(sid)
	if err != nil || sess == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if sess.PermissionResponder == nil || *sess.PermissionResponder != req.ResponderExecutorID {
		writeAPIErr(w, http.StatusForbidden, "forbidden", "not the current permission_responder")
		return
	}
	if s.CCBrokerURL == "" {
		writeAPIErr(w, http.StatusServiceUnavailable, "upstream", "cc-broker URL not configured")
		return
	}
	forwardBody, _ := json.Marshal(map[string]string{
		"verdict": req.Decision,
		"scope":   req.Scope,
		"by":      req.ResponderExecutorID,
	})
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rq, _ := http.NewRequestWithContext(ctx, "POST",
		s.CCBrokerURL+"/api/sessions/"+sid+"/permissions/"+pid+"/decide",
		bytes.NewReader(forwardBody))
	rq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		writeAPIErr(w, http.StatusBadGateway, "upstream", "cc-broker unreachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		writeAPIErr(w, http.StatusConflict, "already_resolved", "permission already resolved")
		return
	}
	if resp.StatusCode != http.StatusOK {
		upstreamBody, _ := io.ReadAll(resp.Body)
		writeAPIErr(w, http.StatusBadGateway, "upstream", string(upstreamBody))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"accepted": true, "applied_at": time.Now().UTC()})
}

func (s *Server) handleExecutorStatus(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	if s.ExecutorRegistryURL == "" {
		writeAPIErr(w, http.StatusServiceUnavailable, "upstream", "executor-registry URL not configured")
		return
	}
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rq, _ := http.NewRequestWithContext(ctx, "GET", s.ExecutorRegistryURL+"/api/executors/"+id, nil)
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		writeAPIErr(w, http.StatusBadGateway, "upstream", "executor-registry unreachable")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
