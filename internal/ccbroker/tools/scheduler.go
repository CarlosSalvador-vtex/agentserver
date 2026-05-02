package tools

import (
	"context"
	"fmt"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

type createScheduledTaskInput struct {
	Cron        string `json:"cron"`
	Prompt      string `json:"prompt"`
	Recurring   bool   `json:"recurring"`
	Description string `json:"description,omitempty"`
}
type cancelScheduledTaskInput struct {
	TaskID string `json:"task_id"`
}

func schedulerTools(tctx *Context) []agentsdk.McpTool {
	notImpl := func(name string) *agentsdk.McpToolResult {
		return errResult(fmt.Errorf("%s: scheduler service not yet implemented in agentserver", name))
	}
	return []agentsdk.McpTool{
		agentsdk.Tool[createScheduledTaskInput]("create_scheduled_task",
			"Create a scheduled task that runs a prompt on a cron schedule.",
			func(ctx context.Context, _ createScheduledTaskInput) (*agentsdk.McpToolResult, error) {
				return notImpl("create_scheduled_task"), nil
			}),
		agentsdk.Tool[struct{}]("list_scheduled_tasks",
			"List all scheduled tasks for this workspace.",
			func(ctx context.Context, _ struct{}) (*agentsdk.McpToolResult, error) {
				return notImpl("list_scheduled_tasks"), nil
			}),
		agentsdk.Tool[cancelScheduledTaskInput]("cancel_scheduled_task",
			"Cancel a scheduled task by ID.",
			func(ctx context.Context, _ cancelScheduledTaskInput) (*agentsdk.McpToolResult, error) {
				return notImpl("cancel_scheduled_task"), nil
			}),
	}
}
