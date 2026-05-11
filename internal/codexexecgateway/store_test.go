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
		store.Exec(`DELETE FROM workspace_executors`)
		store.Exec(`DELETE FROM executors`)
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
