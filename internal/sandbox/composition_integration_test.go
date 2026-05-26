// Integration tests for ResolveComposition require TEST_DATABASE_URL pointing
// at a Postgres instance with all agentserver migrations applied.
package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping composition integration test")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return d
}

func TestResolveComposition_DraftSoulAndSkill(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	ctx := context.Background()
	suffix := t.Name()
	author := "tier1-test-" + suffix

	soul, err := d.CreateSoulDraft("soul-"+suffix, "test soul", author)
	if err != nil {
		t.Fatalf("create soul: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM soul_drafts WHERE id = $1`, soul.ID)
	})

	if err := d.UpdateSoulDraft(soul.ID, map[string]interface{}{
		"id": "julia",
		"constraints": map[string]interface{}{
			"max_turns": float64(10),
		},
	}, "Sou a Julia, atendente de cobrança."); err != nil {
		t.Fatalf("update soul: %v", err)
	}

	skill, err := d.CreateSkillDraft("skill-"+suffix, "test skill", author)
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM skill_drafts WHERE id = $1`, skill.ID)
	})

	if err := d.UpdateSkillDraftFiles(skill.ID, map[string]string{
		"prompt.md":              "# Cobrança\nInstruções do skill.",
		"openclaw.plugin.json":   `{"id":"` + skill.Name + `","configSchema":{"type":"object"}}`,
	}); err != nil {
		t.Fatalf("update skill files: %v", err)
	}

	sbxID := "sbx-comp-" + suffix
	ns := "agent-ws-test"
	if err := d.CreateSandboxComposition(
		sbxID,
		"draft:"+soul.ID,
		[]string{"draft:" + skill.ID},
		nil,
		false,
	); err != nil {
		t.Fatalf("create composition: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM sandbox_compositions WHERE sandbox_id = $1`, sbxID)
	})

	m := &Manager{db: d, cfg: Config{}}
	resolved, err := m.ResolveComposition(ctx, sbxID, ns, SandboxTypeOpenclaw.String())
	if err != nil {
		t.Fatalf("ResolveComposition: %v", err)
	}
	if len(resolved.EphemeralConfigMaps) != 2 {
		t.Fatalf("EphemeralConfigMaps = %d, want 2", len(resolved.EphemeralConfigMaps))
	}
	if resolved.SoulBody == "" || !strings.Contains(resolved.SoulBody, "Julia") {
		t.Fatalf("SoulBody = %q, want persona body", resolved.SoulBody)
	}
	if len(resolved.EnabledSkillNames) != 1 || resolved.EnabledSkillNames[0] != skill.Name {
		t.Fatalf("EnabledSkillNames = %v, want [%s]", resolved.EnabledSkillNames, skill.Name)
	}

	var soulMount, skillMount bool
	for _, mount := range resolved.ExtraMounts {
		if mount.MountPath == "/home/agent/.openclaw/soul.md" {
			soulMount = true
		}
		if strings.HasPrefix(mount.MountPath, "/home/agent/.openclaw/extensions/"+skill.Name+"/") {
			skillMount = true
		}
	}
	if !soulMount {
		t.Fatal("missing soul.md mount at /home/agent/.openclaw/soul.md")
	}
	if !skillMount {
		t.Fatal("missing skill mount under /home/agent/.openclaw/extensions/")
	}
}

func TestResolveComposition_GitRefsAreNoop(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	ctx := context.Background()
	suffix := t.Name()
	sbxID := "sbx-git-" + suffix

	if err := d.CreateSandboxComposition(
		sbxID,
		"git:cobranca@abc123",
		[]string{"git:my-skill@main"},
		nil,
		false,
	); err != nil {
		t.Fatalf("create composition: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM sandbox_compositions WHERE sandbox_id = $1`, sbxID)
	})

	m := &Manager{db: d, cfg: Config{}}
	resolved, err := m.ResolveComposition(ctx, sbxID, "agent-ws-test", SandboxTypeOpenclaw.String())
	if err != nil {
		t.Fatalf("ResolveComposition: %v", err)
	}
	if len(resolved.EphemeralConfigMaps) != 0 {
		t.Fatalf("git refs should not create ephemeral CMs, got %d", len(resolved.EphemeralConfigMaps))
	}
	if resolved.SoulBody != "" {
		t.Fatalf("SoulBody should be empty for git soul ref, got %q", resolved.SoulBody)
	}
	if len(resolved.EnabledSkillNames) != 1 || resolved.EnabledSkillNames[0] != "my-skill" {
		t.Fatalf("EnabledSkillNames = %v, want [my-skill]", resolved.EnabledSkillNames)
	}
}

func TestResolveComposition_MissingSandboxComposition(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	m := &Manager{db: d, cfg: Config{}}
	resolved, err := m.ResolveComposition(context.Background(), "no-such-sandbox", "agent-ws-test", SandboxTypeOpenclaw.String())
	if err != nil {
		t.Fatalf("ResolveComposition: %v", err)
	}
	if len(resolved.EphemeralConfigMaps) != 0 || resolved.SoulBody != "" {
		t.Fatalf("expected empty resolved composition, got %+v", resolved)
	}
}
