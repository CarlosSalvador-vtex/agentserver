//go:build integration

package envmcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRun_AgainstRealCodexExecServer is opt-in: build with `-tags
// integration`. It requires `codex` on PATH (any version that supports
// `codex exec-server --listen ws://127.0.0.1:0`) and writes/reads
// from /tmp.
func TestRun_AgainstRealCodexExecServer(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not on PATH; skip integration test")
	}

	cmd := exec.Command("codex", "exec-server", "--listen", "ws://127.0.0.1:0")
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start codex: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// codex exec-server prints `ws://IP:PORT` on its first stdout line.
	scanner := bufio.NewScanner(stdoutPipe)
	if !scanner.Scan() {
		t.Fatalf("codex exec-server did not print listen URL")
	}
	wsURL := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(wsURL, "ws://") {
		t.Fatalf("unexpected first stdout line %q", wsURL)
	}

	t.Setenv("CXG_INT_TOKEN", "ignored-for-local-server")
	in := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shell","arguments":{"command":["sh","-c","printf integration-ok"]}}}`,
		"",
	}, "\n"))
	out := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := Run(ctx, RunArgs{
		ExeID:     "exe_int",
		BridgeURL: wsURL,
		TokenEnv:  "CXG_INT_TOKEN",
		ExeDesc:   "Integration",
	}, in, out, &bytes.Buffer{}, logger); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "integration-ok") {
		t.Fatalf("expected integration-ok in stdout, got: %s", out.String())
	}
}
