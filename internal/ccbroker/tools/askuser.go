package tools

import (
	"context"
	"fmt"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

type askUserQuestionInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

func askUserTools(tctx *Context) []agentsdk.McpTool {
	return []agentsdk.McpTool{
		agentsdk.Tool[askUserQuestionInput]("AskUserQuestion",
			"Ask the user a question and wait for their answer.",
			func(ctx context.Context, _ askUserQuestionInput) (*agentsdk.McpToolResult, error) {
				return errResult(fmt.Errorf("AskUserQuestion: pending-question queue not yet implemented in agentserver")), nil
			}),
	}
}
