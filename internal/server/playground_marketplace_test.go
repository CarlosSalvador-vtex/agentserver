package server

import "testing"

func TestSkillTagsFromFiles(t *testing.T) {
	files := map[string]string{
		"SKILL.md": `---
name: cobranca
tags: [cobranca, ptbr, finance, mock, lgpd]
---
`,
	}
	got := skillTagsFromFiles(files)
	if len(got) != 5 {
		t.Fatalf("expected 5 tags, got %v", got)
	}
	if got[0] != "cobranca" || got[4] != "lgpd" {
		t.Errorf("unexpected tags: %v", got)
	}
}

func TestSoulCompatibleSkills(t *testing.T) {
	fm := map[string]interface{}{
		"compatible_skills": []interface{}{"cobranca", "support"},
	}
	got := soulCompatibleSkills(fm)
	if len(got) != 2 || got[0] != "cobranca" {
		t.Errorf("got %v", got)
	}
}

func TestResolveDryRunModel(t *testing.T) {
	t.Setenv("PLAYGROUND_DRYRUN_MODEL", "env-model")
	if got := resolveDryRunModel("req-model"); got != "req-model" {
		t.Errorf("request model: got %q", got)
	}
	if got := resolveDryRunModel(""); got != "env-model" {
		t.Errorf("env model: got %q", got)
	}
	t.Setenv("PLAYGROUND_DRYRUN_MODEL", "")
	if got := resolveDryRunModel(""); got != playgroundDryRunModelDefault {
		t.Errorf("default model: got %q", got)
	}
}
