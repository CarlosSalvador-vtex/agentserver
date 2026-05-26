package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// Playground dry-run config — kept here vs values.yaml since these are
// runtime-only knobs the operator may want to tune without redeploy.
const (
	playgroundDryRunModelDefault = "claude-sonnet-4-6"
	playgroundDryRunMaxTokens    = 1024
)

// playgroundDryRunRequest carries optional inputs to the dry-run
// endpoint. soul_ref points at a soul that should be layered on top of
// this skill draft to preview their combined system prompt.
type playgroundDryRunRequest struct {
	SoulRef     string                            `json:"soul_ref,omitempty"`
	UserMessage string                            `json:"user_message,omitempty"`
	History     []playgroundDryRunMessage         `json:"history,omitempty"`
	Config      map[string]interface{}            `json:"config,omitempty"`
}

type playgroundDryRunMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// playgroundDryRunResponse returns the composed prompt + tool surface
// + an actual LLM completion when llmproxy is reachable. Falls back to
// preview-only when llmproxy is unavailable or returns an error — the
// frontend always renders the prompt panel + completion when present.
type playgroundDryRunResponse struct {
	SystemPrompt string                      `json:"system_prompt"`
	Tools        []playgroundDryRunTool      `json:"tools"`
	Messages     []playgroundDryRunMessage   `json:"messages"`
	SoulSummary  *playgroundDryRunSoulInfo   `json:"soul,omitempty"`
	SkillSummary playgroundDryRunSkillInfo   `json:"skill"`
	// Completion is the LLM's reply, populated when llmproxy returned
	// a response. Empty when llmproxy is not configured or the call
	// failed — CompletionError carries the diagnostic in that case.
	Completion      string `json:"completion,omitempty"`
	CompletionModel string `json:"completion_model,omitempty"`
	CompletionError string `json:"completion_error,omitempty"`
}

type playgroundDryRunTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type playgroundDryRunSoulInfo struct {
	Name    string `json:"name"`
	Source  string `json:"source"` // "draft" or "git"
	Voice   string `json:"voice,omitempty"`
	MaxTurn int    `json:"max_turns,omitempty"`
}

type playgroundDryRunSkillInfo struct {
	Name      string   `json:"name"`
	Files     []string `json:"files"`
	HasPrompt bool     `json:"has_prompt"`
	HasIndex  bool     `json:"has_index"`
}

