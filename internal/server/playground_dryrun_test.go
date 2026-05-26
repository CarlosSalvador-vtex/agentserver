package server

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

func TestExtractToolsFromSkill_ToolsAndCommands(t *testing.T) {
	skill := &db.SkillDraft{
		Files: map[string]string{
			"openclaw.plugin.json": `{
				"id": "cobranca",
				"tools": ["lookup_debt", "generate_boleto"],
				"commands": [
					{"name": "/cobranca", "description": "start session"},
					{"name": "/refund", "description": ""}
				]
			}`,
		},
	}
	tools := extractToolsFromSkill(skill)
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools (2 + 2 commands), got %d: %+v", len(tools), tools)
	}
	// Tool name order: tools array first, then commands.
	if tools[0].Name != "lookup_debt" {
		t.Errorf("expected lookup_debt first, got %q", tools[0].Name)
	}
	for _, tl := range tools {
		if tl.Name == "/cobranca" && tl.Description != "start session" {
			t.Errorf("/cobranca should carry description, got %q", tl.Description)
		}
	}
}

func TestExtractToolsFromSkill_NoManifest(t *testing.T) {
	skill := &db.SkillDraft{Files: map[string]string{"prompt.md": "x"}}
	tools := extractToolsFromSkill(skill)
	if tools != nil {
		t.Errorf("expected nil tools when manifest absent, got %+v", tools)
	}
}

func TestComposeMessages(t *testing.T) {
	req := playgroundDryRunRequest{
		UserMessage: "/cobranca",
		History: []playgroundDryRunMessage{
			{Role: "user", Content: "previous"},
			{Role: "assistant", Content: "previous-reply"},
		},
	}
	msgs := composeMessages(req)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[2].Content != "/cobranca" || msgs[2].Role != "user" {
		t.Errorf("user message should be last, got %+v", msgs[2])
	}
}

func TestSortedKeys(t *testing.T) {
	in := map[string]string{"c": "", "a": "", "b": ""}
	got := sortedKeys(in)
	want := []string{"a", "b", "c"}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("sortedKeys: at %d got %q want %q", i, got[i], k)
		}
	}
}

func TestSkillDraftDryRun_FilesShape(t *testing.T) {
	// Builds the skill file list a dry-run reports back. Smoke against
	// the cobranca-like shape.
	skill := &db.SkillDraft{
		ID:           "test-id",
		Name:         "cobranca",
		AuthorUserID: sql.NullString{String: "u1", Valid: true},
		Files: map[string]string{
			"SKILL.md":               "# cobranca",
			"prompt.md":              "Você é Júlia",
			"index.mjs":              "export default {}",
			"openclaw.plugin.json":   `{"id":"cobranca","configSchema":{}}`,
			"references/leads.json": `[{"cpf_last_3":"111"}]`,
		},
	}
	files := sortedKeys(skill.Files)
	if !strings.Contains(strings.Join(files, ","), "references/leads.json") {
		t.Errorf("expected references/leads.json in files; got %v", files)
	}
}
