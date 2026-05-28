package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
)

// --- Marketplace endpoints (improvements.md #18) --------------------------
//
// GET  /api/marketplace/skills          — list shared skill drafts (any auth)
// GET  /api/marketplace/souls           — list shared soul drafts  (any auth)
// POST /api/marketplace/skills/{id}/fork — copy to caller workspace
// POST /api/marketplace/souls/{id}/fork  — copy to caller workspace
// PATCH /api/playground/skills/{id}/visibility — workspace owner/maintainer
// PATCH /api/playground/souls/{id}/visibility  — workspace owner/maintainer
// PATCH /api/admin/playground/skills/{id}/visibility — admin override
// PATCH /api/admin/playground/souls/{id}/visibility  — admin override

type marketplaceSkillListing struct {
	playgroundSkillSummary
	AuthorWorkspaceID string   `json:"author_workspace_id,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

type marketplaceSoulListing struct {
	playgroundSoulSummary
	AuthorWorkspaceID string   `json:"author_workspace_id,omitempty"`
	CompatibleSkills  []string `json:"compatible_skills,omitempty"`
}

type marketplaceSkillListResponse struct {
	Skills []marketplaceSkillListing `json:"skills"`
}

type marketplaceSoulListResponse struct {
	Souls []marketplaceSoulListing `json:"souls"`
}

type marketplaceForkRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

var skillTagsRE = regexp.MustCompile(`(?m)^tags:\s*\[([^\]]*)\]`)

// handleListMarketplaceSkills lists shared skill drafts visible in the marketplace.
//
//	@Summary		List marketplace skills
//	@Description	Returns skill drafts shared to the marketplace catalog.
//	@Tags			marketplace
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	marketplaceSkillListResponse
//	@Failure		401	{object}	map[string]string
//	@Router			/api/marketplace/skills [get]
func (s *Server) handleListMarketplaceSkills(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSharedSkillDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]marketplaceSkillListing, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, marketplaceSkillListing{
			playgroundSkillSummary: summarizeSkill(d),
			AuthorWorkspaceID:      d.WorkspaceID.String,
			Tags:                   skillTagsFromFiles(d.Files),
		})
	}
	writeJSON(w, http.StatusOK, marketplaceSkillListResponse{Skills: out})
}

// handleListMarketplaceSouls lists shared soul drafts visible in the marketplace.
//
//	@Summary		List marketplace souls
//	@Description	Returns soul drafts shared to the marketplace catalog.
//	@Tags			marketplace
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	marketplaceSoulListResponse
//	@Failure		401	{object}	map[string]string
//	@Router			/api/marketplace/souls [get]
func (s *Server) handleListMarketplaceSouls(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSharedSoulDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]marketplaceSoulListing, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, marketplaceSoulListing{
			playgroundSoulSummary: summarizeSoul(d),
			AuthorWorkspaceID:     d.WorkspaceID.String,
			CompatibleSkills:      soulCompatibleSkills(d.Frontmatter),
		})
	}
	writeJSON(w, http.StatusOK, marketplaceSoulListResponse{Souls: out})
}

// handleForkMarketplaceSkill copies a marketplace skill draft into the caller workspace.
//
//	@Summary		Fork marketplace skill
//	@Description	Creates a private copy of a shared marketplace skill draft in the target workspace.
//	@Tags			marketplace
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Marketplace skill draft ID"
//	@Param			body	body		marketplaceForkRequest	true	"Target workspace"
//	@Success		201	{object}	playgroundSkillSummary
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Router			/api/marketplace/skills/{id}/fork [post]
func (s *Server) handleForkMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	fork, err := s.DB.ForkSkillDraft(id, userID, wsID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.summarizeSkillForUser(userID, fork))
}

// handleForkMarketplaceSoul copies a marketplace soul draft into the caller workspace.
//
//	@Summary		Fork marketplace soul
//	@Description	Creates a private copy of a shared marketplace soul draft in the target workspace.
//	@Tags			marketplace
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Marketplace soul draft ID"
//	@Param			body	body		marketplaceForkRequest	true	"Target workspace"
//	@Success		201	{object}	playgroundSoulSummary
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Router			/api/marketplace/souls/{id}/fork [post]
func (s *Server) handleForkMarketplaceSoul(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	wsID, err := s.resolveDraftWorkspaceID(userID, req.WorkspaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	fork, err := s.DB.ForkSoulDraft(id, userID, wsID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.summarizeSoulForUser(userID, fork))
}

// userCanSetDraftVisibility allows workspace owner/maintainer to share drafts
// scoped to that workspace. System-wide drafts (NULL workspace) stay admin-only.
func (s *Server) userCanSetDraftVisibility(userID string, workspaceID sql.NullString) bool {
	if !workspaceID.Valid || workspaceID.String == "" {
		return false
	}
	role, err := s.DB.GetWorkspaceMemberRole(workspaceID.String, userID)
	if err != nil {
		return false
	}
	return role == "owner" || role == "maintainer"
}

// handleAuthorSetSkillVisibility sets public/private visibility on a skill draft.
//
//	@Summary		Set skill draft visibility
//	@Description	Patches visibility to public or private for the draft author.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Skill draft ID"
//	@Param			body	body		map[string]string	true	"visibility: public|private"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/skills/{id}/visibility [patch]
func (s *Server) handleAuthorSetSkillVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSkillDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.userCanSetDraftVisibility(userID, draft.WorkspaceID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.patchSkillDraftVisibility(w, r, userID, id)
}

// handleAuthorSetSoulVisibility sets public/private visibility on a soul draft.
//
//	@Summary		Set soul draft visibility
//	@Description	Patches visibility to public or private for the draft author.
//	@Tags			playground
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Soul draft ID"
//	@Param			body	body		map[string]string	true	"visibility: public|private"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		403	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/souls/{id}/visibility [patch]
func (s *Server) handleAuthorSetSoulVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSoulDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.userCanSetDraftVisibility(userID, draft.WorkspaceID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.patchSoulDraftVisibility(w, r, userID, id)
}

// handleSetSkillVisibility sets skill draft visibility (admin marketplace moderation).
//
//	@Summary		Admin set skill visibility
//	@Description	Admin-only visibility patch for marketplace moderation.
//	@Tags			marketplace
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Skill draft ID"
//	@Param			body	body		map[string]string	true	"visibility: public|private"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/marketplace/skills/{id}/visibility [patch]
func (s *Server) handleSetSkillVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	s.patchSkillDraftVisibility(w, r, userID, id)
}

// handleSetSoulVisibility sets soul draft visibility (admin marketplace moderation).
//
//	@Summary		Admin set soul visibility
//	@Description	Admin-only visibility patch for marketplace moderation.
//	@Tags			marketplace
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		string	true	"Soul draft ID"
//	@Param			body	body		map[string]string	true	"visibility: public|private"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string
//	@Failure		401	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Router			/api/playground/marketplace/souls/{id}/visibility [patch]
func (s *Server) handleSetSoulVisibility(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	s.patchSoulDraftVisibility(w, r, userID, id)
}

func (s *Server) patchSkillDraftVisibility(w http.ResponseWriter, r *http.Request, userID, id string) {
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.DB.SetSkillDraftVisibility(id, req.Visibility); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AppendDraftAuditEvent("skill", id, userID, "visibility_changed", map[string]interface{}{
		"visibility": req.Visibility,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchSoulDraftVisibility(w http.ResponseWriter, r *http.Request, userID, id string) {
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.DB.SetSoulDraftVisibility(id, req.Visibility); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.DB.AppendDraftAuditEvent("soul", id, userID, "visibility_changed", map[string]interface{}{
		"visibility": req.Visibility,
	})
	w.WriteHeader(http.StatusNoContent)
}

func skillTagsFromFiles(files map[string]string) []string {
	content, ok := files["SKILL.md"]
	if !ok {
		return nil
	}
	m := skillTagsRE.FindStringSubmatch(content)
	if len(m) < 2 {
		return nil
	}
	raw := strings.TrimSpace(m[1])
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tag := strings.Trim(strings.TrimSpace(p), `"'`)
		if tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func soulCompatibleSkills(fm map[string]interface{}) []string {
	if fm == nil {
		return nil
	}
	raw, ok := fm["compatible_skills"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

// --- Marketplace preview (Tier B item B2) -------------------------------
//
// Read-only snippets so authors can browse marketplace entries before
// committing to fork. Returns description + bounded excerpts (max
// previewMaxBytes per file) of authoritative files: prompt.md / SKILL.md
// for skills, body for souls. The full draft is intentionally NOT
// returned to keep payloads small and discourage copy-paste piracy.

const previewMaxBytes = 4 * 1024

type marketplaceSkillPreview struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	AuthorWorkspaceID string   `json:"author_workspace_id,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	UpdatedAt         string   `json:"updated_at,omitempty"`
	PromotedCommit    string   `json:"promoted_commit,omitempty"`
	// PromptExcerpt is the first previewMaxBytes of prompt.md (if any).
	PromptExcerpt string `json:"prompt_excerpt,omitempty"`
	// FileList is filename → byte size for orientation (no content).
	FileList map[string]int `json:"file_list,omitempty"`
}

type marketplaceSoulPreview struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	AuthorWorkspaceID string   `json:"author_workspace_id,omitempty"`
	CompatibleSkills  []string `json:"compatible_skills,omitempty"`
	UpdatedAt         string   `json:"updated_at,omitempty"`
	PromotedCommit    string   `json:"promoted_commit,omitempty"`
	// BodyExcerpt is the first previewMaxBytes of the soul body.
	BodyExcerpt string `json:"body_excerpt,omitempty"`
	// SchemaVersion + selected frontmatter keys without secrets.
	SchemaVersion string `json:"schema_version,omitempty"`
}

// handleGetMarketplaceSkillPreview — GET /api/marketplace/skills/{id}/preview
func (s *Server) handleGetMarketplaceSkillPreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSkillDraft(id)
	if err != nil || d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if d.Visibility != "shared" {
		// Don't disclose existence of private drafts.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	preview := marketplaceSkillPreview{
		ID:                d.ID,
		Name:              d.Name,
		Description:       d.Description,
		AuthorWorkspaceID: d.WorkspaceID.String,
		Tags:              skillTagsFromFiles(d.Files),
		UpdatedAt:         d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		PromotedCommit:    d.PromotedCommit.String,
		FileList:          make(map[string]int, len(d.Files)),
	}
	for path, content := range d.Files {
		preview.FileList[path] = len(content)
		if path == "prompt.md" && preview.PromptExcerpt == "" {
			preview.PromptExcerpt = truncate(content, previewMaxBytes)
		}
	}
	writeJSON(w, http.StatusOK, preview)
}

// handleGetMarketplaceSoulPreview — GET /api/marketplace/souls/{id}/preview
func (s *Server) handleGetMarketplaceSoulPreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSoulDraft(id)
	if err != nil || d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if d.Visibility != "shared" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	preview := marketplaceSoulPreview{
		ID:                d.ID,
		Name:              d.Name,
		Description:       d.Description,
		AuthorWorkspaceID: d.WorkspaceID.String,
		CompatibleSkills:  soulCompatibleSkills(d.Frontmatter),
		UpdatedAt:         d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		PromotedCommit:    d.PromotedCommit.String,
		BodyExcerpt:       truncate(d.Body, previewMaxBytes),
		SchemaVersion:     d.SchemaVersion,
	}
	writeJSON(w, http.StatusOK, preview)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Cut at a newline boundary if reasonably close to keep readability.
	cut := strings.LastIndex(s[:max], "\n")
	if cut < max/2 {
		cut = max
	}
	return s[:cut] + "\n\n…truncated for preview…"
}

// --- Export / Import (marketplace) -----------------------------------------
//
// GET  /api/marketplace/skills/{id}/export  — full skill JSON (public)
// GET  /api/marketplace/souls/{id}/export   — full soul  JSON (public)
// POST /api/admin/marketplace/skills/import — create shared skill (admin)
// POST /api/admin/marketplace/souls/import  — create shared soul  (admin)

type skillExportPayload struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Files       map[string]string `json:"files"`
}

type soulExportPayload struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Frontmatter map[string]interface{} `json:"frontmatter"`
	Body        string                 `json:"body"`
}

func (s *Server) handleExportMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSkillDraft(id)
	if err != nil || d == nil || d.Visibility != "shared" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	payload := skillExportPayload{
		Name:        d.Name,
		Description: d.Description,
		Files:       d.Files,
	}
	safeName := sanitizeFilename(d.Name)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeName+`.json"`)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleExportMarketplaceSoul(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSoulDraft(id)
	if err != nil || d == nil || d.Visibility != "shared" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	payload := soulExportPayload{
		Name:        d.Name,
		Description: d.Description,
		Frontmatter: d.Frontmatter,
		Body:        d.Body,
	}
	safeName := sanitizeFilename(d.Name)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeName+`.json"`)
	writeJSON(w, http.StatusOK, payload)
}

