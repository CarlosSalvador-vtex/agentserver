// internal/agent/tui/runtime_cwd.go
package tui

import "github.com/agentserver/agentserver/internal/agent"

// writeRuntimeCwd is called by Model.runLocalCommand("cd", ...) to update
// the local executor's working directory. It writes through to the executor
// session JSON file; the ExecutorClient picks up the change on its next tool
// call. No network round-trip.
func writeRuntimeCwd(executorID, cwd string) error {
	if executorID == "" || cwd == "" {
		return nil
	}
	return agent.SetRuntimeCwd(executorID, cwd)
}
