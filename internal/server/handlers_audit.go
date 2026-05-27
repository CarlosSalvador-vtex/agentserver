package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/go-chi/chi/v5"
)

// AuditEventResponse is the JSON shape returned by ListWorkspaceAudit.
type AuditEventResponse struct {
	ID             int64          `json:"id"`
	UserID         string         `json:"user_id,omitempty"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	EventType      string         `json:"event_type"`
	Details        map[string]any `json:"details,omitempty"`
	RequestMethod  string         `json:"request_method,omitempty"`
	RequestPath    string         `json:"request_path,omitempty"`
	ResponseStatus int            `json:"response_status,omitempty"`
	IP             string         `json:"ip,omitempty"`
	UserAgent      string         `json:"user_agent,omitempty"`
	ErrorMsg       string         `json:"error_msg,omitempty"`
	At             string         `json:"at"`
} // @name AuditEventResponse

// AuditListResponse paginates events for one workspace.
type AuditListResponse struct {
	Events []AuditEventResponse `json:"events"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
} // @name AuditListResponse

// handleListWorkspaceAudit — GET /api/workspaces/{id}/audit
//
//	@Summary     List workspace audit events
//	@Description Returns the audit trail for a workspace. Owner/maintainer only —
//	@Description developers do not have access. Filtering: event_type, from, to.
//	@Description Pagination: limit (default 100, max 500), offset.
//	@Tags        Workspaces
//	@Produce     json
//	@Param       id          path   string  true   "Workspace ID"
//	@Param       event_type  query  string  false  "Filter by exact event type"
//	@Param       from        query  string  false  "RFC3339 lower bound (inclusive)"
//	@Param       to          query  string  false  "RFC3339 upper bound (inclusive)"
//	@Param       limit       query  int     false  "Page size (default 100, max 500)"
//	@Param       offset      query  int     false  "Pagination offset"
//	@Success     200  {object}  AuditListResponse
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Failure     500  {string}  string  "internal error"
//	@Router      /api/workspaces/{id}/audit [get]
func (s *Server) handleListWorkspaceAudit(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}

	q := db.AuditQuery{WorkspaceID: wsID}
	if v := r.URL.Query().Get("event_type"); v != "" {
		q.EventType = v
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.From = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.To = t
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			q.Offset = n
		}
	}

	events, err := s.DB.ListAuditEvents(q)
	if err != nil {
		log.Printf("list audit events: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := AuditListResponse{
		Limit:  q.Limit,
		Offset: q.Offset,
		Events: make([]AuditEventResponse, 0, len(events)),
	}
	for _, ev := range events {
		resp.Events = append(resp.Events, AuditEventResponse{
			ID:             ev.ID,
			UserID:         ev.UserID,
			WorkspaceID:    ev.WorkspaceID,
			EventType:      ev.EventType,
			Details:        ev.Details,
			RequestMethod:  ev.RequestMethod,
			RequestPath:    ev.RequestPath,
			ResponseStatus: ev.ResponseStatus,
			IP:             ev.IP,
			UserAgent:      ev.UserAgent,
			ErrorMsg:       ev.ErrorMsg,
			At:             ev.At.UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
