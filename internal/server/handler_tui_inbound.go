package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/bridge"
	"github.com/agentserver/agentserver/internal/db"
)

const tuiAttachmentMaxBytes = 8 << 20

type tuiInboundReq struct {
	SessionID           string                 `json:"session_id,omitempty"`
	ExecutorID          string                 `json:"executor_id"`
	Text                string                 `json:"text"`
	Attachments         []tuiInboundAttachment `json:"attachments,omitempty"`
	Metadata            map[string]any         `json:"metadata,omitempty"`
	PermissionResponder bool                   `json:"permission_responder,omitempty"`
}

type tuiInboundAttachment struct {
	Kind       string `json:"kind"`
	Filename   string `json:"filename"`
	Size       int    `json:"size"`
	ContentB64 string `json:"content_b64"`
}

func (s *Server) handleTUIInbound(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "wid")
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}

	var req tuiInboundReq
	if err := json.NewDecoder(io.LimitReader(r.Body, tuiAttachmentMaxBytes+1<<10)).Decode(&req); err != nil {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "invalid body")
		return
	}
	if req.ExecutorID == "" || req.Text == "" {
		writeAPIErr(w, http.StatusBadRequest, "invalid", "executor_id and text required")
		return
	}
	var attachBytes int
	for _, a := range req.Attachments {
		attachBytes += len(a.ContentB64)
	}
	if attachBytes > tuiAttachmentMaxBytes {
		writeAPIErr(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "attachments exceed 8 MiB")
		return
	}

	// Resolve / create session.
	sid := req.SessionID
	if sid == "" {
		sid = "cse_" + uuid.NewString()
		if err := s.DB.CreateAgentSessionTUI(r.Context(), db.CreateTUISessionParams{
			ID:                  sid,
			WorkspaceID:         workspaceID,
			ExternalID:          fmt.Sprintf("tui:%s:%d", req.ExecutorID, time.Now().Unix()),
			Title:               "TUI session",
			CreatorUserID:       userID,
			PermissionMode:      "ask",
			PreferredExecutorID: req.ExecutorID,
		}); err != nil {
			writeAPIErr(w, http.StatusInternalServerError, "internal", "create session failed")
			return
		}
		if req.PermissionResponder {
			if _, err := s.DB.AttachResponder(r.Context(), sid, req.ExecutorID, true); err != nil {
				log.Printf("tui_inbound: attach responder: %v", err)
			}
		}
	} else {
		sess, err := s.DB.GetAgentSession(sid)
		if err != nil || sess == nil || sess.WorkspaceID != workspaceID {
			writeAPIErr(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		// Observer guard: if a different executor is the preferred operator,
		// reject this inbound (only the operator can submit prompts).
		if sess.PreferredExecutorID != nil && *sess.PreferredExecutorID != req.ExecutorID {
			writeAPIErr(w, http.StatusForbidden, "not_operator", "this executor is not the operator")
			return
		}
	}

	// CAS active_turn_id.
	turnID := "trn_" + uuid.NewString()
	ok, err := s.DB.ClaimActiveTurn(r.Context(), sid, turnID)
	if err != nil {
		writeAPIErr(w, http.StatusInternalServerError, "internal", "claim turn failed")
		return
	}
	if !ok {
		cur, _ := s.DB.GetActiveTurn(r.Context(), sid)
		writeAPIErr(w, http.StatusConflict, "turn_in_progress", fmt.Sprintf("active turn %s", cur))
		return
	}

	// Asynchronously call cc-broker. The SSE bridge is implemented in Task 16;
	// for T14 we drain the body so cc-broker's turn completes.
	go s.callCCBrokerForTUI(context.Background(), sid, turnID, workspaceID, userID, req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"session_id": sid,
		"turn_id":    turnID,
	})
}

func (s *Server) callCCBrokerForTUI(ctx context.Context, sid, turnID, wid, userID string, req tuiInboundReq) {
	if s.CCBrokerURL == "" {
		log.Printf("tui_inbound: CCBrokerURL not configured")
		_ = s.DB.ClearActiveTurn(ctx, sid, turnID)
		return
	}

	sess, _ := s.DB.GetAgentSession(sid)
	metaModel, _ := req.Metadata["model"].(string)
	if metaModel == "" && sess != nil && sess.PreferredModel != nil {
		metaModel = *sess.PreferredModel
	}
	turnKind, _ := req.Metadata["turn_kind"].(string)
	if turnKind == "" {
		turnKind = "user"
	}

	var preferredExec string
	if sess != nil && sess.PreferredExecutorID != nil {
		preferredExec = *sess.PreferredExecutorID
	}
	permissionMode := "ask"
	if sess != nil && sess.PermissionMode != "" {
		permissionMode = sess.PermissionMode
	}

	body, _ := json.Marshal(map[string]any{
		"session_id":   sid,
		"workspace_id": wid,
		"user_message": req.Text,
		"metadata": map[string]any{
			"channel_type":          "tui",
			"creator_user_id":       userID,
			"permission_mode":       permissionMode,
			"model":                 metaModel,
			"preferred_executor_id": preferredExec,
			"turn_kind":             turnKind,
		},
	})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.CCBrokerURL+"/api/turns", bytes.NewReader(body))
	if err != nil {
		log.Printf("tui_inbound: build cc-broker request: %v", err)
		_ = s.DB.ClearActiveTurn(ctx, sid, turnID)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("tui_inbound: cc-broker call failed: %v", err)
		_ = s.DB.ClearActiveTurn(ctx, sid, turnID)
		return
	}
	defer resp.Body.Close()

	// Stream and bridge SSE events from cc-broker → agent_session_events + SSE broker.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var (
		eventType string
		dataBuf   bytes.Buffer
	)
	flushEvent := func() {
		if eventType == "" && dataBuf.Len() == 0 {
			return
		}
		payload := append([]byte(nil), dataBuf.Bytes()...)
		inserted, _ := s.DB.InsertAgentSessionEvents(sid, []db.AgentSessionEvent{
			{EventID: uuid.NewString(), EventType: eventType, Source: "ccbroker", Payload: payload},
		})
		var seq int64
		if len(inserted) > 0 {
			seq = inserted[0].SeqNum
		}
		if s.BridgeHandler != nil && s.BridgeHandler.SSE != nil {
			s.BridgeHandler.SSE.Publish(sid, &bridge.StreamClientEvent{
				SequenceNum: seq,
				EventType:   eventType,
				Source:      "ccbroker",
				Payload:     payload,
			})
		}
		eventType = ""
		dataBuf.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flushEvent()
		case strings.HasPrefix(line, ":"):
			// comment / keepalive — ignore
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	flushEvent() // flush trailing event if any (no terminating blank line)

	// Safety net: clear active_turn_id even if cc-broker's /turn-finished
	// callback is missed. The CAS guard means a fresh turn won't be clobbered.
	_ = s.DB.ClearActiveTurn(ctx, sid, turnID)
}

// writeAPIErr writes a {"error":{"code","message"}} response.
func writeAPIErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}
