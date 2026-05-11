package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

const envMcpHelp = `Usage: codex-app-gateway env-mcp [flags]

Run the binary as a stdio MCP child for one executor (per spawned codex turn).

Required flags:
  --exe-id     <id>             executor id
  --bridge-url <ws-url>         ws URL for /bridge/{exe_id}
  --token-env  <env-var-name>   env var holding the cap token (token never appears in argv)

Optional flags:
  --exe-desc   <text>           executor description shown to the LLM (default: --exe-id)
  --turn-id    <id>             turn id (logged to stderr only)
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
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(os.Stderr, envMcpHelp)
		os.Exit(0)
	}
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
	fs.SetOutput(io.Discard)
	exeID := fs.String("exe-id", "", "executor id (required)")
	bridgeURL := fs.String("bridge-url", "", "ws URL for /bridge/{exe_id} (required)")
	tokenEnv := fs.String("token-env", "", "env var name holding the cap token (required)")
	exeDesc := fs.String("exe-desc", "", "executor description shown to the LLM (defaults to --exe-id)")
	turnID := fs.String("turn-id", "", "turn id (logged to stderr only)")
	if err := fs.Parse(rawArgs); err != nil {
		return envmcp.RunArgs{}, err
	}
	if fs.NArg() > 0 {
		return envmcp.RunArgs{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
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
