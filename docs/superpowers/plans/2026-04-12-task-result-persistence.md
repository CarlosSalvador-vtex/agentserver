# Task Result Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `check_task` return the actual task output — both the final summary (A) and full session output on demand (B).

**Architecture:** Two-pronged fix. (A) The task worker sends `stream.Result()` back to the server when reporting completion; the server persists it in `tasks.result_json` via the existing `UpdateAgentTaskResult` DB method. (B) `handleGetTask` gains an `include_output` query param that reads `agent_session_events` and extracts human-readable text; the MCP `check_task` tool exposes this as an optional parameter.

**Tech Stack:** Go, PostgreSQL, claude-agent-sdk-go

---

## File Structure

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/server/agent_tasks.go` | `handleUpdateTaskStatus` accepts result; `handleGetTask` supports `include_output` |
| Create | `internal/server/task_output.go` | `extractTaskOutput()` — pure function: events → readable text |
| Create | `internal/server/task_output_test.go` | Unit tests for `extractTaskOutput` |
| Modify | `internal/agent/task_worker.go` | `ExecuteTask` returns `*ResultMessage`; `reportTaskComplete` sends result |
| Modify | `internal/mcpbridge/tools.go` | `check_task` schema gains `include_output`; handler formats output |

---

### Task 1: Extract task output function + unit test

Pure function with no dependencies on server or DB. Build and test in isolation first.

**Files:**
- Create: `internal/server/task_output.go`
- Create: `internal/server/task_output_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/server/task_output_test.go`:

```go
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

func TestExtractTaskOutput_AssistantText(t *testing.T) {
	events := []db.AgentSessionEvent{
		makeEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the output of df -h:"}]}}`),
	}
	got := extractTaskOutput(events)
	if got == "" {
		t.Fatal("expected non-empty output")
	}
	want := "Here is the output of df -h:"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTaskOutput_ToolResult(t *testing.T) {
	events := []db.AgentSessionEvent{
		makeEvent(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":"Filesystem  Size  Used  Avail\n/dev/sda1   100G  40G   60G"}]}}`),
	}
	got := extractTaskOutput(events)
	if got == "" {
		t.Fatal("expected non-empty output")
	}
	if want := "Filesystem  Size  Used  Avail\n/dev/sda1   100G  40G   60G"; want != got {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTaskOutput_Mixed(t *testing.T) {
	events := []db.AgentSessionEvent{
		makeEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"Running df -h..."},{"type":"tool_use","id":"tu_1","name":"Bash"}]}}`),
		makeEvent(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":"/ 100G 40G 60G"}]}}`),
		makeEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"Disk is 40% used."}]}}`),
	}
	got := extractTaskOutput(events)
	if got == "" {
		t.Fatal("expected non-empty output")
	}
	// Should contain all three pieces in order.
	for _, sub := range []string{"Running df -h...", "/ 100G 40G 60G", "Disk is 40% used."} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q, got:\n%s", sub, got)
		}
	}
}

func TestExtractTaskOutput_Empty(t *testing.T) {
	got := extractTaskOutput(nil)
	if got != "" {
		t.Errorf("expected empty string for nil events, got %q", got)
	}
}

func makeEvent(payload string) db.AgentSessionEvent {
	raw := json.RawMessage(payload)
	return db.AgentSessionEvent{
		EventType: "client_event",
		Source:    "worker",
		Payload:   raw,
	}
}

```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/server/ -run TestExtractTaskOutput -v`
Expected: FAIL — `extractTaskOutput` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/server/task_output.go`:

