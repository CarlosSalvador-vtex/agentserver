package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempHome points os.UserHomeDir at a fresh tempdir for the duration of the
// test. executorSessionsDir derives from $HOME, so this isolates session files.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeSession(t *testing.T, dir string, sess ExecutorSession) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(&sess, "", "  ")
	path := filepath.Join(dir, sess.ExecutorID+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAnyExecutorSessionForServer_PicksMostRecent(t *testing.T) {
	withTempHome(t)
	dir, _ := executorSessionsDir()

	older := time.Now().Add(-2 * time.Hour).UTC()
	newer := time.Now().Add(-1 * time.Hour).UTC()
	writeSession(t, dir, ExecutorSession{
		ExecutorID: "exe_old", WorkspaceID: "ws_old",
		ServerURL: "https://srv.example", CreatedAt: older,
	})
	writeSession(t, dir, ExecutorSession{
		ExecutorID: "exe_new", WorkspaceID: "ws_new",
		ServerURL: "https://srv.example", CreatedAt: newer,
	})
	// Different server should be ignored.
	writeSession(t, dir, ExecutorSession{
		ExecutorID: "exe_other", WorkspaceID: "ws_other",
		ServerURL: "https://other.example", CreatedAt: time.Now().UTC(),
	})

	got, err := LoadAnyExecutorSessionForServer("https://srv.example")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a session, got nil")
	}
	if got.ExecutorID != "exe_new" || got.WorkspaceID != "ws_new" {
		t.Errorf("expected exe_new/ws_new, got %+v", got)
	}
}

func TestLoadAnyExecutorSessionForServer_NoMatchReturnsNil(t *testing.T) {
	withTempHome(t)
	dir, _ := executorSessionsDir()
	writeSession(t, dir, ExecutorSession{
		ExecutorID: "exe_a", WorkspaceID: "ws_a",
		ServerURL: "https://other.example", CreatedAt: time.Now().UTC(),
	})

	got, err := LoadAnyExecutorSessionForServer("https://nomatch.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestLoadAnyExecutorSessionForServer_NoSessionsDirReturnsNil(t *testing.T) {
	withTempHome(t)
	got, err := LoadAnyExecutorSessionForServer("https://srv.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
