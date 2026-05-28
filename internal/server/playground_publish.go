package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
)

// handlePublishSkillDraft publishes a skill draft to the workspace without
// any git operations. The published draft is served directly by the sandbox
// manager instead of the git system template, enabling zero-git self-service.
//
//	@Summary		Publish skill draft
//	@Description	Sets the draft status to 'published'. The sandbox manager resolves published workspace drafts before git system templates.
//	@Tags			playground
//	@Produce		json
//	@Param			id	path		string	true	"Skill draft ID"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{string}	string
//	@Failure		403	{string}	string
//	@Failure		404	{string}	string
//	@Router			/api/playground/skills/{id}/publish [post]
func (s *Server) handlePublishSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	draft, err := s.DB.GetSkillDraft(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if draft == nil {
		http.Error(w, "draft not found", http.StatusNotFound)
		return
	}

	// Only the author or a maintainer/owner can publish.
	isMaintainer := s.userIsMaintainerOrOwner(userID)
	isAuthor := draft.AuthorUserID.Valid && draft.AuthorUserID.String == userID
	if !isAuthor && !isMaintainer {
		http.Error(w, "only the author or a maintainer can publish this draft", http.StatusForbidden)
		return
	}

	workspaceID := ""
	if draft.WorkspaceID.Valid {
		workspaceID = draft.WorkspaceID.String
	}

	if err := s.DB.PublishSkillDraft(id, workspaceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	RecordDraftAction("skill", "published")
	_ = s.DB.AppendDraftAuditEvent("skill", id, userID, "published", nil)

	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "published"})
}

// handlePublishSoulDraft publishes a soul draft to the workspace without git.
//
//	@Summary		Publish soul draft
//	@Description	Sets the draft status to 'published'. The sandbox manager resolves published workspace drafts before git system templates.
//	@Tags			playground
//	@Produce		json
//	@Param			id	path		string	true	"Soul draft ID"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{string}	string
//	@Failure		403	{string}	string
//	@Failure		404	{string}	string
//	@Router			/api/playground/souls/{id}/publish [post]
func (s *Server) handlePublishSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	draft, err := s.DB.GetSoulDraft(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if draft == nil {
		http.Error(w, "draft not found", http.StatusNotFound)
		return
	}

	isMaintainer := s.userIsMaintainerOrOwner(userID)
	isAuthor := draft.AuthorUserID.Valid && draft.AuthorUserID.String == userID
	if !isAuthor && !isMaintainer {
		http.Error(w, "only the author or a maintainer can publish this draft", http.StatusForbidden)
		return
	}

	workspaceID := ""
	if draft.WorkspaceID.Valid {
		workspaceID = draft.WorkspaceID.String
	}

	if err := s.DB.PublishSoulDraft(id, workspaceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	RecordDraftAction("soul", "published")
	_ = s.DB.AppendDraftAuditEvent("soul", id, userID, "published", nil)

	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "published"})
}