```go
package server

import (
	"encoding/json"
	"strings"

	"github.com/agentserver/agentserver/internal/db"
)

// extractTaskOutput converts session events into human-readable text.
// It extracts assistant text blocks and tool_result content from claude CLI messages.
func extractTaskOutput(events []db.AgentSessionEvent) string {
	var parts []string

	for _, e := range events {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Content []json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(e.Payload, &msg); err != nil {
			continue
		}

		for _, block := range msg.Message.Content {
			var cb struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Content string `json:"content"` // tool_result text content
			}
			if err := json.Unmarshal(block, &cb); err != nil {
				continue
			}

			switch cb.Type {
			case "text":
				if cb.Text != "" {
					parts = append(parts, cb.Text)
				}
			case "tool_result":
				if cb.Content != "" {
					parts = append(parts, cb.Content)
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentserver && go test ./internal/server/ -run TestExtractTaskOutput -v`
Expected: PASS (all 4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/server/task_output.go internal/server/task_output_test.go
git commit -m "feat: add extractTaskOutput for human-readable session event text"
```

---

### Task 2: Server — handleUpdateTaskStatus accepts result

Modify the status update endpoint to persist the task result when the worker reports completion with result data.

**Files:**
- Modify: `internal/server/agent_tasks.go:277-318` (`handleUpdateTaskStatus`)

- [ ] **Step 1: Modify the request struct to accept result fields**

In `internal/server/agent_tasks.go`, replace the `handleUpdateTaskStatus` request struct (lines 291-294):

Old:
```go
	var req struct {
		Status        string `json:"status"`
		FailureReason string `json:"failure_reason,omitempty"`
	}
```

New:
```go
	var req struct {
		Status        string           `json:"status"`
		FailureReason string           `json:"failure_reason,omitempty"`
		Result        json.RawMessage  `json:"result,omitempty"`
		TotalCostUSD  *float64         `json:"total_cost_usd,omitempty"`
		NumTurns      int              `json:"num_turns,omitempty"`
	}
```

Add `"encoding/json"` to the file's import block if not already present (it is — already imported on line 5).

- [ ] **Step 2: Use UpdateAgentTaskResult when result is provided**

Replace lines 307-311:

Old:
```go
	if req.Status == "failed" && req.FailureReason != "" {
		s.DB.FailAgentTask(taskID, req.FailureReason)
	} else {
		s.DB.UpdateAgentTaskStatus(taskID, req.Status)
	}
```

New:
```go
	switch {
	case req.Status == "failed" && req.FailureReason != "":
		s.DB.FailAgentTask(taskID, req.FailureReason)
	case req.Status == "completed" && len(req.Result) > 0:
		s.DB.UpdateAgentTaskResult(taskID, req.Result, req.TotalCostUSD, req.NumTurns)
	default:
		s.DB.UpdateAgentTaskStatus(taskID, req.Status)
	}
```

- [ ] **Step 3: Build to verify**

Run: `cd /root/agentserver && go build ./internal/server/...`
Expected: compiles with no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/server/agent_tasks.go
git commit -m "feat: handleUpdateTaskStatus persists result via UpdateAgentTaskResult"
```

---

### Task 3: Server — handleGetTask supports include_output

Add query parameter support so callers can request the full session output alongside the task status.

**Files:**
- Modify: `internal/server/agent_tasks.go:163-207` (`handleGetTask`)

- [ ] **Step 1: Add include_output logic after the existing response map**

In `handleGetTask`, after line 203 (`resp["completed_at"] = ...`) and before the JSON encode, add:

```go
	if r.URL.Query().Get("include_output") == "true" && task.SessionID.Valid {
		events, err := s.DB.GetAgentSessionEventsSince(task.SessionID.String, 0, 500)
		if err == nil && len(events) > 0 {
			resp["output"] = extractTaskOutput(events)
		}
	}
```

- [ ] **Step 2: Build to verify**

Run: `cd /root/agentserver && go build ./internal/server/...`
Expected: compiles (extractTaskOutput is in the same package).

- [ ] **Step 3: Commit**

```bash
git add internal/server/agent_tasks.go
git commit -m "feat: handleGetTask supports include_output query param"
```

---

### Task 4: Worker — return and report result on completion

Modify the task worker to capture `stream.Result()` and send it back to the server.

**Files:**
- Modify: `internal/agent/task_worker.go:41-141` (`ExecuteTask`)
- Modify: `internal/agent/task_worker.go:144-173` (`RunTaskWorker`)
- Modify: `internal/agent/task_worker.go:249-253` (`reportTaskComplete`)

- [ ] **Step 1: Change ExecuteTask return type**

Change `ExecuteTask` signature (line 41) from:

```go
func (w *TaskWorker) ExecuteTask(ctx context.Context, taskID, sessionID, prompt, systemContext string, maxTurns int, maxBudgetUSD float64) error {
```

to:

```go
func (w *TaskWorker) ExecuteTask(ctx context.Context, taskID, sessionID, prompt, systemContext string, maxTurns int, maxBudgetUSD float64) (*agentsdk.ResultMessage, error) {
```

- [ ] **Step 2: Update return statements in ExecuteTask**

Replace all `return fmt.Errorf(...)` and `return nil` in ExecuteTask:

Line 55 (create session error): `return nil, fmt.Errorf("create session: %w", err)`
Line 69 (fetch creds error): `return nil, fmt.Errorf("fetch credentials: %w", err)`
Line 82 (attach bridge error): `return nil, fmt.Errorf("attach bridge: %w", err)`
Line 129 (stream error): change to:
```go
	if err := stream.Err(); err != nil {
		bridge.ReportState(agentsdk.SessionStateIdle)
		return nil, fmt.Errorf("query execution: %w", err)
	}
```

Replace lines 132-141 (the result + final return) with:

```go
	var resultMsg *agentsdk.ResultMessage
	if result, err := stream.Result(); err == nil && result != nil {
		resultMsg = result
		resultData, _ := json.Marshal(result)
		bridge.WriteBatch([]json.RawMessage{resultData})
	}

	bridge.ReportState(agentsdk.SessionStateIdle)
	log.Printf("task-worker: task %s completed", taskID)
	return resultMsg, nil
```

- [ ] **Step 3: Update RunTaskWorker to pass result to reportTaskComplete**

In `RunTaskWorker` (line 164), change:

```go
				if err := worker.ExecuteTask(ctx, task.ID, task.SessionID, task.Prompt, task.SystemContext, task.MaxTurns, task.MaxBudgetUSD); err != nil {
					log.Printf("task-worker: task %s failed: %v", task.ID, err)
					worker.reportTaskFailure(ctx, task.ID, err.Error())
				} else {
					worker.reportTaskComplete(ctx, task.ID)
				}
```

to:

```go
				resultMsg, err := worker.ExecuteTask(ctx, task.ID, task.SessionID, task.Prompt, task.SystemContext, task.MaxTurns, task.MaxBudgetUSD)
				if err != nil {
					log.Printf("task-worker: task %s failed: %v", task.ID, err)
					worker.reportTaskFailure(ctx, task.ID, err.Error())
				} else {
					worker.reportTaskComplete(ctx, task.ID, resultMsg)
				}
```

- [ ] **Step 4: Update reportTaskComplete to include result data**

Replace `reportTaskComplete` (lines 249-253):

```go
func (w *TaskWorker) reportTaskComplete(ctx context.Context, taskID string) {
	log.Printf("task-worker: task %s completed", taskID)
	body, _ := json.Marshal(map[string]string{"status": "completed"})
	w.updateTaskStatus(ctx, taskID, body)
}
```

with:

```go
func (w *TaskWorker) reportTaskComplete(ctx context.Context, taskID string, result *agentsdk.ResultMessage) {
	log.Printf("task-worker: task %s completed", taskID)
	payload := map[string]any{"status": "completed"}
	if result != nil {
		payload["result"] = result.Result
		payload["total_cost_usd"] = result.TotalCostUSD
		payload["num_turns"] = result.NumTurns
	}
	body, _ := json.Marshal(payload)
	w.updateTaskStatus(ctx, taskID, body)
}
```

Note: `result.Result` is a `string`. When `json.Marshal` encodes the map, the string becomes a JSON string `"text"`. On the server side, `json.RawMessage` captures the raw JSON bytes including the quotes, which is valid JSONB for storage. When read back via `map[string]any`, Go's decoder returns a plain Go string — no double-quoting.

- [ ] **Step 5: Build to verify**

Run: `cd /root/agentserver && go build ./internal/agent/...`
Expected: compiles with no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/task_worker.go
git commit -m "feat: task worker reports result summary on completion"
```

---

### Task 5: MCP bridge — check_task exposes include_output

Update the MCP tool schema and handler to surface both the result and full output.

**Files:**
- Modify: `internal/mcpbridge/tools.go:107-117` (check_task schema)
- Modify: `internal/mcpbridge/tools.go:217-247` (`handleCheckTask`)

- [ ] **Step 1: Add include_output to check_task schema**

In `tools.go`, replace the check_task tool definition (lines 107-117):

```go
		{
			Name:        "check_task",
			description: "Check the status and result of a previously delegated task.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":        map[string]any{"type": "string", "description": "The task ID returned by delegate_task"},
					"include_output": map[string]any{"type": "boolean", "description": "If true, include the full task output from session events (may be long). Default: false."},
				},
				"required": []string{"task_id"},
			},
			Annotations: map[string]any{"readOnlyHint": true},
		},
