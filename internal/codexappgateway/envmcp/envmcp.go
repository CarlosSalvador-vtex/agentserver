// Package envmcp implements the `codex-app-gateway env-mcp` subcommand:
// a stateless MCP server that codex spawns as a child process. It
// exposes a fixed tool set (list_environments, shell, unified_exec,
// write_stdin, read_output, terminate, read_file, apply_patch) to
// codex; tool calls are multiplexed across the workspace's connected
// executors via a per-exe BridgeClient pool keyed by env_id.
package envmcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// RunArgs is the parsed CLI input for `codex-app-gateway env-mcp`.
// Per the 2026-05-16 fixed-tools redesign, env-mcp is workspace-scoped
// rather than per-executor; one child binary handles every executor in
// the workspace via env_id routing.
type RunArgs struct {
	WorkspaceID        string // --workspace-id
	ExecGatewayURL     string // --exec-gateway-url; pool appends /<exe_id>
	AppGatewayInternal string // --app-gateway-internal; list_environments calls /internal/connected here
	WorkspaceTokenEnv  string // --workspace-token-env (workspace-scoped cap token)
	LoopbackTokenEnv   string // --loopback-token-env (for /internal/connected)
}

// Run constructs the BridgePool, builds the tool registry, and serves
// the MCP loop on stdin/stdout until EOF or context cancellation.
//
// stdout is the MCP JSON-RPC stream; do not write to it from outside
// MCPServer.Serve. Diagnostic output flows through logger (gateway
// supervisor pipes our stderr into the pod's stderr with a
// `[codex-subproc]` prefix). The `stderr` parameter is reserved for
// future direct writes (e.g., panic dumps) and currently unused.
func Run(ctx context.Context, args RunArgs, stdin io.Reader, stdout, stderr io.Writer, logger *slog.Logger) error {
	_ = stderr
	wsToken := os.Getenv(args.WorkspaceTokenEnv)
	if wsToken == "" {
		return fmt.Errorf("env var %s is empty; cannot authenticate to bridge", args.WorkspaceTokenEnv)
	}
	lbToken := os.Getenv(args.LoopbackTokenEnv)
	if lbToken == "" {
		return fmt.Errorf("env var %s is empty; cannot authenticate to app-gateway loopback", args.LoopbackTokenEnv)
	}
	if args.WorkspaceID == "" || args.ExecGatewayURL == "" || args.AppGatewayInternal == "" {
		return fmt.Errorf("env-mcp: workspace-id, exec-gateway-url, app-gateway-internal all required")
	}

	logger.Info("env-mcp starting",
		"workspace_id", args.WorkspaceID,
		"exec_gateway_url", args.ExecGatewayURL,
		"app_gateway_internal", args.AppGatewayInternal,
	)

	pool := NewBridgePool(args.ExecGatewayURL, wsToken, logger)
	defer pool.Close()

	sessions := newSessionStore()
	connectedURL := strings.TrimRight(args.AppGatewayInternal, "/") + "/internal/connected"
	resolver := NewNameResolver(connectedURL, lbToken, logger)

	tools := []Tool{
		NewListEnvironmentsTool(resolver),
		NewShellTool(pool, resolver),
		NewUnifiedExecTool(pool, sessions, resolver),
		NewWriteStdinTool(pool, sessions),
		NewReadOutputTool(pool, sessions),
		NewTerminateTool(pool, sessions),
		NewReadFileTool(pool, resolver),
		NewApplyPatchTool(pool, resolver),
	}
	srv := NewMCPServer("agentserver", tools, logger)
	if err := srv.Serve(ctx, stdin, stdout); err != nil {
		return fmt.Errorf("mcp serve: %w", err)
	}
	logger.Info("env-mcp clean exit (stdin closed)")
	return nil
}
