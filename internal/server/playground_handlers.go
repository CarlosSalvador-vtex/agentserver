package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// Quotas — keep in sync with docs/playground-design.md §10.
const (
	playgroundMaxDraftsPerUser   = 50
	playgroundMaxFileSizeBytes   = 256 * 1024  // 256 KiB
	playgroundMaxTotalDraftBytes = 1024 * 1024 // 1 MiB
)

// playgroundSkillSummary is the lightweight shape returned by the list
// endpoint. Full files payload only ships from GET /{id}.
type playgroundSkillSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	WorkspaceID     string `json:"workspace_id,omitempty"` // empty = system template
	Visibility      string `json:"visibility,omitempty"` // private | shared (migration 036)
	CanSetVisibility bool  `json:"can_set_visibility,omitempty"`
	PromotedPRURL   string `json:"promoted_pr_url,omitempty"`
	PromotedPRState string `json:"promoted_pr_state,omitempty"`
	PromotedCommit  string `json:"promoted_commit,omitempty"`
	UpdatedAt       string `json:"updated_at"`
}

type playgroundSkillFull struct {
	playgroundSkillSummary
	Files map[string]string `json:"files"`
}

type playgroundSoulSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	SchemaVersion   string `json:"schema_version"`
	WorkspaceID     string `json:"workspace_id,omitempty"` // empty = system template
	Visibility      string `json:"visibility,omitempty"` // private | shared (migration 036)
	CanSetVisibility bool  `json:"can_set_visibility,omitempty"`
	PromotedPRURL   string `json:"promoted_pr_url,omitempty"`
	PromotedPRState string `json:"promoted_pr_state,omitempty"`
	PromotedCommit  string `json:"promoted_commit,omitempty"`
	UpdatedAt       string `json:"updated_at"`
}

type playgroundSoulFull struct {
	playgroundSoulSummary
	Frontmatter map[string]interface{} `json:"frontmatter"`
	Body        string                 `json:"body"`
}

type playgroundSkillListResponse struct {
	Drafts []playgroundSkillSummary `json:"drafts"`
}

type playgroundSoulListResponse struct {
	Drafts []playgroundSoulSummary `json:"drafts"`
}

type promoteDraftResponse struct {
	PRURL     string `json:"pr_url"`
	Branch    string `json:"branch"`
	HeadSha   string `json:"head_sha"`
	DraftID   string `json:"draft_id"`
	DraftKind string `json:"draft_kind"`
}

// --- Skill drafts ----------------------------------------------------------

// handleListSkillDrafts lists skill drafts for the authenticated author.
//
//	@Summary		List skill drafts
//	@Description	Returns skill drafts owned by the current user in the playground catalog.
//	@Tags			playground
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	playgroundSkillListResponse	"drafts"
//	@Failure		401	{object}	map[string]string
//	@Router			/api/playground/skills [get]
func (s *Server) handleListSkillDrafts(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	wsIDs := s.callerWorkspaceIDs(userID)
	drafts, err := s.DB.ListSkillDraftsForScope(userID, wsIDs)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSkillSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, s.summarizeSkillForUser(userID, d))
	}
	writeJSON(w, http.StatusOK, playgroundSkillListResponse{Drafts: out})
}

