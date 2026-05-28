package db

import (
	"context"
	"errors"
	"os"
	"testing"
)

func testDBURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return url
}

func TestAnonymizeUser_scrubsPIIAndRevokesAccess(t *testing.T) {
	database, err := Open(testDBURL(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()

	userID := "usr-anon-target"
	email := "anon-target@example.com"
	_, err = database.ExecContext(ctx, `
		INSERT INTO users (id, email, name, picture, role)
		VALUES ($1, $2, 'Visible Name', 'https://example.com/pic.png', 'user')`,
		userID, email)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	wsID := "ws-anon-member"
	_, err = database.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, slug) VALUES ($1, 'Anon WS', 'anon-ws')`,
		wsID)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, wsID)
	})

	_, err = database.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')`, wsID, userID)
	if err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	_, err = database.ExecContext(ctx, `
		INSERT INTO user_credentials (user_id, password_hash)
		VALUES ($1, 'fake-hash')`, userID)
	if err != nil {
		t.Fatalf("insert credential: %v", err)
	}

	_, err = database.ExecContext(ctx, `
		INSERT INTO auth_tokens (token, user_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '1 day')`, "tok-anon-target", userID)
	if err != nil {
		t.Fatalf("insert auth token: %v", err)
	}

	if err := database.AnonymizeUser(ctx, userID); err != nil {
		t.Fatalf("AnonymizeUser: %v", err)
	}

	var gotEmail, name, picture, hash *string
	var anonymizedAt *string
	err = database.QueryRowContext(ctx, `
		SELECT email, name, picture, original_email_hash, anonymized_at::text
		FROM users WHERE id = $1`, userID).
		Scan(&gotEmail, &name, &picture, &hash, &anonymizedAt)
	if err != nil {
		t.Fatalf("select user: %v", err)
	}
	if anonymizedAt == nil || *anonymizedAt == "" {
		t.Fatal("expected anonymized_at set")
	}
	if gotEmail == nil || *gotEmail != anonymizedPlaceholderEmail(userID) {
		t.Fatalf("email = %v, want placeholder %q", gotEmail, anonymizedPlaceholderEmail(userID))
	}
	if name != nil || picture != nil {
		t.Fatalf("expected name and picture NULL, got name=%v picture=%v", name, picture)
	}
	if hash == nil || *hash != hashEmailForAnonymization(email) {
		t.Fatalf("unexpected original_email_hash")
	}

	var credCount int
	_ = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials WHERE user_id = $1`, userID).Scan(&credCount)
	if credCount != 0 {
		t.Fatalf("expected no credentials, got %d", credCount)
	}

	var memCount int
	_ = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_members WHERE user_id = $1`, userID).Scan(&memCount)
	if memCount != 0 {
		t.Fatalf("expected no memberships, got %d", memCount)
	}

	var tokCount int
	_ = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_tokens WHERE user_id = $1`, userID).Scan(&tokCount)
	if tokCount != 0 {
		t.Fatalf("expected no auth tokens, got %d", tokCount)
	}
}

func TestAnonymizeUser_idempotentSecondCall(t *testing.T) {
	database, err := Open(testDBURL(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()

	userID := "usr-anon-idem"
	_, err = database.ExecContext(ctx, `
		INSERT INTO users (id, email, role) VALUES ($1, 'idem@example.com', 'user')`,
		userID)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if err := database.AnonymizeUser(ctx, userID); err != nil {
		t.Fatalf("first anonymize: %v", err)
	}
	err = database.AnonymizeUser(ctx, userID)
	if !errors.Is(err, ErrUserAlreadyAnonymized) {
		t.Fatalf("second anonymize: want ErrUserAlreadyAnonymized, got %v", err)
	}
}

func TestWorkspacesWhereUserIsLastOwner(t *testing.T) {
	database, err := Open(testDBURL(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()

	ownerID := "usr-last-owner"
	otherID := "usr-other-owner"
	wsSolo := "ws-solo-owner"
	wsShared := "ws-shared-owner"

	for _, u := range []struct{ id, email string }{
		{ownerID, "solo@example.com"},
		{otherID, "other@example.com"},
	} {
		_, err := database.ExecContext(ctx, `INSERT INTO users (id, email, role) VALUES ($1, $2, 'user')`, u.id, u.email)
		if err != nil {
			t.Fatalf("insert user %s: %v", u.id, err)
		}
		t.Cleanup(func() {
			_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, u.id)
		})
	}

	_, err = database.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, slug) VALUES ($1, 'Solo', 'solo'), ($2, 'Shared', 'shared')`,
		wsSolo, wsShared)
	if err != nil {
		t.Fatalf("insert workspaces: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(ctx, `DELETE FROM workspaces WHERE id IN ($1, $2)`, wsSolo, wsShared)
	})

	_, err = database.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner'), ($3, $2, 'owner'), ($3, $4, 'owner')`,
		wsSolo, ownerID, wsShared, otherID)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	list, err := database.WorkspacesWhereUserIsLastOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("WorkspacesWhereUserIsLastOwner: %v", err)
	}
	if len(list) != 1 || list[0].ID != wsSolo {
		t.Fatalf("expected solo workspace only, got %+v", list)
	}

	otherList, err := database.WorkspacesWhereUserIsLastOwner(ctx, otherID)
	if err != nil {
		t.Fatalf("other owner check: %v", err)
	}
	if len(otherList) != 0 {
		t.Fatalf("expected empty for shared owner, got %+v", otherList)
	}
}
