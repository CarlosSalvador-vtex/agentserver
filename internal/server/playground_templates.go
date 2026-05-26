package server

import (
	"encoding/json"
	"net/http"
)

// Sprint 2 PR-7 — Tier 1 picker gaps (improvements.md gap a).
//
// Templates are git-pinned skills + souls shipped with the chart (e.g.
// `deploy/helm/agentserver/skills/cobranca/`). The picker needs a way to
// list them so users can opt in without first creating a draft. We expose a
// hardcoded registry here; a follow-up (#17 tenant catalog) will move this
// into the database, then into per-workspace scope.
//
// Until then, the source of truth is this slice + the docs in
// docs/playground-design.md §4.3 (composition refs grammar).

type templateSkill struct {
	Name         string                 `json:"name"`
	Ref          string                 `json:"ref"`
	Description  string                 `json:"description"`
	ConfigSchema map[string]interface{} `json:"config_schema,omitempty"`
}

type templateSoul struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
}

var bundledSkillTemplates = []templateSkill{
	{
		Name:        "cobranca",
		Ref:         "git:cobranca@main",
		Description: "Cobrança pt-BR (mock) — debt-collection skill for OpenClaw + Hermes. LGPD-safe synthetic data.",
		ConfigSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]interface{}{},
		},
	},
}

var bundledSoulTemplates = []templateSoul{
	// Empty for now. Future: bundle the Julia persona as a git-pinned soul.
}

func (s *Server) handleListSkillTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": bundledSkillTemplates})
}

func (s *Server) handleListSoulTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": bundledSoulTemplates})
}

func (s *Server) handleGetSkillTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	for _, t := range bundledSkillTemplates {
		if t.Name == name {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	http.Error(w, "template not found", http.StatusNotFound)
}

// jsonMust is a tiny helper kept here so the registry literal above can use
// inline JSON for richer configSchemas without hand-building Go maps. Not
// used yet; reserved for the future schema-rich templates the marketplace
// (#18) will need.
func jsonMust(raw string) map[string]interface{} {
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		panic("playground_templates: invalid embedded JSON: " + err.Error())
	}
	return out
}

var _ = jsonMust // silence "unused" until first call site lands
