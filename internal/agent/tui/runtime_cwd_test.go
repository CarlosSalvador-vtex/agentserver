// internal/agent/tui/runtime_cwd_test.go
package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/agent"
)

func TestRuntimeCwd_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agentserver", "executors")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "exe_a.json")
	body := `{"executor_id":"exe_a","name":"x","workspace_id":"ws","tunnel_token":"t","registry_token":"r","server_url":"u","created_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agent.SetRuntimeCwd("exe_a", "/tmp/foo"); err != nil {
		t.Fatal(err)
	}
	if got := agent.LoadRuntimeCwd("exe_a"); got != "/tmp/foo" {
		t.Errorf("LoadRuntimeCwd=%q want /tmp/foo", got)
	}
	sess, err := agent.LoadSessionByID("exe_a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.RegistryToken != "r" {
		t.Errorf("RegistryToken corrupted: %+v", sess)
	}
	if sess.RuntimeCwd != "/tmp/foo" {
		t.Errorf("RuntimeCwd not in struct: %+v", sess)
	}
}