```

- [ ] **Step 2: Update handleCheckTask to use include_output**

Replace `handleCheckTask` (lines 217-247) with:

```go
func (b *Bridge) handleCheckTask(args json.RawMessage) (*ToolResult, error) {
	var params struct {
		TaskID        string `json:"task_id"`
		IncludeOutput bool   `json:"include_output"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return errorResult("Invalid arguments: " + err.Error()), nil
	}

	url := fmt.Sprintf("%s/api/agent/tasks/%s", b.config.ServerURL, params.TaskID)
	if params.IncludeOutput {
		url += "?include_output=true"
	}
	body, err := b.apiGet(url)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to check task: %v", err)), nil
	}

	var task map[string]any
	json.Unmarshal(body, &task)

	status, _ := task["status"].(string)
	summary := fmt.Sprintf("Task %s: %s", params.TaskID, status)

	if result, ok := task["result"].(string); ok && result != "" {
		summary += "\n\nResult:\n" + result
	}
	if reason, ok := task["failure_reason"].(string); ok && reason != "" {
		summary += "\n\nFailure reason: " + reason
	}
	if output, ok := task["output"].(string); ok && output != "" {
		summary += "\n\nFull output:\n" + output
	}

	return textResult(summary), nil
}
```

- [ ] **Step 3: Build to verify**

Run: `cd /root/agentserver && go build ./internal/mcpbridge/...`
Expected: compiles with no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpbridge/tools.go
git commit -m "feat: check_task MCP tool supports include_output for full session output"
```

---

### Task 6: Full build + test

- [ ] **Step 1: Run all tests**

```bash
cd /root/agentserver && go build ./...
cd /root/agentserver && go test ./internal/server/ -run TestExtractTaskOutput -v
cd /root/agentserver && go vet ./internal/server/... ./internal/mcpbridge/... ./internal/agent/...
```

Expected: build succeeds, 4 tests pass, no vet warnings.

- [ ] **Step 2: Final commit (if any fixups needed)**

Only if earlier steps needed corrections. Otherwise skip.
