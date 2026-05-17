// Command astool dispatches a single MCP tool call against the codex
// app-server through the agentserver codex-app-gateway, bypassing the LLM.
//
// Intended use: invoke from codex TUI shell mode (`!astool env_mcp
// list_environments`). The TUI injects CODEX_THREAD_ID into the
// subprocess env (see codex protocol/src/shell_environment.rs); astool
// reuses that thread to issue an `mcpServer/tool/call` JSON-RPC over
// the gateway's WebSocket endpoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nhooyr.io/websocket"
)

const usage = `astool — invoke a codex MCP tool directly through the gateway

Usage:
  astool [flags] <server> <tool> [json-args]

Arguments:
  <server>     MCP server name as configured in [mcp_servers] (e.g. env_mcp)
  <tool>       Tool name (e.g. list_environments). Do NOT prefix mcp__server__.
  [json-args]  Optional JSON object for the tool's arguments.

Flags:
  --gateway <url>   gateway WS URL (env ASTOOL_GATEWAY_URL,
                    default ws://localhost:8086/codex-app/ws)
  --token   <tok>   bearer token (env ASTOOL_TOKEN)
  --thread  <id>    thread id (env CODEX_THREAD_ID — auto-set by codex TUI ! mode)
  --timeout <dur>   overall timeout (default 30s)
  --json            print raw JSON-RPC response instead of human text
  --verbose         log RPC traffic to stderr
`

type flags struct {
	gateway string
	token   string
	thread  string
	timeout time.Duration
	jsonOut bool
	verbose bool
}

func parseFlags(args []string) (flags, []string, error) {
	fs := flag.NewFlagSet("astool", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	gateway := fs.String("gateway", "", "")
	token := fs.String("token", "", "")
	thread := fs.String("thread", "", "")
	timeout := fs.Duration("timeout", 30*time.Second, "")
	jsonOut := fs.Bool("json", false, "")
	verbose := fs.Bool("verbose", false, "")
	if err := fs.Parse(args); err != nil {
		return flags{}, nil, err
	}
	f := flags{
		gateway: firstNonEmpty(*gateway, os.Getenv("ASTOOL_GATEWAY_URL"), "ws://localhost:8086/codex-app/ws"),
		token:   firstNonEmpty(*token, os.Getenv("ASTOOL_TOKEN")),
		thread:  firstNonEmpty(*thread, os.Getenv("CODEX_THREAD_ID")),
		timeout: *timeout,
		jsonOut: *jsonOut,
		verbose: *verbose,
	}
	return f, fs.Args(), nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "astool: "+err.Error())
		os.Exit(exitCodeFor(err))
	}
}

// toolErrorExit signals that the MCP tool itself returned is_error=true.
// Distinguished from transport errors so callers can branch on exit code.
type toolErrorExit struct{ msg string }

func (e *toolErrorExit) Error() string { return e.msg }

func exitCodeFor(err error) int {
	var te *toolErrorExit
	if errors.As(err, &te) {
		return 2
	}
	return 1
}

func run(rawArgs []string) error {
	if len(rawArgs) == 1 && (rawArgs[0] == "-h" || rawArgs[0] == "--help" || rawArgs[0] == "help") {
		fmt.Fprint(os.Stderr, usage)
		return nil
	}
	f, positional, err := parseFlags(rawArgs)
	if err != nil {
		fmt.Fprint(os.Stderr, usage)
		return err
	}
	if len(positional) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("missing <server> and <tool>")
	}
	server, tool := positional[0], positional[1]
	var arguments json.RawMessage
	if len(positional) >= 3 {
		raw := strings.TrimSpace(strings.Join(positional[2:], " "))
		if !json.Valid([]byte(raw)) {
			return fmt.Errorf("invalid JSON arguments: %s", raw)
		}
		arguments = json.RawMessage(raw)
	}
	if f.token == "" {
		return errors.New("missing token (set --token or ASTOOL_TOKEN)")
	}
	if f.thread == "" {
		return errors.New("missing thread id (set --thread or CODEX_THREAD_ID; codex TUI ! mode auto-sets it)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()
	sigCtx, stopSig := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSig()

	resp, err := callMCPTool(sigCtx, f, server, tool, arguments)
	if err != nil {
		return err
	}
	return renderResponse(resp, f.jsonOut)
}

// --- JSON-RPC over WebSocket -------------------------------------------------

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) String() string {
	if len(e.Data) == 0 {
		return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s (data: %s)", e.Code, e.Message, e.Data)
}

// mcpToolCallResult mirrors codex_app_server_protocol::McpServerToolCallResponse.
type mcpToolCallResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           *bool             `json:"isError,omitempty"`
	Meta              json.RawMessage   `json:"_meta,omitempty"`
}

