package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func newCodexTestDB(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := Open(url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM codex_remote_tokens`)
		d.Close()
	})
	return d
}

func TestCodexTokens_CreateAndGet(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	exp := time.Now().Add(90 * 24 * time.Hour).UTC()
	err := d.CreateCodexToken(ctx, CodexToken{
		ID:          "a3k9f7zq",
		UserID:      "usr_a",
		WorkspaceID: "ws_x",
		Name:        "my mac",
		TokenHash:   "$2a$12$abc",
		ExpiresAt:   exp,
	})
	if err != nil {
		t.Fatalf("CreateCodexToken: %v", err)
	}
	got, err := d.GetCodexToken(ctx, "a3k9f7zq")
	if err != nil || got == nil {
		t.Fatalf("GetCodexToken: %v %+v", err, got)
	}
	if got.UserID != "usr_a" || got.WorkspaceID != "ws_x" || got.Name != "my mac" {
		t.Errorf("got = %+v", got)
	}
}

func TestCodexTokens_GetMissing(t *testing.T) {
	d := newCodexTestDB(t)
	got, err := d.GetCodexToken(context.Background(), "missing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for missing, got %+v", got)
	}
}

func TestCodexTokens_ListAndRevoke(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	for i, id := range []string{"id1", "id2", "id3"} {
		_ = d.CreateCodexToken(ctx, CodexToken{
			ID: id, UserID: "usr_a", WorkspaceID: "ws_x",
			Name: id, TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
		})
		_ = i
	}
	rows, _ := d.ListCodexTokensForWorkspace(ctx, "ws_x", false)
	if len(rows) != 3 {
		t.Fatalf("want 3, got %d", len(rows))
	}
	if err := d.RevokeCodexToken(ctx, "id2"); err != nil {
		t.Fatalf("RevokeCodexToken: %v", err)
	}
	rows, _ = d.ListCodexTokensForWorkspace(ctx, "ws_x", false)
	if len(rows) != 2 {
		t.Fatalf("after revoke want 2, got %d", len(rows))
	}
	rows, _ = d.ListCodexTokensForWorkspace(ctx, "ws_x", true)
	if len(rows) != 3 {
		t.Fatalf("with include_revoked want 3, got %d", len(rows))
	}
}

func TestCodexTokens_Revoke_Idempotent(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	_ = d.CreateCodexToken(ctx, CodexToken{
		ID: "id1", UserID: "u", WorkspaceID: "w", Name: "n",
		TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err := d.RevokeCodexToken(ctx, "id1"); err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeCodexToken(ctx, "id1"); err != nil {
		t.Fatal("second revoke must be idempotent")
	}
	if err := d.RevokeCodexToken(ctx, "missing"); err != nil {
		t.Fatal("revoke missing must be idempotent")
	}
}

func TestCodexTokens_Touch(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	_ = d.CreateCodexToken(ctx, CodexToken{
		ID: "id1", UserID: "u", WorkspaceID: "w", Name: "n",
		TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err := d.TouchCodexToken(ctx, "id1"); err != nil {
		t.Fatalf("TouchCodexToken: %v", err)
	}
	got, _ := d.GetCodexToken(ctx, "id1")
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt should be set after touch")
	}
}
