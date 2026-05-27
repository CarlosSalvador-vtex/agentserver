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
