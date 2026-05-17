package envmcp

import (
	"context"
	"encoding/json"
)

// listEnvironmentsSchema: empty object, no args.
var listEnvironmentsSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// ListEnvironmentsTool returns the workspace's connected executors.
// Per v0.54.0 the LLM-facing view shows only name + description +
// last_seen (no exe_id). The shared NameResolver populates its cache
// as a side effect of every call, so subsequent shell/apply_patch/etc
// tool calls can look up name → exe_id.
type ListEnvironmentsTool struct {
	resolver *NameResolver
}

func NewListEnvironmentsTool(resolver *NameResolver) *ListEnvironmentsTool {
	return &ListEnvironmentsTool{resolver: resolver}
}

func (t *ListEnvironmentsTool) Name() string { return "list_environments" }

func (t *ListEnvironmentsTool) Description() string {
	return "Return the list of environments (machines) currently connected to this workspace. " +
		"Each entry has `name`, `description`, `is_default`, and `last_seen`. Pass the `name` " +
		"as the env_id parameter to shell / apply_patch / read_file / unified_exec / etc."
}

func (t *ListEnvironmentsTool) InputSchema() json.RawMessage { return listEnvironmentsSchema }

func (t *ListEnvironmentsTool) Call(ctx context.Context, _ json.RawMessage) (MCPCallToolResult, error) {
	body, err := t.resolver.LLMView(ctx)
	if err != nil {
		return errResult("list_environments: " + err.Error()), nil
	}
	return MCPCallToolResult{
		Content: []MCPToolContent{{Type: "text", Text: string(body)}},
	}, nil
}
