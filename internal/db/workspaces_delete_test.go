package db

import (
	"database/sql"
	"strings"
	"testing"
)

func TestSoftDeleteWorkspace(t *testing.T) {
	d := newTestDB(t)
	wid := "ws_soft_" + strings.ReplaceAll(strings.ReplaceAll(t.Name(), "/", "_"), " ", "_")
	slug := wid + "-slug"
	t.Cleanup(func() {
		d.Exec(`DELETE FROM workspaces WHERE id = $1`, wid)
	})
	if _, err := d.Exec(
		`INSERT INTO workspaces (id, name, slug, created_at, updated_at) VALUES ($1, $2, $3, NOW(), NOW())`,
		wid, "Soft Del", slug,
	); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	if err := d.SoftDeleteWorkspace(wid); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, err := d.GetWorkspace(wid)
	if err != nil {
		t.Fatalf("get after soft delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected workspace hidden after soft delete")
	}
	if err := d.SoftDeleteWorkspace(wid); err != sql.ErrNoRows {
		t.Fatalf("second soft delete: want ErrNoRows, got %v", err)
	}
	taken, err := d.SlugExists(slug)
	if err != nil {
		t.Fatalf("slug exists: %v", err)
	}
	if !taken {
		t.Fatal("slug should remain occupied after soft delete")
	}
	bySlug, err := d.GetWorkspaceBySlug(slug)
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if bySlug != nil {
		t.Fatal("GetWorkspaceBySlug should not return soft-deleted workspace")
	}
}
