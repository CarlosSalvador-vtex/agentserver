package tui

import (
	"strings"
	"testing"
)

func TestTimeline_AppendAndRender_BasicEvents(t *testing.T) {
	tl := NewTimeline(100)
	tl.Append(SSEEvent{Type: "user_message", Data: []byte(`{"text":"hi"}`), LastEventID: "1"})
	tl.Append(SSEEvent{Type: "assistant_message", Data: []byte(`{"text":"hello"}`), LastEventID: "2"})
	tl.Append(SSEEvent{Type: "tool_use", Data: []byte(`{"tool_use_id":"tu1","tool":"remote_bash","executor_id":"exe_a","args":{"command":"ls"}}`), LastEventID: "3"})
	tl.Append(SSEEvent{Type: "tool_result", Data: []byte(`{"tool_use_id":"tu1","output":"a\nb","exit_code":0}`), LastEventID: "4"})
	out := tl.Render(80, "exe_a")
	if !strings.Contains(out, "hi") || !strings.Contains(out, "hello") {
		t.Errorf("missing text: %s", out)
	}
	if !strings.Contains(out, "remote_bash") {
		t.Errorf("missing tool name: %s", out)
	}
	if !strings.Contains(out, "executed locally") {
		t.Errorf("local executor tag missing: %s", out)
	}
}

func TestTimeline_NonLocalExecutorTagged(t *testing.T) {
	tl := NewTimeline(100)
	tl.Append(SSEEvent{Type: "tool_use", Data: []byte(`{"tool_use_id":"tu","tool":"remote_bash","executor_id":"exe_other","args":{}}`), LastEventID: "1"})
	out := tl.Render(80, "exe_self")
	if strings.Contains(out, "executed locally") {
		t.Errorf("should NOT tag locally for non-self executor")
	}
	if !strings.Contains(out, "exe_other") {
		t.Errorf("should label foreign executor: %s", out)
	}
}

func TestTimeline_DropsOldestWhenOverCap(t *testing.T) {
	tl := NewTimeline(3)
	for i := 1; i <= 5; i++ {
		tl.Append(SSEEvent{
			Type:        "user_message",
			Data:        []byte(`{"text":"msg` + string(rune('0'+i)) + `"}`),
			LastEventID: "x",
		})
	}
	if tl.Len() != 3 {
		t.Errorf("len=%d want 3", tl.Len())
	}
	out := tl.Render(80, "")
	if strings.Contains(out, "msg1") || strings.Contains(out, "msg2") {
		t.Errorf("oldest items not dropped: %s", out)
	}
	if !strings.Contains(out, "msg5") {
		t.Errorf("newest missing: %s", out)
	}
}

func TestTimeline_PermissionResolvedReplacesRequestState(t *testing.T) {
	tl := NewTimeline(100)
	tl.Append(SSEEvent{
		Type:        "permission_request",
		Data:        []byte(`{"permission_id":"p1","tool":"remote_bash"}`),
		LastEventID: "1",
	})
	tl.Append(SSEEvent{
		Type:        "permission_resolved",
		Data:        []byte(`{"permission_id":"p1","decision":{"verdict":"allow","scope":"once"}}`),
		LastEventID: "2",
	})
	out := tl.Render(80, "")
	if !strings.Contains(out, "allow") {
		t.Errorf("resolved decision not visible: %s", out)
	}
	if strings.Count(out, "p1") < 1 {
		t.Errorf("expected at least one mention of perm id: %s", out)
	}
}
