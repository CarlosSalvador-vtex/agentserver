package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type controlReq struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

func (s *Server) handleAgentSessionControl(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	sid := chi.URLParam(r, "sid")
	sess, err := s.DB.GetAgentSession(sid)
	if err != nil || sess == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	var req controlReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "invalid body")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch req.Command {
	case "model":
		m, _ := req.Args["model"].(string)
		if m == "" {
			writeAPIErr(w, http.StatusBadRequest, "invalid", "args.model required")
			return
		}
		if _, err := s.DB.Exec(`UPDATE agent_sessions SET preferred_model=$1, updated_at=NOW() WHERE id=$2`, m, sid); err != nil {
			writeAPIErr(w, http.StatusInternalServerError, "internal", "write failed")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"applied": true, "model": m})

	case "permission":
		m, _ := req.Args["mode"].(string)
		if m != "ask" && m != "bypass" {
			writeAPIErr(w, http.StatusBadRequest, "invalid", "args.mode must be ask or bypass")
			return
		}
		if _, err := s.DB.Exec(`UPDATE agent_sessions SET permission_mode=$1, updated_at=NOW() WHERE id=$2`, m, sid); err != nil {
			writeAPIErr(w, http.StatusInternalServerError, "internal", "write failed")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"applied": true, "mode": m})

	case "compact":
		if s.CCBrokerURL == "" {
			writeAPIErr(w, http.StatusServiceUnavailable, "upstream", "cc-broker URL not configured")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rq, _ := http.NewRequestWithContext(ctx, "POST", s.CCBrokerURL+"/api/sessions/"+sid+"/compact", nil)
		resp, err := http.DefaultClient.Do(rq)
		if err != nil {
			writeAPIErr(w, http.StatusBadGateway, "upstream", "cc-broker unreachable")
			return
		}
		resp.Body.Close()
		json.NewEncoder(w).Encode(map[string]any{"queued": true})

	case "cost":
		// Sum token usage from session events. v1 simple: scan up to 1000 events.
		events, _ := s.DB.GetAgentSessionEventsSince(sid, 0, 1000)
		var inTok, outTok int64
		for _, ev := range events {
			var p struct {
				Usage struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			inTok += p.Usage.InputTokens
			outTok += p.Usage.OutputTokens
			// SDK may also nest under message.usage; v1 supports the flat case only.
		}
		json.NewEncoder(w).Encode(map[string]any{
			"input_tokens":  inTok,
			"output_tokens": outTok,
		})

	case "agents":
		if s.ExecutorRegistryURL == "" {
			writeAPIErr(w, http.StatusServiceUnavailable, "upstream", "executor-registry URL not configured")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		u := s.ExecutorRegistryURL + "/api/executors?workspace_id=" + sess.WorkspaceID
		rq, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
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

	default:
		writeAPIErr(w, http.StatusBadRequest, "unknown_command", "unknown control command: "+req.Command)
	}
}
