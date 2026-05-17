package codexexecgateway

import (
	"context"
	"os"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, err := NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		store.truncateForTest()
		store.Close()
	})
	return store
}

func TestStore_CreateAndGetExecutor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	exe := Executor{
		ExeID:        "exe_test1",
		UserID:       "user_a",
		DisplayName:  "laptop",
		Description:  "Daisy MBP",
		DefaultCwd:   "/home/daisy",
		RegisteredAt: time.Now().UTC(),
	}
	if err := store.CreateExecutor(ctx, exe, "hashed_token"); err != nil {
		t.Fatalf("CreateExecutor: %v", err)
	}
	got, err := store.GetExecutor(ctx, "exe_test1")
	if err != nil {
		t.Fatalf("GetExecutor: %v", err)
	}
	if got == nil || got.ExeID != "exe_test1" || got.Description != "Daisy MBP" {
		t.Fatalf("GetExecutor: got %+v", got)
	}
}

func TestStore_BindAndListWorkspaceExecutors(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	exe := Executor{ExeID: "exe_a", UserID: "u1", Description: "alpha", RegisteredAt: time.Now().UTC()}
	if err := store.CreateExecutor(ctx, exe, "h"); err != nil {
		t.Fatalf("CreateExecutor: %v", err)
	}
	if err := store.BindWorkspaceExecutor(ctx, "ws_1", "exe_a", "alpha", "", true); err != nil {
		t.Fatalf("BindWorkspaceExecutor: %v", err)
	}
	rows, err := store.ListWorkspaceExecutors(ctx, "ws_1")
	if err != nil {
		t.Fatalf("ListWorkspaceExecutors: %v", err)
	}
	if len(rows) != 1 || rows[0].ExeID != "exe_a" || !rows[0].IsDefault {
		t.Fatalf("got %+v", rows)
	}
}

func TestStore_UnbindWorkspaceExecutor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	exe := Executor{ExeID: "exe_b", UserID: "u1", RegisteredAt: time.Now().UTC()}
	store.CreateExecutor(ctx, exe, "h")
	store.BindWorkspaceExecutor(ctx, "ws_1", "exe_b", "beta", "", false)
	if err := store.UnbindWorkspaceExecutor(ctx, "ws_1", "exe_b"); err != nil {
		t.Fatalf("UnbindWorkspaceExecutor: %v", err)
	}
	rows, _ := store.ListWorkspaceExecutors(ctx, "ws_1")
	if len(rows) != 0 {
		t.Fatalf("after unbind got %d rows", len(rows))
	}
}

func TestStore_ConnectedExecutorsForWorkspace(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, e := range []Executor{
		{ExeID: "exe_x", UserID: "u1", Description: "x desc", DefaultCwd: "/x", RegisteredAt: now},
		{ExeID: "exe_y", UserID: "u1", Description: "y desc", DefaultCwd: "/y", RegisteredAt: now},
	} {
		store.CreateExecutor(ctx, e, "h")
	}
	store.BindWorkspaceExecutor(ctx, "ws_1", "exe_x", "x", "", true)
	store.BindWorkspaceExecutor(ctx, "ws_1", "exe_y", "y", "", false)
	got, err := store.ConnectedExecutorsForWorkspace(ctx, "ws_1", []string{"exe_x"})
	if err != nil {
		t.Fatalf("ConnectedExecutorsForWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].ExeID != "exe_x" || !got[0].IsDefault {
		t.Fatalf("got %+v", got)
	}
}