// handleSkillDraftDryRun composes the system prompt that this draft +
// optional soul ref would produce inside a sandbox. Returns prompt +
// tool surface without invoking the LLM. The frontend renders this as
// a preview so authors can validate prompt assembly before promoting.
func (s *Server) handleSkillDraftDryRun(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	skill, err := s.DB.GetSkillDraft(id)
	if err != nil || skill == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !skill.AuthorUserID.Valid || skill.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}

	var req playgroundDryRunRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
	}

	resp := playgroundDryRunResponse{
		Tools:    extractToolsFromSkill(skill),
		Messages: composeMessages(req),
		SkillSummary: playgroundDryRunSkillInfo{
			Name:      skill.Name,
			Files:     sortedKeys(skill.Files),
			HasPrompt: skill.Files["prompt.md"] != "",
			HasIndex:  skill.Files["index.mjs"] != "",
		},
	}

	// Compose system prompt: soul.body first (identity), then skill
	// prompt.md (capability). Matches the docs/playground-design.md
	// convention so previews mirror runtime behavior.
	var promptParts []string
	if req.SoulRef != "" {
		soulBody, soulInfo, err := s.resolveSoulForPreview(req.SoulRef, userID)
		if err != nil {
			http.Error(w, fmt.Sprintf("soul ref: %v", err), http.StatusBadRequest)
			return
		}
		if soulBody != "" {
			promptParts = append(promptParts, soulBody)
		}
		resp.SoulSummary = soulInfo
	}
	if body := skill.Files["prompt.md"]; body != "" {
		promptParts = append(promptParts, body)
	}
	resp.SystemPrompt = strings.Join(promptParts, "\n\n---\n\n")

	// Optional LLM round-trip via llmproxy. When the proxy URL is unset
	// (dev / first-boot), we return the preview-only shape so the
	// frontend always has something to render.
	if s.LLMProxyURL != "" && req.UserMessage != "" {
		model := playgroundDryRunModelDefault
		if envModel := strings.TrimSpace(os.Getenv("PLAYGROUND_DRYRUN_MODEL")); envModel != "" {
			model = envModel
		}
		completion, err := s.callLLMProxyForDryRunForUser(r.Context(), userID, model, resp.SystemPrompt, resp.Messages)
		if err != nil {
			resp.CompletionError = err.Error()
		} else {
			resp.Completion = completion
			resp.CompletionModel = model
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// callLLMProxyForDryRun sends the composed dry-run payload to llmproxy
// in Anthropic /v1/messages shape. llmproxy validates the bearer
// against the workspace_tokens catalog, so we mint (or reuse) the
// workspace proxy token of the caller's first workspace before
// dispatching. INTERNAL_API_SECRET alone is rejected by llmproxy
// (it only honours tokens that map to a workspace row).
func (s *Server) callLLMProxyForDryRunForUser(ctx context.Context, userID, model, systemPrompt string, msgs []playgroundDryRunMessage) (string, error) {
	wss, err := s.DB.ListWorkspacesByUser(userID)
	if err != nil || len(wss) == 0 {
		return "", fmt.Errorf("dry-run LLM call needs at least one workspace membership for user %s", userID)
	}
	wsID := wss[0].ID
	proxyToken, err := s.DB.GetOrCreateWorkspaceToken(wsID)
	if err != nil {
		return "", fmt.Errorf("mint workspace proxy token (ws=%s): %w", wsID, err)
	}

	type anthMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type anthReq struct {
		Model     string    `json:"model"`
		System    string    `json:"system,omitempty"`
		MaxTokens int       `json:"max_tokens"`
		Messages  []anthMsg `json:"messages"`
	}

	body := anthReq{
		Model:     model,
		System:    systemPrompt,
		MaxTokens: playgroundDryRunMaxTokens,
		Messages:  make([]anthMsg, 0, len(msgs)),
	}
	for _, m := range msgs {
		if m.Role == "" || m.Content == "" {
			continue
		}
		body.Messages = append(body.Messages, anthMsg{Role: m.Role, Content: m.Content})
	}
	if len(body.Messages) == 0 {
		return "", fmt.Errorf("no user message to send")
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(s.LLMProxyURL, "/") + "/v1/messages"
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "POST", url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", proxyToken)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("llmproxy %d: %s", resp.StatusCode, string(rb))
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	var out strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	return out.String(), nil
}

// resolveSoulForPreview turns a soul ref into the body string the
// dry-run should compose. Currently supports draft: refs only (the
// most useful case in the playground). git: refs returns a placeholder
// with name + a flag indicating the body should be fetched from the
// chart ConfigMap at runtime (out of scope for the preview).
func (s *Server) resolveSoulForPreview(ref, userID string) (string, *playgroundDryRunSoulInfo, error) {
	if strings.HasPrefix(ref, "draft:") {
		soulID := strings.TrimPrefix(ref, "draft:")
		soul, err := s.DB.GetSoulDraft(soulID)
		if err != nil || soul == nil {
			return "", nil, fmt.Errorf("soul draft %s: not found", soulID)
		}
		if !soul.AuthorUserID.Valid || soul.AuthorUserID.String != userID {
			return "", nil, fmt.Errorf("soul draft %s: not yours", soulID)
		}
		info := &playgroundDryRunSoulInfo{
			Name:   soul.Name,
			Source: "draft",
		}
		if voice, ok := soul.Frontmatter["voice"].(map[string]interface{}); ok {
			if lang, ok := voice["language"].(string); ok {
				info.Voice = lang
			}
		}
		if c, ok := soul.Frontmatter["constraints"].(map[string]interface{}); ok {
			if mt, ok := c["max_turns"].(float64); ok {
				info.MaxTurn = int(mt)
			}
		}
		return soul.Body, info, nil
	}
	if strings.HasPrefix(ref, "git:") {
		// Git soul bodies live on disk in the chart; preview can show
		// a synthetic note. Real-LLM dry-run (future PR) reads the file
		// directly.
		name := strings.TrimPrefix(ref, "git:")
		if at := strings.Index(name, "@"); at >= 0 {
			name = name[:at]
		}
		return "[git soul " + name + " — body resolved at runtime]",
			&playgroundDryRunSoulInfo{Name: name, Source: "git"}, nil
	}
	return "", nil, fmt.Errorf("unsupported ref prefix")
}

// extractToolsFromSkill peeks at openclaw.plugin.json to surface
// declared tool names. We only read the JSON manifest because the
// authoritative tool list there is what OpenClaw will register.
func extractToolsFromSkill(skill *db.SkillDraft) []playgroundDryRunTool {
	manifest, ok := skill.Files["openclaw.plugin.json"]
	if !ok {
		return nil
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(manifest), &parsed); err != nil {
		return nil
	}
	// Newer manifests carry { commands: [{name, description}], tools: [name] }
	// (see cobranca pre-PR-13). We read both shapes optimistically.
	var out []playgroundDryRunTool
	if tools, ok := parsed["tools"].([]interface{}); ok {
		for _, t := range tools {
			if name, ok := t.(string); ok {
				out = append(out, playgroundDryRunTool{Name: name})
			}
		}
	}
	if cmds, ok := parsed["commands"].([]interface{}); ok {
		for _, c := range cmds {
			if m, ok := c.(map[string]interface{}); ok {
				name, _ := m["name"].(string)
				desc, _ := m["description"].(string)
				if name != "" {
					out = append(out, playgroundDryRunTool{Name: name, Description: desc})
				}
			}
		}
	}
	return out
}

// composeMessages returns the message array a real LLM call would send,
// in OpenAI/Anthropic shape order (system first when set; we don't add
// it here — caller adds it after computing the system prompt).
func composeMessages(req playgroundDryRunRequest) []playgroundDryRunMessage {
	out := make([]playgroundDryRunMessage, 0, len(req.History)+1)
	out = append(out, req.History...)
	if req.UserMessage != "" {
		out = append(out, playgroundDryRunMessage{Role: "user", Content: req.UserMessage})
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