func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	s := b.String()
	if s == "" {
		return "export"
	}
	return s
}

func (s *Server) handleImportMarketplaceSkill(w http.ResponseWriter, r *http.Request) {
	var req skillExportPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	d, err := s.DB.CreateSkillDraft(req.Name, req.Description, userID, "")
	if err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	if len(req.Files) > 0 {
		if err := s.DB.UpdateSkillDraftFiles(d.ID, req.Files); err != nil {
			http.Error(w, "files failed", http.StatusInternalServerError)
			return
		}
	}
	if err := s.DB.SetSkillDraftVisibility(d.ID, "shared"); err != nil {
		http.Error(w, "visibility failed", http.StatusInternalServerError)
		return
	}
	d, _ = s.DB.GetSkillDraft(d.ID)
	writeJSON(w, http.StatusCreated, summarizeSkill(d))
}

func (s *Server) handleImportMarketplaceSoul(w http.ResponseWriter, r *http.Request) {
	var req soulExportPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	d, err := s.DB.CreateSoulDraft(req.Name, req.Description, userID, "")
	if err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	if req.Frontmatter != nil || req.Body != "" {
		fm := req.Frontmatter
		if fm == nil {
			fm = map[string]interface{}{}
		}
		if err := s.DB.UpdateSoulDraft(d.ID, fm, req.Body); err != nil {
			http.Error(w, "content failed", http.StatusInternalServerError)
			return
		}
	}
	if err := s.DB.SetSoulDraftVisibility(d.ID, "shared"); err != nil {
		http.Error(w, "visibility failed", http.StatusInternalServerError)
		return
	}
	d, _ = s.DB.GetSoulDraft(d.ID)
	writeJSON(w, http.StatusCreated, summarizeSoul(d))
}

