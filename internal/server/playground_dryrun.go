package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
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
// without actually calling the LLM. A future iteration will wire this
// through llmproxy; for v1 the response is enough to render a preview
// panel in the playground UI.
type playgroundDryRunResponse struct {
	SystemPrompt string                      `json:"system_prompt"`
	Tools        []playgroundDryRunTool      `json:"tools"`
	Messages     []playgroundDryRunMessage   `json:"messages"`
	SoulSummary  *playgroundDryRunSoulInfo   `json:"soul,omitempty"`
	SkillSummary playgroundDryRunSkillInfo   `json:"skill"`
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

	writeJSON(w, http.StatusOK, resp)
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
