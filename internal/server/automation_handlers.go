package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// GetAutomationCatalog godoc
// @Summary      List automation catalog templates
// @Description  Returns ready-made automation templates for onboarding (static, not workspace-scoped).
// @Tags         automations
// @Produce      json
// @Success      200  {object}  AutomationCatalogListResponse
// @Failure      401  {string}  string  "unauthorized"
// @Failure      500  {string}  string  "internal error"
// @Router       /api/automations/catalog [get]
// @Security     BearerAuth
func (s *Server) handleGetAutomationCatalog(w http.ResponseWriter, r *http.Request) {
	templates := make([]AutomationCatalogEntryResponse, 0, len(automationCatalog))
	for _, e := range automationCatalog {
		templates = append(templates, AutomationCatalogEntryResponse{
			Key:            e.Key,
			Title:          e.Title,
			Description:    e.Description,
			SuggestedCron:  e.SuggestedCron,
			PromptTemplate: e.PromptTemplate,
			SkillRef:       e.SkillRef,
		})
	}
	writeJSON(w, http.StatusOK, AutomationCatalogListResponse{Templates: templates})
}

func automationToResponse(a *db.Automation) AutomationResponse {
	return AutomationResponse{
		ID:          a.ID,
		WorkspaceID: a.WorkspaceID,
		Name:        a.Name,
		SkillRef:    a.SkillRef,
		Cron:        a.Cron,
		ChannelID:   a.ChannelID,
		Enabled:     a.Enabled,
		Config:      string(a.Config),
		LastRunAt:   formatTimePtr(a.LastRunAt),
		NextRunAt:   formatTimePtr(a.NextRunAt),
		LastError:   a.LastError,
		CreatedAt:   formatTime(a.CreatedAt),
		UpdatedAt:   formatTime(a.UpdatedAt),
	}
}

type automationConfigPayload struct {
	ChannelID   string `json:"channel_id"`
	WorkspaceID string `json:"workspace_id"`
	WechatID    string `json:"wechat_user_id"`
	Prompt      string `json:"prompt"`
}

func buildAutomationConfig(channelID, workspaceID, wechatUserID, prompt string) (json.RawMessage, error) {
	cfg := automationConfigPayload{
		ChannelID:   channelID,
		WorkspaceID: workspaceID,
		WechatID:    wechatUserID,
		Prompt:      prompt,
	}
	return json.Marshal(cfg)
}

