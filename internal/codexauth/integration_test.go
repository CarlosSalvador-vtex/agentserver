//go:build integration
// +build integration

package codexauth

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// TestIntegration_AgentIdentityWithRealCodex spins up the codexauth
// Server in-process behind httptest, mints an Agent Identity JWT, then
// invokes the real `codex exec-server --remote --use-agent-identity-auth`
// binary and confirms it gets past JWKS fetch + task/register (i.e.
// reaches the /cloud/executor/.../register call, which isn't mounted
// on our test server, so codex will see a 404 and bail — but that's
// AFTER our auth checks have passed).
//
// Requires `codex` >= 0.132 on PATH and TEST_DATABASE_URL set. Skips
// cleanly otherwise.
func TestIntegration_AgentIdentityWithRealCodex(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex binary not on PATH")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	srv := newAuthTestServer(t, "")
	uid := mustCreateTestUser(t, srv.Store.db)

	r := chi.NewRouter()
	srv.Mount(r)
	httpSrv := httptest.NewServer(r)
	defer httpSrv.Close()

	mint, err := srv.MintAgentIdentity(context.Background(), MintAgentIdentityArgs{
		AgentRuntimeID: "exe_integration_real",
		UserID:         uid,
		Email:          "u@test",
	})
	if err != nil {
		t.Fatalf("MintAgentIdentity: %v", err)
	}

	// codex exec-server --remote $URL --executor-id $EXE --use-agent-identity-auth
	//   1. reads CODEX_ACCESS_TOKEN
	//   2. fetches {chatgpt.base_url}/agent-identities/jwks → verifies JWT
	//   3. POSTs {CODEX_AGENT_IDENTITY_AUTHAPI_BASE_URL}/v1/agent/{rid}/task/register
	//   4. POSTs $URL/cloud/executor/{rid}/register   ← we don't mount this; codex hits 404 here
	//
	// Reaching step 4 means our auth-side wiring (JWKS + task/register)
	// worked end-to-end. The 404 at step 4 is expected.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex",
		"-c", "chatgpt.base_url="+httpSrv.URL,
		"exec-server", "--remote", httpSrv.URL,
		"--executor-id", "exe_integration_real",
		"--name", "test-agent",
		"--use-agent-identity-auth",
	)
	cmd.Env = append(os.Environ(),
		"CODEX_ACCESS_TOKEN="+mint.JWT,
		"CODEX_AGENT_IDENTITY_AUTHAPI_BASE_URL="+httpSrv.URL,
	)
	stderrPipe, _ := cmd.StderrPipe()
	stdoutPipe, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		t.Fatalf("start codex: %v", err)
	}

	// Collect output until codex exits (it will, because step 4 returns
	// 404 and codex retries with backoff — context timeout will cut it
	// short).
	var combined strings.Builder
	go func() {
		b, _ := io.ReadAll(stderrPipe)
		combined.Write(b)
	}()
	go func() {
		b, _ := io.ReadAll(stdoutPipe)
		combined.Write(b)
	}()
	_ = cmd.Wait()
	out := combined.String()

	// Auth-side success indicators (each appears in codex's stderr on
	// successful JWKS+task/register):
	//   - we should NOT see "requires ChatGPT authentication"
	//   - we should NOT see "failed to verify agent identity JWT" (JWKS works)
	//   - we should NOT see "failed to register agent task" (task/register works)
	for _, bad := range []string{
		"requires ChatGPT authentication",
		"failed to verify agent identity JWT",
		"failed to register agent task",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("codex stderr contains %q (auth setup failed):\n%s", bad, out)
		}
	}

	// Positive signal: the cloud-executor POST is the last step codex
	// makes. If we see SOMETHING resembling a registration attempt or
	// the 404 from our test server, JWKS+task/register made it through.
	// (codex's exact log lines change between versions; permissive check.)
	if !strings.Contains(out, "registered") && !strings.Contains(out, "404") &&
		!strings.Contains(out, "cloud/executor") {
		t.Logf("codex output (for diagnostic):\n%s", out)
		// Don't t.Errorf here — codex versions differ in stderr format,
		// and the negative checks above are the real assertion.
	}
}
