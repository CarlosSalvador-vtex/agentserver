package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentserver/agentserver/internal/codexappgateway/envmcp"
)

const usage = `codex-app-gateway — codex gateway binary

Subcommands:
  env-mcp     Run as a stdio MCP child for one executor (per spawned codex turn)
  serve       Run the gateway HTTP/WS server (not implemented in this plan)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "env-mcp":
		runEnvMcp(os.Args[2:])
	case "serve":
		fmt.Fprintln(os.Stderr, "codex-app-gateway: serve subcommand not implemented in this plan")
		os.Exit(2)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runEnvMcp(rawArgs []string) {
	args, err := parseEnvMcpArgs(rawArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-app-gateway env-mcp:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := envmcp.Run(ctx, args, os.Stdin, os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("env-mcp exited with error", "err", err)
		os.Exit(1)
	}
}

func parseEnvMcpArgs(rawArgs []string) (envmcp.RunArgs, error) {
	fs := flag.NewFlagSet("env-mcp", flag.ContinueOnError)
	exeID := fs.String("exe-id", "", "executor id (required)")
	bridgeURL := fs.String("bridge-url", "", "ws URL for /bridge/{exe_id} (required)")
	tokenEnv := fs.String("token-env", "", "env var name holding the cap token (required)")
	exeDesc := fs.String("exe-desc", "", "executor description shown to the LLM (defaults to --exe-id)")
	turnID := fs.String("turn-id", "", "turn id (logged to stderr only)")
	if err := fs.Parse(rawArgs); err != nil {
		return envmcp.RunArgs{}, err
	}
	if *exeID == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--exe-id is required")
	}
	if *bridgeURL == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--bridge-url is required")
	}
	if *tokenEnv == "" {
		return envmcp.RunArgs{}, fmt.Errorf("--token-env is required")
	}
	desc := *exeDesc
	if desc == "" {
		desc = *exeID
	}
	return envmcp.RunArgs{
		ExeID:     *exeID,
		BridgeURL: *bridgeURL,
		TokenEnv:  *tokenEnv,
		ExeDesc:   desc,
		TurnID:    *turnID,
	}, nil
}
