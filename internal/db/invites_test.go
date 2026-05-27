package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"
)

func newInviteTestDB(t *testing.T) *DB {
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
		d.Exec(`DELETE FROM workspace_invites`)
		d.Exec(`DELETE FROM workspace_members`)
		d.Exec(`DELETE FROM users WHERE email LIKE 'invite-test-%'`)
		d.Exec(`DELETE FROM workspaces WHERE name LIKE 'invite-test-%'`)
		d.Close()
	})
	return d
}

func hashHex(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

func seedInviteFixtures(t *testing.T, d *DB) (wsID, userID string) {
	t.Helper()
	userID = "usr-invite-test-1"
	if err := d.CreateUser(userID, "invite-test-creator@example.com", "$2a$10$dummy"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := d.CreateWorkspace("ws-invite-test", "invite-test-workspace"); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsID = "ws-invite-test"
	return
}

func TestInvite_CreateAndGetByID(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	plainToken := "test-token-abc-xyz"
	expires := time.Now().Add(7 * 24 * time.Hour).UTC()

	inv, err := d.CreateInvite(
		"inv-1", wsID, "alice@example.com", "developer",
		hashHex(plainToken), creatorID, expires,
	)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if inv.ID != "inv-1" || inv.Email != "alice@example.com" || inv.Role != "developer" {
		t.Fatalf("unexpected invite: %+v", inv)
	}
	if inv.AcceptedAt.Valid {
		t.Fatalf("new invite should not be accepted yet")
	}

	got, err := d.GetInviteByID("inv-1")
	if err != nil {
		t.Fatalf("GetInviteByID: %v", err)
	}
	if got == nil || got.TokenHash != hashHex(plainToken) {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestInvite_GetPendingByTokenHash(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	plainToken := "pending-token"
	_, err := d.CreateInvite(
		"inv-pend", wsID, "bob@example.com", "viewer",
		hashHex(plainToken), creatorID,
		time.Now().Add(1*time.Hour).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := d.GetPendingInviteByTokenHash(hashHex(plainToken))
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != "inv-pend" {
			t.Fatalf("expected to find pending invite, got %+v", got)
		}
	})

	t.Run("wrong hash returns nil", func(t *testing.T) {
		got, err := d.GetPendingInviteByTokenHash(hashHex("other"))
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("expected nil for missing hash, got %+v", got)
		}
	})
}

func TestInvite_PendingExcludesExpired(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	plainToken := "expired-token"
	_, err := d.CreateInvite(
		"inv-exp", wsID, "expired@example.com", "developer",
		hashHex(plainToken), creatorID,
		time.Now().Add(-1*time.Hour).UTC(), // expired 1h ago
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := d.GetPendingInviteByTokenHash(hashHex(plainToken))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expired invite should not be returned, got %+v", got)
	}
}

func TestInvite_MarkAccepted(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	_, err := d.CreateInvite(
		"inv-acc", wsID, "accept@example.com", "developer",
		hashHex("t1"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.MarkInviteAccepted("inv-acc"); err != nil {
		t.Fatalf("first accept: %v", err)
	}

	if err := d.MarkInviteAccepted("inv-acc"); !errors.Is(err, ErrInviteAlreadyAccepted) {
		t.Fatalf("expected ErrInviteAlreadyAccepted on second accept, got %v", err)
	}

	got, _ := d.GetInviteByID("inv-acc")
	if !got.AcceptedAt.Valid {
		t.Fatalf("expected accepted_at to be set")
	}

	// Accepted invite should not be returned by GetPending
	pending, _ := d.GetPendingInviteByTokenHash(hashHex("t1"))
	if pending != nil {
		t.Fatalf("accepted invite should not be returned as pending, got %+v", pending)
	}
}

func TestInvite_DuplicatePending(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	_, err := d.CreateInvite(
		"inv-dup-1", wsID, "dup@example.com", "developer",
		hashHex("first"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = d.CreateInvite(
		"inv-dup-2", wsID, "dup@example.com", "developer",
		hashHex("second"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	)
	if !errors.Is(err, ErrInviteAlreadyPending) {
		t.Fatalf("expected ErrInviteAlreadyPending, got %v", err)
	}
}

func TestInvite_DuplicateAllowedAfterAccept(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	if _, err := d.CreateInvite(
		"inv-flow-1", wsID, "flow@example.com", "developer",
		hashHex("t"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := d.MarkInviteAccepted("inv-flow-1"); err != nil {
		t.Fatal(err)
	}

	// After acceptance, a new invite for the same email is allowed
	// (e.g., re-invite to a different role later).
	if _, err := d.CreateInvite(
		"inv-flow-2", wsID, "flow@example.com", "owner",
		hashHex("t2"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	); err != nil {
		t.Fatalf("expected re-invite to be allowed, got %v", err)
	}
}

func TestInvite_DeleteRevokesPending(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	_, err := d.CreateInvite(
		"inv-del", wsID, "revoke@example.com", "developer",
		hashHex("t"), creatorID, time.Now().Add(1*time.Hour).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteInvite("inv-del"); err != nil {
		t.Fatalf("DeleteInvite: %v", err)
	}
	got, _ := d.GetInviteByID("inv-del")
	if got != nil {
		t.Fatalf("expected invite to be deleted, got %+v", got)
	}
}

func TestInvite_ListByWorkspace(t *testing.T) {
	d := newInviteTestDB(t)
	wsID, creatorID := seedInviteFixtures(t, d)

	for i, email := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		_, err := d.CreateInvite(
			"inv-list-"+string(rune('a'+i)), wsID, email, "developer",
			hashHex("t-"+email), creatorID,
			time.Now().Add(1*time.Hour).UTC(),
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := d.ListInvitesByWorkspace(wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 invites, got %d", len(got))
	}
}