func callMCPTool(ctx context.Context, f flags, server, tool string, arguments json.RawMessage) (*mcpToolCallResult, error) {
	conn, _, err := websocket.Dial(ctx, f.gateway, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"Authorization": {"Bearer " + f.token},
		},
		// codex app-server rejects permessage-deflate at handshake.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", f.gateway, err)
	}
	conn.SetReadLimit(64 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	send := func(v any) error {
		buf, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if f.verbose {
			fmt.Fprintln(os.Stderr, "→", string(buf))
		}
		return conn.Write(ctx, websocket.MessageText, buf)
	}

	recv := func() (*rpcResponse, error) {
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return nil, err
			}
			if f.verbose {
				fmt.Fprintln(os.Stderr, "←", string(raw))
			}
			var resp rpcResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return nil, fmt.Errorf("decode rpc frame: %w", err)
			}
			// Skip server notifications/requests (no id, or id we didn't send).
			// We only return frames carrying a result/error and an id.
			if resp.ID == nil {
				continue
			}
			return &resp, nil
		}
	}

	// 1. initialize — required handshake before any other method.
	initParams := map[string]any{
		"clientInfo": map[string]any{
			"name":    "astool",
			"title":   "astool",
			"version": "0",
		},
		"capabilities": map[string]any{
			"experimentalApi":          true,
			"requestAttestation":       false,
			"optOutNotificationMethods": []string{},
		},
	}
	if err := send(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: initParams}); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}
	initResp, err := recv()
	if err != nil {
		return nil, fmt.Errorf("recv initialize: %w", err)
	}
	if initResp.Error != nil {
		return nil, errors.New(initResp.Error.String())
	}

	// 2. initialized notification.
	if err := send(rpcNotification{JSONRPC: "2.0", Method: "initialized"}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	// 3. mcpServer/tool/call against the existing thread.
	callParams := map[string]any{
		"thread_id": f.thread,
		"server":    server,
		"tool":      tool,
	}
	if arguments != nil {
		callParams["arguments"] = arguments
	}
	if err := send(rpcRequest{JSONRPC: "2.0", ID: 2, Method: "mcpServer/tool/call", Params: callParams}); err != nil {
		return nil, fmt.Errorf("send tool/call: %w", err)
	}
	callResp, err := recv()
	if err != nil {
		return nil, fmt.Errorf("recv tool/call: %w", err)
	}
	if callResp.Error != nil {
		return nil, errors.New(callResp.Error.String())
	}

	var result mcpToolCallResult
	if err := json.Unmarshal(callResp.Result, &result); err != nil {
		return nil, fmt.Errorf("decode tool/call result: %w", err)
	}
	return &result, nil
}

// --- Output rendering --------------------------------------------------------

func renderResponse(resp *mcpToolCallResult, jsonOut bool) error {
	if jsonOut {
		buf, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(buf))
		if resp.IsError != nil && *resp.IsError {
			return &toolErrorExit{msg: "tool reported isError=true"}
		}
		return nil
	}

	for _, item := range resp.Content {
		var typed struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(item, &typed); err == nil && typed.Type == "text" {
			fmt.Println(typed.Text)
			continue
		}
		// Non-text content (image, resource_link, …) — dump pretty JSON.
		var pretty any
		_ = json.Unmarshal(item, &pretty)
		buf, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(buf))
	}

	if len(resp.StructuredContent) > 0 {
		fmt.Println("---")
		var pretty any
		_ = json.Unmarshal(resp.StructuredContent, &pretty)
		buf, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(buf))
	}

	if resp.IsError != nil && *resp.IsError {
		return &toolErrorExit{msg: "tool reported isError=true"}
	}
	return nil
}
