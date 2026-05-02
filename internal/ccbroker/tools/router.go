package tools

import agentsdk "github.com/agentserver/claude-agent-sdk-go"

// BuildMcpServer assembles every tool into one in-process MCP server.
// The server name "cc-broker" matches the AllowedTools wildcard
// "mcp__cc-broker__*" used by runner/options.go.
func BuildMcpServer(tctx *Context) *agentsdk.McpSdkServer {
	var tools []agentsdk.McpTool
	tools = append(tools, executorTools(tctx)...)
	tools = append(tools, workspaceTools(tctx)...)
	tools = append(tools, imTools(tctx)...)
	tools = append(tools, schedulerTools(tctx)...)
	tools = append(tools, askUserTools(tctx)...)
	return agentsdk.CreateSdkMcpServer("cc-broker", "1.0.0", tools...)
}
