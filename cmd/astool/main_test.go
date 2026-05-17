package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFlags_EnvFallbacks(t *testing.T) {
	t.Setenv("ASTOOL_GATEWAY_URL", "ws://example/codex-app/ws")
	t.Setenv("ASTOOL_TOKEN", "tok-from-env")
	t.Setenv("CODEX_THREAD_ID", "thread-xyz")

	f, positional, err := parseFlags([]string{"env_mcp", "list_environments"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.gateway != "ws://example/codex-app/ws" {
		t.Errorf("gateway = %q", f.gateway)
	}
	if f.token != "tok-from-env" {
		t.Errorf("token = %q", f.token)
	}
	if f.thread != "thread-xyz" {
		t.Errorf("thread = %q", f.thread)
	}
	if f.timeout != 30*time.Second {
		t.Errorf("timeout = %v", f.timeout)
	}
	if len(positional) != 2 || positional[0] != "env_mcp" || positional[1] != "list_environments" {
		t.Errorf("positional = %v", positional)
	}
}

func TestParseFlags_CLIOverridesEnv(t *testing.T) {
	t.Setenv("ASTOOL_TOKEN", "from-env")
	f, _, err := parseFlags([]string{"--token", "from-flag", "s", "t"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.token != "from-flag" {
		t.Errorf("token = %q (env should not win over flag)", f.token)
	}
}

func TestRun_MissingThread(t *testing.T) {
	t.Setenv("ASTOOL_TOKEN", "tok")
	os.Unsetenv("CODEX_THREAD_ID")
	err := run([]string{"env_mcp", "list_environments"})
	if err == nil || !strings.Contains(err.Error(), "thread id") {
		t.Fatalf("expected missing-thread error, got %v", err)
	}
	if exitCodeFor(err) != 1 {
		t.Errorf("exit code = %d, want 1", exitCodeFor(err))
	}
}

func TestRun_MissingToken(t *testing.T) {
	os.Unsetenv("ASTOOL_TOKEN")
	t.Setenv("CODEX_THREAD_ID", "tid")
	err := run([]string{"env_mcp", "list_environments"})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected missing-token error, got %v", err)
	}
}

func TestRun_InvalidJSONArgs(t *testing.T) {
	t.Setenv("ASTOOL_TOKEN", "tok")
	t.Setenv("CODEX_THREAD_ID", "tid")
	err := run([]string{"env_mcp", "switch_environment", "{not json"})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid-JSON error, got %v", err)
	}
}

func TestRenderResponse_TextContent(t *testing.T) {
	resp := &mcpToolCallResult{
		Content: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"hello world"}`),
			json.RawMessage(`{"type":"text","text":"second line"}`),
		},
	}
	out := captureStdout(t, func() {
		if err := renderResponse(resp, false); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	if !strings.Contains(out, "hello world") || !strings.Contains(out, "second line") {
		t.Errorf("output missing text: %q", out)
	}
}

func TestRenderResponse_IsErrorReturnsToolExit(t *testing.T) {
	yes := true
	resp := &mcpToolCallResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"boom"}`)},
		IsError: &yes,
	}
	err := renderResponse(resp, false)
	var te *toolErrorExit
	if !errors.As(err, &te) {
		t.Fatalf("expected toolErrorExit, got %T %v", err, err)
	}
	if exitCodeFor(err) != 2 {
		t.Errorf("exit code = %d, want 2", exitCodeFor(err))
	}
}

func TestRenderResponse_JSONMode(t *testing.T) {
	resp := &mcpToolCallResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"x"}`)},
	}
	out := captureStdout(t, func() {
		if err := renderResponse(resp, true); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	if !strings.Contains(out, `"content"`) {
		t.Errorf("json mode output should contain raw structure: %q", out)
	}
}

func TestRenderResponse_StructuredContent(t *testing.T) {
	resp := &mcpToolCallResult{
		Content:           []json.RawMessage{json.RawMessage(`{"type":"text","text":"summary"}`)},
		StructuredContent: json.RawMessage(`{"environments":[{"id":"a"}]}`),
	}
	out := captureStdout(t, func() {
		if err := renderResponse(resp, false); err != nil {
			t.Fatalf("render: %v", err)
		}
	})
	if !strings.Contains(out, "summary") {
		t.Errorf("missing text section: %q", out)
	}
	if !strings.Contains(out, `"environments"`) {
		t.Errorf("missing structured section: %q", out)
	}
	if !strings.Contains(out, "---") {
		t.Errorf("missing separator: %q", out)
	}
}

// captureStdout replaces os.Stdout for the duration of fn and returns
// the captured bytes. Keeps tests free of global cleanup boilerplate.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	<-done
	return buf.String()
}