// --- Admin system skills/souls management -----------------------------------

func (s *Server) handleAdminListSystemSkills(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSystemSkillDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]marketplaceSkillListing, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, marketplaceSkillListing{
			playgroundSkillSummary: summarizeSkill(d),
			AuthorWorkspaceID:      d.WorkspaceID.String,
			Tags:                   skillTagsFromFiles(d.Files),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"skills": out})
}

func (s *Server) handleAdminListSystemSouls(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.DB.ListSystemSoulDrafts()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]marketplaceSoulListing, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, marketplaceSoulListing{
			playgroundSoulSummary: summarizeSoul(d),
			AuthorWorkspaceID:     d.WorkspaceID.String,
			CompatibleSkills:      soulCompatibleSkills(d.Frontmatter),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"souls": out})
}

func (s *Server) handleAdminArchiveSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSkillDraft(id)
	if err != nil || d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.DB.ArchiveSkillDraft(id); err != nil {
		http.Error(w, "archive failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminArchiveSoul(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.DB.GetSoulDraft(id)
	if err != nil || d == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.DB.ArchiveSoulDraft(id); err != nil {
		http.Error(w, "archive failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ensure sql package stays in use even if all callsites are removed during
// refactors. (References scoped local; helps linter ergonomics.)
var _ = sql.ErrNoRows
