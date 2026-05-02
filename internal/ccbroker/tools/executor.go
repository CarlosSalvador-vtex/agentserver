package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

// --- input shapes ---

type remoteBashInput struct {
	ExecutorID  string `json:"executor_id"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
}

type remoteReadInput struct {
	ExecutorID string `json:"executor_id"`
	FilePath   string `json:"file_path"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type remoteEditInput struct {
	ExecutorID string `json:"executor_id"`
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type remoteWriteInput struct {
	ExecutorID string `json:"executor_id"`
	FilePath   string `json:"file_path"`
	Content    string `json:"content"`
}

type remoteGlobInput struct {
	ExecutorID string `json:"executor_id"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
}

type remoteGrepInput struct {
	ExecutorID string `json:"executor_id"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
}

type remoteLSInput struct {
	ExecutorID string `json:"executor_id"`
	Path       string `json:"path,omitempty"`
}

type listExecutorsInput struct {
	StatusFilter string `json:"status_filter,omitempty"`
}

// executorTools returns all 8 executor-related tools, closures over tctx.
func executorTools(tctx *Context) []agentsdk.McpTool {
	exec := func(toolName string, args any) (*agentsdk.McpToolResult, error) {
		return forwardExecute(tctx, toolName, args)
	}
	return []agentsdk.McpTool{
		agentsdk.Tool[remoteBashInput]("remote_bash",
			"Execute a shell command on the specified executor.",
			func(ctx context.Context, in remoteBashInput) (*agentsdk.McpToolResult, error) {
				return exec("Bash", in)
			}),
		agentsdk.Tool[remoteReadInput]("remote_read",
			"Read a file on the specified executor.",
			func(ctx context.Context, in remoteReadInput) (*agentsdk.McpToolResult, error) {
				return exec("Read", in)
			}),
		agentsdk.Tool[remoteEditInput]("remote_edit",
			"Edit a file on the specified executor.",
			func(ctx context.Context, in remoteEditInput) (*agentsdk.McpToolResult, error) {
				return exec("Edit", in)
			}),
		agentsdk.Tool[remoteWriteInput]("remote_write",
			"Write content to a file on the specified executor.",
			func(ctx context.Context, in remoteWriteInput) (*agentsdk.McpToolResult, error) {
				return exec("Write", in)
			}),
		agentsdk.Tool[remoteGlobInput]("remote_glob",
			"Find files matching a glob pattern on the specified executor.",
			func(ctx context.Context, in remoteGlobInput) (*agentsdk.McpToolResult, error) {
				return exec("Glob", in)
			}),
		agentsdk.Tool[remoteGrepInput]("remote_grep",
			"Search for a regex pattern in files on the specified executor.",
			func(ctx context.Context, in remoteGrepInput) (*agentsdk.McpToolResult, error) {
				return exec("Grep", in)
			}),
		agentsdk.Tool[remoteLSInput]("remote_ls",
			"List directory contents on the specified executor.",
			func(ctx context.Context, in remoteLSInput) (*agentsdk.McpToolResult, error) {
				return exec("LS", in)
			}),
		agentsdk.Tool[listExecutorsInput]("list_executors",
			"List available executors in this workspace with their capabilities.",
			func(ctx context.Context, in listExecutorsInput) (*agentsdk.McpToolResult, error) {
				return listExecutors(tctx)
			}),
	}
}

// forwardExecute routes a remote_* tool call to executor-registry POST /api/execute.
// args must be a struct whose JSON representation contains an "executor_id" field;
// that field is stripped before forwarding in the "arguments" payload.
func forwardExecute(tctx *Context, toolName string, args any) (*agentsdk.McpToolResult, error) {
	// Marshal the typed input so we can extract executor_id and strip it.
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return errResult(fmt.Errorf("marshal tool args: %w", err)), nil
	}

	var argsMap map[string]json.RawMessage
	if err := json.Unmarshal(rawArgs, &argsMap); err != nil {
		return errResult(fmt.Errorf("unmarshal tool args: %w", err)), nil
	}

	executorIDRaw, ok := argsMap["executor_id"]
	if !ok {
		return errResult(fmt.Errorf("executor_id is required")), nil
	}
	var executorID string
	if err := json.Unmarshal(executorIDRaw, &executorID); err != nil || executorID == "" {
		return errResult(fmt.Errorf("executor_id must be a non-empty string")), nil
	}

	// Build arguments without executor_id.
	delete(argsMap, "executor_id")
	cleanArgs, err := json.Marshal(argsMap)
	if err != nil {
		return errResult(fmt.Errorf("marshal clean args: %w", err)), nil
	}

	body, err := json.Marshal(map[string]any{
		"executor_id": executorID,
		"tool":        toolName,
		"arguments":   json.RawMessage(cleanArgs),
	})
	if err != nil {
		return errResult(fmt.Errorf("marshal execute request: %w", err)), nil
	}

	req, err := http.NewRequest(http.MethodPost, tctx.ExecutorRegistryURL+"/api/execute", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build execute request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tctx.HTTP.Do(req)
	if err != nil {
		return errResult(fmt.Errorf("executor-registry request failed: %w", err)), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errResult(fmt.Errorf("read executor-registry response: %w", err)), nil
	}

	return textResult(string(respBody)), nil
}

// listExecutors queries executor-registry GET /api/executors?workspace_id=<wid>.
func listExecutors(tctx *Context) (*agentsdk.McpToolResult, error) {
	u, err := url.Parse(tctx.ExecutorRegistryURL + "/api/executors")
	if err != nil {
		return nil, fmt.Errorf("parse executor-registry URL: %w", err)
	}
	q := u.Query()
	q.Set("workspace_id", tctx.WorkspaceID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build list-executors request: %w", err)
	}

	resp, err := tctx.HTTP.Do(req)
	if err != nil {
		return errResult(fmt.Errorf("executor-registry request failed: %w", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errResult(fmt.Errorf("read executor-registry response: %w", err)), nil
	}

	return textResult(string(body)), nil
}

// errResult wraps an error in an IsError McpToolResult.
func errResult(err error) *agentsdk.McpToolResult {
	return &agentsdk.McpToolResult{
		Content: []agentsdk.McpToolContent{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

// textResult wraps a plain string in a successful McpToolResult.
func textResult(s string) *agentsdk.McpToolResult {
	return &agentsdk.McpToolResult{
		Content: []agentsdk.McpToolContent{{Type: "text", Text: s}},
	}
}
