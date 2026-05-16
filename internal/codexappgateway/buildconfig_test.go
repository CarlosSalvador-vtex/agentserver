package codexappgateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway"
	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

type stubConnected struct {
	rows []execmodel.ConnectedExecutor
	err  error
	gotW string
}

func (s *stubConnected) Connected(_ context.Context, w string) ([]execmodel.ConnectedExecutor, error) {
	s.gotW = w
	return s.rows, s.err
}

// stubTokenFetcher returns empty token (caller falls back to static
// CodexAPIKey or none); good enough for tests that don't care about the
// env value, only about the config.toml content.
type stubTokenFetcher struct{}

func (stubTokenFetcher) FetchToken(_ context.Context, _ string) (string, error) {
	return "", nil
}

func newTestCfg() ServeConfig {
	return ServeConfig{
		ExecGatewayWSURL:     "ws://exec-gw:6060",
		CapTokenHMACSecret:   []byte("cap-secret"),
		CapTokenTTL:          time.Minute,
		ModelProvider:        "modelserver",
		Model:                "gpt-5.5",
		ModelProviderBaseURL: "http://llmproxy:8085/v1",
		ModelProviderEnvKey:  "CODEX_API_KEY",
		ModelProviderWireAPI: "responses",
	}
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestBuildConfig_PopulatesExecutorsAndMintsValidTokens(t *testing.T) {
	stub := &stubConnected{rows: []execmodel.ConnectedExecutor{
		{ExeID: "exe_alpha", Description: "Daisy MBP"},
		{ExeID: "exe_beta", Description: "EC2"},
	}}
	cfg := newTestCfg()
	build := makeBuildConfig(cfg, stub, stubTokenFetcher{}, "/usr/local/bin/codex-app-gateway", newDiscardLogger())

	got, err := build(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stub.gotW != "ws_a" {
		t.Errorf("client called with %q, want ws_a", stub.gotW)
	}
	if got.Config.ModelProvider != "modelserver" || got.Config.Model != "gpt-5.5" {
		t.Errorf("model: %+v", got)
	}
	if len(got.Config.Executors) != 2 {
		t.Fatalf("executors: %+v", got.Config.Executors)
	}
	if got.Config.Executors[0].BridgeURL != "ws://exec-gw:6060/bridge/exe_alpha" {
		t.Errorf("bridge url[0] = %s", got.Config.Executors[0].BridgeURL)
	}
	if got.Config.Executors[0].TokenEnv != "CXG_BRIDGE_TOKEN_EXE_ALPHA" {
		t.Errorf("token env[0] = %s", got.Config.Executors[0].TokenEnv)
	}
	if got.Config.Executors[0].CodexBin != "/usr/local/bin/codex-app-gateway" {
		t.Errorf("codex bin[0] = %s", got.Config.Executors[0].CodexBin)
	}
	// All entries share one turn_id (so revoke-turn cancels them as a unit).
	if got.Config.Executors[0].TurnID == "" || got.Config.Executors[0].TurnID != got.Config.Executors[1].TurnID {
		t.Errorf("turn ids should match: %q %q", got.Config.Executors[0].TurnID, got.Config.Executors[1].TurnID)
	}
	// Per the 2026-05-16 redesign, all executors in a workspace share
	// one workspace-scoped token. Each must verify with the same
	// workspace_id and turn_id; /bridge enforces exe_id ownership
	// separately via workspace_executors lookup.
	var first codexexecgateway.CapPayload
	for i, e := range got.Config.Executors {
		p, err := codexexecgateway.VerifyCapabilityToken(e.TokenVal, cfg.CapTokenHMACSecret)
		if err != nil {
			t.Fatalf("verify[%d]: %v", i, err)
		}
		if p.WorkspaceID != "ws_a" {
			t.Errorf("token[%d].workspace_id = %q", i, p.WorkspaceID)
		}
		if i == 0 {
			first = p
		} else if p.TurnID != first.TurnID {
			t.Errorf("token[%d].turn_id = %q, want %q (all-share)", i, p.TurnID, first.TurnID)
		}
	}
	// Default trusted path applied when none configured.
	if len(got.Config.ProjectTrustedPaths) != 1 || got.Config.ProjectTrustedPaths[0] != "/tmp" {
		t.Errorf("trusted paths default: %v", got.Config.ProjectTrustedPaths)
	}
}

func TestBuildConfig_FailSoftWhenExecGatewayDown(t *testing.T) {
	stub := &stubConnected{err: errors.New("connection refused")}
	cfg := newTestCfg()
	build := makeBuildConfig(cfg, stub, stubTokenFetcher{}, "/codex-app-gateway", newDiscardLogger())

	got, err := build(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("build should fail-soft, got %v", err)
	}
	if len(got.Config.Executors) != 0 {
		t.Errorf("expected empty executors on degraded fetch, got %+v", got.Config.Executors)
	}
	if got.Config.Model == "" {
		t.Error("model should still be populated for chat-only mode")
	}
}

func TestBuildConfig_NoExecutorsStillProducesValidConfig(t *testing.T) {
	stub := &stubConnected{rows: nil}
	cfg := newTestCfg()
	build := makeBuildConfig(cfg, stub, stubTokenFetcher{}, "/codex-app-gateway", newDiscardLogger())
	got, err := build(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(got.Config.Executors) != 0 {
		t.Errorf("got executors: %+v", got.Config.Executors)
	}
}

func TestBuildConfig_RespectsConfiguredTrustedPaths(t *testing.T) {
	stub := &stubConnected{}
	cfg := newTestCfg()
	cfg.ProjectTrustedPaths = []string{"/workspace", "/data"}
	build := makeBuildConfig(cfg, stub, stubTokenFetcher{}, "/codex-app-gateway", newDiscardLogger())
	got, err := build(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Join(got.Config.ProjectTrustedPaths, ",") != "/workspace,/data" {
		t.Errorf("trusted paths = %v", got.Config.ProjectTrustedPaths)
	}
}

func TestBuildConfig_ExeIDWithDashesNormalisesEnvVar(t *testing.T) {
	stub := &stubConnected{rows: []execmodel.ConnectedExecutor{
		{ExeID: "exe-dashy-id"},
	}}
	cfg := newTestCfg()
	build := makeBuildConfig(cfg, stub, stubTokenFetcher{}, "/codex-app-gateway", newDiscardLogger())
	got, err := build(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got.Config.Executors[0].TokenEnv != "CXG_BRIDGE_TOKEN_EXE_DASHY_ID" {
		t.Errorf("token env = %s", got.Config.Executors[0].TokenEnv)
	}
}
