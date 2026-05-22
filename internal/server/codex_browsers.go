package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleListCodexBrowsers returns one row per non-revoked codex token for
// the workspace, annotated with live session info from
// codex_browser_sessions. Auth: any workspace member.
//
//	@Summary   List Codex browser sessions for a workspace
//	@Tags      Codex Browser Sessions
//	@Produce   json
//	@Param     wid  path  string  true  "Workspace id"
//	@Success   200  {array}   CodexBrowserItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "not a member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/browsers [get]
func (s *Server) handleListCodexBrowsers(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	tokens, err := s.DB.ListCodexTokensForWorkspace(r.Context(), wid, false)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]CodexBrowserItem, 0, len(tokens))
	for _, t := range tokens {
		row := CodexBrowserItem{
			ID: t.ID, Name: t.Name, WorkspaceID: t.WorkspaceID,
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
		}
		// One row + one count per token. Browsers panels are small (single
		// digit tokens per workspace typically); N+1 here is acceptable and
		// keeps the query trivial. If this becomes hot, fold into a single
		// JOIN+LEFT-LATERAL query.
		openCount, _ := s.DB.CountOpenCodexBrowserSessions(r.Context(), t.ID)
		row.IsOnline = openCount > 0
		if latest, _ := s.DB.LatestCodexBrowserSession(r.Context(), t.ID); latest != nil {
			row.ClientIP = latest.ClientIP
			row.ClientUA = latest.ClientUA
			row.CodexVersion = latest.CodexVersion
			row.OS = latest.OS
			ca := latest.ConnectedAt
			row.ConnectedAt = &ca
			row.DisconnectedAt = latest.DisconnectedAt
		}
		out = append(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
