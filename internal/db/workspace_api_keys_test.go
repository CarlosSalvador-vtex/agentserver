package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// setupAPIKeyFixtures inserts a workspace and user row needed by FK constraints,
// returning (workspaceID, userID). Cleanup is registered on t.
func setupAPIKeyFixtures(t *testing.T, d *DB) (workspaceID, userID string) {
	t.Helper()
	suffix := fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name())))[:8]
	workspaceID = "ws_wak_" + suffix
	userID = "u_wak_" + suffix

	_, err := d.Exec(
		`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		workspaceID, "test workspace "+suffix,
	)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	_, err = d.Exec(
		`INSERT INTO users (id, username, email) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, "wak_user_"+suffix, "wak_user_"+suffix+"@example.com",
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	t.Cleanup(func() {
		// cascade deletes workspace_api_keys too
		d.Exec(`DELETE FROM workspace_api_keys WHERE workspace_id = $1`, workspaceID)
		d.Exec(`DELETE FROM workspaces WHERE id = $1`, workspaceID)
		d.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	return
}

// makeHash is a helper that produces the sha256 hex of a secret string,
// matching what ValidateWorkspaceAPIKeySecret expects.
func makeHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func TestWorkspaceAPIKey_CreateAndGet(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	key := WorkspaceAPIKey{
		ID:          "wak_testcreate",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "test-key",
		Prefix:      "wak_testcreate",
		SecretHash:  makeHash("wak_testcreate_secretvalue"),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	rows, err := d.ListWorkspaceAPIKeys(ctx, wsID)
	if err != nil {
		t.Fatalf("ListWorkspaceAPIKeys: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.ID != key.ID {
		t.Errorf("ID: got %q, want %q", got.ID, key.ID)
	}
	if got.Name != key.Name {
		t.Errorf("Name: got %q, want %q", got.Name, key.Name)
	}
	if got.Prefix != key.Prefix {
		t.Errorf("Prefix: got %q, want %q", got.Prefix, key.Prefix)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "turns:submit" {
		t.Errorf("Scopes: got %v, want [turns:submit]", got.Scopes)
	}
	// List must NOT return secret_hash
	if got.SecretHash != "" {
		t.Errorf("SecretHash should be empty from List, got %q", got.SecretHash)
	}
}

func TestWorkspaceAPIKey_ValidateHashMatch(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	secret := "wak_hashtest1_mysecretvalue0000000000000000"
	key := WorkspaceAPIKey{
		ID:          "wak_hashtest1",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "hash-test",
		Prefix:      "wak_hashtest1",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	got, err := d.ValidateWorkspaceAPIKeySecret(ctx, "wak_hashtest1", secret)
	if err != nil {
		t.Fatalf("ValidateWorkspaceAPIKeySecret: %v", err)
	}
	if got.WorkspaceID != wsID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, wsID)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "turns:submit" {
		t.Errorf("Scopes: got %v, want [turns:submit]", got.Scopes)
	}
	if got.SecretHash != "" {
		t.Errorf("SecretHash should be cleared on return, got %q", got.SecretHash)
	}
}

func TestWorkspaceAPIKey_ValidateHashMismatch(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	secret := "wak_hashmism1_correctsecretvalue000000000"
	key := WorkspaceAPIKey{
		ID:          "wak_hashmism1",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "mismatch-test",
		Prefix:      "wak_hashmism1",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	_, err := d.ValidateWorkspaceAPIKeySecret(ctx, "wak_hashmism1", "wak_hashmism1_wrongsecret")
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows on mismatch, got %v", err)
	}
}

func TestWorkspaceAPIKey_ValidateRevoked(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	secret := "wak_revoktest_secretvalue0000000000000000"
	key := WorkspaceAPIKey{
		ID:          "wak_revoktest",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "revoke-test",
		Prefix:      "wak_revoktest",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}
	if err := d.RevokeWorkspaceAPIKey(ctx, wsID, "wak_revoktest"); err != nil {
		t.Fatalf("RevokeWorkspaceAPIKey: %v", err)
	}

	_, err := d.ValidateWorkspaceAPIKeySecret(ctx, "wak_revoktest", secret)
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows for revoked key, got %v", err)
	}
}

func TestWorkspaceAPIKeys_ListExcludesSecretHash(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	key := WorkspaceAPIKey{
		ID:          "wak_listsecret",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "list-secret-test",
		Prefix:      "wak_listsecret",
		SecretHash:  makeHash("wak_listsecret_somevalue"),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	rows, err := d.ListWorkspaceAPIKeys(ctx, wsID)
	if err != nil {
		t.Fatalf("ListWorkspaceAPIKeys: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	for _, r := range rows {
		if r.SecretHash != "" {
			t.Errorf("List returned SecretHash=%q for key %q — must be empty", r.SecretHash, r.ID)
		}
		if r.Scopes == nil {
			t.Errorf("Scopes should be non-nil (empty slice acceptable), got nil for key %q", r.ID)
		}
	}
}

func TestWorkspaceAPIKey_TouchLastUsed(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	key := WorkspaceAPIKey{
		ID:          "wak_touchtest1",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "touch-test",
		Prefix:      "wak_touchtest1",
		SecretHash:  makeHash("wak_touchtest1_val"),
		Scopes:      []string{"turns:submit"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	before := time.Now().Add(-time.Second)
	if err := d.TouchWorkspaceAPIKeyLastUsed(ctx, "wak_touchtest1"); err != nil {
		t.Fatalf("TouchWorkspaceAPIKeyLastUsed: %v", err)
	}

	rows, err := d.ListWorkspaceAPIKeys(ctx, wsID)
	if err != nil {
		t.Fatalf("ListWorkspaceAPIKeys: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected rows after touch")
	}
	var touched *WorkspaceAPIKey
	for i := range rows {
		if rows[i].ID == "wak_touchtest1" {
			touched = &rows[i]
			break
		}
	}
	if touched == nil {
		t.Fatal("key not found after touch")
	}
	if touched.LastUsedAt == nil {
		t.Fatal("LastUsedAt should be set after touch")
	}
	if touched.LastUsedAt.Before(before) {
		t.Errorf("LastUsedAt %v is before %v — not updated", touched.LastUsedAt, before)
	}
	if time.Since(*touched.LastUsedAt) > 5*time.Second {
		t.Errorf("LastUsedAt %v is too far in the past", touched.LastUsedAt)
	}
}

func TestWorkspaceAPIKey_MultipleScopes(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	secret := "wak_multiscope_secretvalue0000000000000"
	key := WorkspaceAPIKey{
		ID:          "wak_multiscope",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "multi-scope-test",
		Prefix:      "wak_multiscope",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"turns:submit", "threads:read"},
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	got, err := d.ValidateWorkspaceAPIKeySecret(ctx, "wak_multiscope", secret)
	if err != nil {
		t.Fatalf("ValidateWorkspaceAPIKeySecret: %v", err)
	}
	if len(got.Scopes) != 2 {
		t.Fatalf("want 2 scopes, got %d: %v", len(got.Scopes), got.Scopes)
	}
	scopeSet := map[string]bool{}
	for _, s := range got.Scopes {
		scopeSet[s] = true
	}
	if !scopeSet["turns:submit"] {
		t.Errorf("missing turns:submit in scopes: %v", got.Scopes)
	}
	if !scopeSet["threads:read"] {
		t.Errorf("missing threads:read in scopes: %v", got.Scopes)
	}
}