func parseAutomationConfig(raw json.RawMessage) (automationConfigPayload, error) {
	var cfg automationConfigPayload
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Server) validateAutomationChannel(_ context.Context, workspaceID, channelID string) (*db.IMChannel, int, error) {
	ch, err := s.DB.GetIMChannel(channelID)
	if errors.Is(err, sql.ErrNoRows) {
		// Unknown channel id — a client error, not a server fault.
		return nil, http.StatusBadRequest, nil
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if ch == nil || ch.WorkspaceID != workspaceID {
		return nil, http.StatusBadRequest, nil
	}
	return ch, http.StatusOK, nil
}

func (s *Server) validateCron(cronExpr string) error {
	_, err := db.ComputeNextRun(cronExpr, time.Now().UTC())
	return err
}

// handleListAutomations — GET /api/workspaces/{id}/automations
//
//	@Summary     List workspace automations
//	@Description Returns all scheduled automations for the workspace.
//	@Tags        Automations
//	@Produce     json
//	@Param       id  path  string  true  "Workspace ID"
//	@Success     200  {object}  AutomationListResponse
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Router      /api/workspaces/{id}/automations [get]
func (s *Server) handleListAutomations(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer", "developer", "viewer") {
		return
	}
	list, err := s.DB.ListAutomations(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := AutomationListResponse{Automations: make([]AutomationResponse, 0, len(list))}
	for _, a := range list {
		resp.Automations = append(resp.Automations, automationToResponse(a))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCreateAutomation — POST /api/workspaces/{id}/automations
//
//	@Summary     Create automation
//	@Description Creates a scheduled automation. Recomputes next_run_at from cron.
//	@Tags        Automations
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                   true  "Workspace ID"
//	@Param       body  body  AutomationCreateRequest  true  "Automation payload"
//	@Success     201   {object}  AutomationResponse
//	@Failure     400   {string}  string  "bad request"
//	@Failure     403   {string}  string  "insufficient permissions"
//	@Router      /api/workspaces/{id}/automations [post]
func (s *Server) handleCreateAutomation(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	var req AutomationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.SkillRef = strings.TrimSpace(req.SkillRef)
	req.Cron = strings.TrimSpace(req.Cron)
	req.ChannelID = strings.TrimSpace(req.ChannelID)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Name == "" || req.SkillRef == "" || req.Cron == "" || req.ChannelID == "" || req.Prompt == "" {
		http.Error(w, "name, skill_ref, cron, channel_id, and prompt are required", http.StatusBadRequest)
		return
	}
	if err := s.validateCron(req.Cron); err != nil {
		http.Error(w, "invalid cron expression", http.StatusBadRequest)
		return
	}
	ch, status, err := s.validateAutomationChannel(r.Context(), wsID, req.ChannelID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if status != http.StatusOK {
		http.Error(w, "channel not found in workspace", http.StatusBadRequest)
		return
	}
	wechatUserID := ch.UserID
	cfg, err := buildAutomationConfig(req.ChannelID, wsID, wechatUserID, req.Prompt)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	next, err := db.ComputeNextRun(req.Cron, time.Now().UTC())
	if err != nil {
		http.Error(w, "invalid cron expression", http.StatusBadRequest)
		return
	}
	a := &db.Automation{
		ID:          uuid.New().String(),
		WorkspaceID: wsID,
		Name:        req.Name,
		SkillRef:    req.SkillRef,
		Cron:        req.Cron,
		ChannelID:   req.ChannelID,
		Config:      cfg,
		Enabled:     enabled,
		NextRunAt:   &next,
	}
	if err := s.DB.CreateAutomation(r.Context(), a); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	created, err := s.DB.GetAutomation(r.Context(), a.ID)
	if err != nil || created == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, automationToResponse(created))
}

// handleGetAutomation — GET /api/workspaces/{id}/automations/{automationId}
//
//	@Summary     Get automation
//	@Tags        Automations
//	@Produce     json
//	@Param       id            path  string  true  "Workspace ID"
//	@Param       automationId  path  string  true  "Automation ID"
//	@Success     200  {object}  AutomationResponse
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Failure     404  {string}  string  "not found"
//	@Router      /api/workspaces/{id}/automations/{automationId} [get]
func (s *Server) handleGetAutomation(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	automationID := chi.URLParam(r, "automationId")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer", "developer", "viewer") {
		return
	}
	a, err := s.DB.GetAutomation(r.Context(), automationID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if a == nil || a.WorkspaceID != wsID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, automationToResponse(a))
}

// handlePatchAutomation — PATCH /api/workspaces/{id}/automations/{automationId}
//
//	@Summary     Update automation
//	@Tags        Automations
//	@Accept      json
//	@Produce     json
//	@Param       id            path  string                   true  "Workspace ID"
//	@Param       automationId  path  string                   true  "Automation ID"
//	@Param       body          body  AutomationPatchRequest   true  "Fields to update"
//	@Success     200           {object}  AutomationResponse
//	@Failure     400           {string}  string  "bad request"
//	@Failure     403           {string}  string  "insufficient permissions"
//	@Failure     404           {string}  string  "not found"
//	@Router      /api/workspaces/{id}/automations/{automationId} [patch]
func (s *Server) handlePatchAutomation(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	automationID := chi.URLParam(r, "automationId")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	var req AutomationPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	existing, err := s.DB.GetAutomation(r.Context(), automationID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil || existing.WorkspaceID != wsID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cfg, err := parseAutomationConfig(existing.Config)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
		if existing.Name == "" {
			http.Error(w, "name cannot be empty", http.StatusBadRequest)
			return
		}
	}
	if req.SkillRef != nil {
		existing.SkillRef = strings.TrimSpace(*req.SkillRef)
		if existing.SkillRef == "" {
			http.Error(w, "skill_ref cannot be empty", http.StatusBadRequest)
			return
		}
	}
	if req.Cron != nil {
		existing.Cron = strings.TrimSpace(*req.Cron)
		if existing.Cron == "" {
			http.Error(w, "cron cannot be empty", http.StatusBadRequest)
			return
		}
		if err := s.validateCron(existing.Cron); err != nil {
			http.Error(w, "invalid cron expression", http.StatusBadRequest)
			return
		}
	}
	if req.ChannelID != nil {
		existing.ChannelID = strings.TrimSpace(*req.ChannelID)
		if existing.ChannelID == "" {
			http.Error(w, "channel_id cannot be empty", http.StatusBadRequest)
			return
		}
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	channelID := existing.ChannelID
	if req.ChannelID != nil {
		channelID = existing.ChannelID
	}
	ch, status, err := s.validateAutomationChannel(r.Context(), wsID, channelID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if status != http.StatusOK {
		http.Error(w, "channel not found in workspace", http.StatusBadRequest)
		return
	}
	wechatUserID := cfg.WechatID
	if ch.UserID != "" {
		wechatUserID = ch.UserID
	}
	prompt := cfg.Prompt
	if req.Prompt != nil {
		prompt = strings.TrimSpace(*req.Prompt)
		if prompt == "" {
			http.Error(w, "prompt cannot be empty", http.StatusBadRequest)
			return
		}
	}
	newCfg, err := buildAutomationConfig(channelID, wsID, wechatUserID, prompt)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	existing.Config = newCfg
	existing.ChannelID = channelID
	if err := s.DB.UpdateAutomation(r.Context(), existing); err != nil {
		if strings.Contains(err.Error(), "parse cron") {
			http.Error(w, "invalid cron expression", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := s.DB.GetAutomation(r.Context(), automationID)
	if err != nil || updated == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, automationToResponse(updated))
}

// handleDeleteAutomation — DELETE /api/workspaces/{id}/automations/{automationId}
//
//	@Summary     Delete automation
//	@Tags        Automations
//	@Param       id            path  string  true  "Workspace ID"
//	@Param       automationId  path  string  true  "Automation ID"
//	@Success     204  "No Content"
//	@Failure     403  {string}  string  "insufficient permissions"
//	@Failure     404  {string}  string  "not found"
//	@Router      /api/workspaces/{id}/automations/{automationId} [delete]
func (s *Server) handleDeleteAutomation(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	automationID := chi.URLParam(r, "automationId")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	existing, err := s.DB.GetAutomation(r.Context(), automationID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil || existing.WorkspaceID != wsID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.DB.DeleteAutomation(r.Context(), automationID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
