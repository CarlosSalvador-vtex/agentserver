package envmcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

// RunArgs is the parsed CLI input for `codex-app-gateway env-mcp`.
type RunArgs struct {
	ExeID     string
	BridgeURL string
	TokenEnv  string
	ExeDesc   string
	TurnID    string
}

// Run is the env-mcp entry point. Filled in by Task 6.
func Run(_ context.Context, _ RunArgs, _ io.Reader, _ io.Writer, _ io.Writer, _ *slog.Logger) error {
	return errors.New("envmcp.Run: not yet implemented")
}