// handleCreateSkillDraft creates a new skill draft.
//
//	@Summary		Create skill draft
//	@Description	Creates a skill draft in the author's workspace playground catalog.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			body	body	playgroundSkillFull	true	"Skill draft payload"
//	@Success		201	{object}	playgroundSkillSummary
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Router			/api/playground/skills [post]
func (s *Server) handleCreateSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())

	count, err := s.DB.CountSkillDraftsByAuthor(userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count >= playgroundMaxDraftsPerUser {
		http.Error(w, fmt.Sprintf("quota exceeded: max %d skill drafts per user", playgroundMaxDraftsPerUser), http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		WorkspaceID string `json:"workspace_id,omitempty"` // tenant scope (improvements.md #17)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := validateSkillName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	draft, err := s.DB.CreateSkillDraft(req.Name, req.Description, userID, wsID)
	if err != nil {
		// UNIQUE (author, name) violation surfaces here.
		http.Error(w, fmt.Sprintf("create failed: %v", err), http.StatusConflict)
		return
	}
	RecordDraftAction("skill", "created")
	_ = s.DB.AppendDraftAuditEvent("skill", draft.ID, userID, "created", map[string]interface{}{"name": draft.Name})
	writeJSON(w, http.StatusCreated, s.summarizeSkillForUser(userID, draft))
}

// handleGetSkillDraft returns a skill draft by ID.
//
//	@Summary		Get skill draft
//	@Description	Returns full skill draft content for the author.
//	@Tags			playground
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Skill draft ID"
//	@Success		200	{object}	playgroundSkillFull
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/skills/{id} [get]
func (s *Server) handleGetSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSkillDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	isAuthor := draft.AuthorUserID.Valid && draft.AuthorUserID.String == userID
	isSystemTemplate := !draft.WorkspaceID.Valid
	wsIDs := s.callerWorkspaceIDs(userID)
	isWorkspaceMember := draft.WorkspaceID.Valid && containsString(wsIDs, draft.WorkspaceID.String)
	if !isAuthor && !isSystemTemplate && !isWorkspaceMember {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, playgroundSkillFull{
		playgroundSkillSummary: s.summarizeSkillForUser(userID, draft),
		Files:                  draft.Files,
	})
}

// handlePatchSkillDraft updates a skill draft.
//
//	@Summary		Update skill draft
//	@Description	Partially updates skill draft fields for the author.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id		path		string				true	"Skill draft ID"
//	@Param			body	body		playgroundSkillFull	true	"Fields to update"
//	@Success		200		{object}	playgroundSkillFull
//	@Failure		400		{object}	map[string]string
//	@Failure		401		{object}	map[string]string
//	@Failure		403		{object}	map[string]string
//	@Failure		404		{object}	map[string]string
//	@Router			/api/playground/skills/{id} [patch]
func (s *Server) handlePatchSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := s.DB.GetSkillDraft(id)
	if err != nil || existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !existing.AuthorUserID.Valid || existing.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if existing.Status != "draft" {
		http.Error(w, fmt.Sprintf("not editable: status=%s", existing.Status), http.StatusConflict)
		return
	}

	var req struct {
		Files map[string]string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateSkillFiles(req.Files); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.DB.UpdateSkillDraftFiles(id, req.Files); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	RecordDraftAction("skill", "patched")
	_ = s.DB.AppendDraftAuditEvent("skill", id, userID, "patched", map[string]interface{}{"files_count": len(req.Files)})
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleArchiveSkillDraft archives a skill draft.
//
//	@Summary		Archive skill draft
//	@Description	Soft-deletes a skill draft owned by the author.
//	@Tags			playground
//	@Security		ApiKeyAuth
//	@Param			id	path	string	true	"Skill draft ID"
//	@Success		204
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/skills/{id} [delete]
func (s *Server) handleArchiveSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	existing, err := s.DB.GetSkillDraft(id)
	if err != nil || existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !existing.AuthorUserID.Valid || existing.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if err := s.DB.ArchiveSkillDraft(id); err != nil {
		http.Error(w, "archive failed", http.StatusInternalServerError)
		return
	}
	RecordDraftAction("skill", "archived")
	_ = s.DB.AppendDraftAuditEvent("skill", id, userID, "archived", nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- Soul drafts -----------------------------------------------------------

// handleListSoulDrafts lists soul drafts for the authenticated author.
//
//	@Summary		List soul drafts
//	@Description	Returns soul drafts owned by the current user in the playground catalog.
//	@Tags			playground
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	playgroundSoulListResponse	"drafts"
//	@Failure		401	{object}	map[string]string
//	@Router			/api/playground/souls [get]
func (s *Server) handleListSoulDrafts(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	wsIDs := s.callerWorkspaceIDs(userID)
	drafts, err := s.DB.ListSoulDraftsForScope(userID, wsIDs)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSoulSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, s.summarizeSoulForUser(userID, d))
	}
	writeJSON(w, http.StatusOK, playgroundSoulListResponse{Drafts: out})
}

// handleCreateSoulDraft creates a new soul draft.
//
//	@Summary		Create soul draft
//	@Description	Creates a soul draft in the author's workspace playground catalog.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			body	body	object	true	"Soul draft fields (name, description, workspace_id)"
//	@Success		201	{object}	playgroundSoulSummary
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Router			/api/playground/souls [post]
func (s *Server) handleCreateSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())

	count, err := s.DB.CountSoulDraftsByAuthor(userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count >= playgroundMaxDraftsPerUser {
		http.Error(w, fmt.Sprintf("quota exceeded: max %d soul drafts per user", playgroundMaxDraftsPerUser), http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		WorkspaceID string `json:"workspace_id,omitempty"` // tenant scope (improvements.md #17)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := validateSkillName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	draft, err := s.DB.CreateSoulDraft(req.Name, req.Description, userID, wsID)
	if err != nil {
		http.Error(w, fmt.Sprintf("create failed: %v", err), http.StatusConflict)
		return
	}
	RecordDraftAction("soul", "created")
	_ = s.DB.AppendDraftAuditEvent("soul", draft.ID, userID, "created", map[string]interface{}{"name": draft.Name})
	writeJSON(w, http.StatusCreated, s.summarizeSoulForUser(userID, draft))
}

// handleGetSoulDraft returns a soul draft by ID.
//
//	@Summary		Get soul draft
//	@Description	Returns full soul draft content for the author.
//	@Tags			playground
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Soul draft ID"
//	@Success		200	{object}	playgroundSoulFull
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/souls/{id} [get]
func (s *Server) handleGetSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSoulDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	isAuthor := draft.AuthorUserID.Valid && draft.AuthorUserID.String == userID
	isSystemTemplate := !draft.WorkspaceID.Valid
	wsIDs := s.callerWorkspaceIDs(userID)
	isWorkspaceMember := draft.WorkspaceID.Valid && containsString(wsIDs, draft.WorkspaceID.String)
	if !isAuthor && !isSystemTemplate && !isWorkspaceMember {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, playgroundSoulFull{
		playgroundSoulSummary: s.summarizeSoulForUser(userID, draft),
		Frontmatter:           draft.Frontmatter,
		Body:                  draft.Body,
	})
}

// handlePatchSoulDraft updates a soul draft.
//
//	@Summary		Update soul draft
//	@Description	Partially updates soul draft fields for the author.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id		path		string				true	"Soul draft ID"
//	@Param			body	body		playgroundSoulFull	true	"Fields to update"
//	@Success		200		{object}	playgroundSoulFull
//	@Failure		400		{object}	map[string]string
//	@Failure		401		{object}	map[string]string
//	@Failure		403		{object}	map[string]string
//	@Failure		404		{object}	map[string]string
//	@Router			/api/playground/souls/{id} [patch]
func (s *Server) handlePatchSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	existing, err := s.DB.GetSoulDraft(id)
	if err != nil || existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !existing.AuthorUserID.Valid || existing.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if existing.Status != "draft" {
		http.Error(w, fmt.Sprintf("not editable: status=%s", existing.Status), http.StatusConflict)
		return
	}

	var req struct {
		Frontmatter map[string]interface{} `json:"frontmatter"`
		Body        string                 `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateSoulFrontmatter(req.Frontmatter); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Body) > playgroundMaxTotalDraftBytes {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := s.DB.UpdateSoulDraft(id, req.Frontmatter, req.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	RecordDraftAction("soul", "patched")
	_ = s.DB.AppendDraftAuditEvent("soul", id, userID, "patched", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleArchiveSoulDraft archives a soul draft.
//
//	@Summary		Archive soul draft
//	@Description	Soft-deletes a soul draft owned by the author.
//	@Tags			playground
//	@Security		ApiKeyAuth
//	@Param			id	path	string	true	"Soul draft ID"
//	@Success		204
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/souls/{id} [delete]
func (s *Server) handleArchiveSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	existing, err := s.DB.GetSoulDraft(id)
	if err != nil || existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !existing.AuthorUserID.Valid || existing.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	if err := s.DB.ArchiveSoulDraft(id); err != nil {
		http.Error(w, "archive failed", http.StatusInternalServerError)
		return
	}
	RecordDraftAction("soul", "archived")
	_ = s.DB.AppendDraftAuditEvent("soul", id, userID, "archived", nil)
	w.WriteHeader(http.StatusNoContent)
}

// callerWorkspaceIDs lists the workspace IDs the user belongs to. Used by
// the scoped list endpoints to filter visible drafts. Returns empty slice
// (not nil) on error so the caller still sees system templates.
func (s *Server) callerWorkspaceIDs(userID string) []string {
	wss, err := s.DB.ListWorkspacesByUser(userID)
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(wss))
	for _, w := range wss {
		out = append(out, w.ID)
	}
	return out
}

// resolveDraftWorkspaceID picks the workspace_id used to scope a new draft
// (improvements.md #17 tenant catalog). When wsChoice is non-empty, the
// caller MUST be a member of that workspace — otherwise we reject with
// "forbidden". Empty wsChoice falls back to the user's first workspace
// (legacy behavior preserves single-tenant assumption). When the user has
// no workspaces at all, returns empty → DB stores NULL = system template.
func (s *Server) resolveDraftWorkspaceID(userID, wsChoice string) (string, error) {
	wss, err := s.DB.ListWorkspacesByUser(userID)
	if err != nil || len(wss) == 0 {
		// No memberships: write NULL so the draft is system-wide. Same
		// behavior as pre-035 — backstop for orphan-author rows.
		return "", nil
	}
	if wsChoice == "" {
		return wss[0].ID, nil
	}
	for _, w := range wss {
		if w.ID == wsChoice {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("workspace_id %q: caller is not a member", wsChoice)
}

// --- helpers ---------------------------------------------------------------

func summarizeSkill(d *db.SkillDraft) playgroundSkillSummary {
	vis := d.Visibility
	if vis == "" {
		vis = "private"
	}
	return playgroundSkillSummary{
		ID:              d.ID,
		Name:            d.Name,
		Description:     d.Description,
		Status:          d.Status,
		WorkspaceID:     d.WorkspaceID.String,
		Visibility:      vis,
		PromotedPRURL:   d.PromotedPRURL.String,
		PromotedPRState: d.PromotedPRState.String,
		PromotedCommit:  d.PromotedCommit.String,
		UpdatedAt:       d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func summarizeSoul(d *db.SoulDraft) playgroundSoulSummary {
	vis := d.Visibility
	if vis == "" {
		vis = "private"
	}
	return playgroundSoulSummary{
		ID:              d.ID,
		Name:            d.Name,
		Description:     d.Description,
		Status:          d.Status,
		SchemaVersion:   d.SchemaVersion,
		WorkspaceID:     d.WorkspaceID.String,
		Visibility:      vis,
		PromotedPRURL:   d.PromotedPRURL.String,
		PromotedPRState: d.PromotedPRState.String,
		PromotedCommit:  d.PromotedCommit.String,
		UpdatedAt:       d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) summarizeSkillForUser(userID string, d *db.SkillDraft) playgroundSkillSummary {
	out := summarizeSkill(d)
	out.CanSetVisibility = s.userCanSetDraftVisibility(userID, d.WorkspaceID)
	return out
}

func (s *Server) summarizeSoulForUser(userID string, d *db.SoulDraft) playgroundSoulSummary {
	out := summarizeSoul(d)
	out.CanSetVisibility = s.userCanSetDraftVisibility(userID, d.WorkspaceID)
	return out
}

// writeJSON mirrors the Content-Type + encode pattern used elsewhere
// in this package.
func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// validateSkillName rejects names that would break filesystem paths,
// k8s ConfigMap key encoding, or git branch creation in promote.
var skillNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}[a-z0-9]$`)

func validateSkillName(name string) error {
	if !skillNameRE.MatchString(name) {
		return fmt.Errorf("name must match ^[a-z][a-z0-9-]{0,62}[a-z0-9]$ (got %q)", name)
	}
	return nil
}

// validateSkillFiles enforces the per-file + total-size quota and
// rejects keys that would render as confusing ConfigMap data keys.
func validateSkillFiles(files map[string]string) error {
	total := 0
	for path, content := range files {
		if path == "" {
			return fmt.Errorf("file path cannot be empty")
		}
		if strings.Contains(path, "..") {
			return fmt.Errorf("file path %q: must not contain ..", path)
		}
		if strings.HasPrefix(path, "/") {
			return fmt.Errorf("file path %q: must be relative", path)
		}
		if len(content) > playgroundMaxFileSizeBytes {
			return fmt.Errorf("file %s: exceeds %d bytes", path, playgroundMaxFileSizeBytes)
		}
		total += len(content)
	}
	if total > playgroundMaxTotalDraftBytes {
		return fmt.Errorf("total payload exceeds %d bytes", playgroundMaxTotalDraftBytes)
	}
	return nil
}

// validateSoulFrontmatter checks the subset of fields the runtime cares
// about. Schema versioning is forward-tolerant: unknown fields are
// ignored, missing optional fields default at runtime.
func validateSoulFrontmatter(fm map[string]interface{}) error {
	if fm == nil {
		return nil
	}
	if id, ok := fm["id"].(string); ok {
		if err := validateSkillName(id); err != nil {
			return fmt.Errorf("frontmatter.id: %w", err)
		}
	}
	if voice, ok := fm["voice"].(map[string]interface{}); ok {
		if lang, ok := voice["language"].(string); ok && lang == "" {
			return fmt.Errorf("frontmatter.voice.language: cannot be empty")
		}
		if formality, ok := voice["formality"].(string); ok {
			switch formality {
			case "", "high", "medium", "low":
			default:
				return fmt.Errorf("frontmatter.voice.formality: must be high|medium|low (got %q)", formality)
			}
		}
	}
	if c, ok := fm["constraints"].(map[string]interface{}); ok {
		if mt, ok := c["max_turns"].(float64); ok && (mt < 1 || mt > 1000) {
			return fmt.Errorf("frontmatter.constraints.max_turns: must be 1..1000")
		}
	}
	return nil
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
