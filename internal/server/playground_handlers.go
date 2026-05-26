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

// --- Skill drafts ----------------------------------------------------------

func (s *Server) handleListSkillDrafts(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	drafts, err := s.DB.ListSkillDraftsByAuthor(userID)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSkillSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, playgroundSkillSummary{
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			Status:        d.Status,
			PromotedPRURL: d.PromotedPRURL.String,
			UpdatedAt:     d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"drafts": out})
}

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

	draft, err := s.DB.CreateSkillDraft(req.Name, req.Description, userID)
	if err != nil {
		// UNIQUE (author, name) violation surfaces here.
		http.Error(w, fmt.Sprintf("create failed: %v", err), http.StatusConflict)
		return
	}
	RecordDraftAction("skill", "created")
	writeJSON(w, http.StatusCreated, summarizeSkill(draft))
}

func (s *Server) handleGetSkillDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSkillDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, playgroundSkillFull{
		playgroundSkillSummary: summarizeSkill(draft),
		Files:                  draft.Files,
	})
}

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
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

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
	w.WriteHeader(http.StatusNoContent)
}

// --- Soul drafts -----------------------------------------------------------

func (s *Server) handleListSoulDrafts(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	drafts, err := s.DB.ListSoulDraftsByAuthor(userID)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	out := make([]playgroundSoulSummary, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, playgroundSoulSummary{
			ID:            d.ID,
			Name:          d.Name,
			Description:   d.Description,
			Status:        d.Status,
			SchemaVersion: d.SchemaVersion,
			PromotedPRURL: d.PromotedPRURL.String,
			UpdatedAt:     d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"drafts": out})
}

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

	draft, err := s.DB.CreateSoulDraft(req.Name, req.Description, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf("create failed: %v", err), http.StatusConflict)
		return
	}
	RecordDraftAction("soul", "created")
	writeJSON(w, http.StatusCreated, summarizeSoul(draft))
}

func (s *Server) handleGetSoulDraft(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	draft, err := s.DB.GetSoulDraft(id)
	if err != nil || draft == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !draft.AuthorUserID.Valid || draft.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, playgroundSoulFull{
		playgroundSoulSummary: summarizeSoul(draft),
		Frontmatter:           draft.Frontmatter,
		Body:                  draft.Body,
	})
}

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
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

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
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---------------------------------------------------------------

func summarizeSkill(d *db.SkillDraft) playgroundSkillSummary {
	return playgroundSkillSummary{
		ID:              d.ID,
		Name:            d.Name,
		Description:     d.Description,
		Status:          d.Status,
		PromotedPRURL:   d.PromotedPRURL.String,
		PromotedPRState: d.PromotedPRState.String,
		PromotedCommit:  d.PromotedCommit.String,
		UpdatedAt:       d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func summarizeSoul(d *db.SoulDraft) playgroundSoulSummary {
	return playgroundSoulSummary{
		ID:              d.ID,
		Name:            d.Name,
		Description:     d.Description,
		Status:          d.Status,
		SchemaVersion:   d.SchemaVersion,
		PromotedPRURL:   d.PromotedPRURL.String,
		PromotedPRState: d.PromotedPRState.String,
		PromotedCommit:  d.PromotedCommit.String,
		UpdatedAt:       d.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
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
