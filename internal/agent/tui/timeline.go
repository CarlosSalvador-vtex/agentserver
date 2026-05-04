package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// TimelineItem is one rendered row (or block) in the message stream.
type TimelineItem struct {
	EventID    string
	EventType  string
	Payload    json.RawMessage
	Resolution map[string]any // for permission_request items: filled when resolved
}

// Timeline buffers up to `cap` items, indexed by event ID and (for
// permission_request items) by permission ID so resolutions can find their
// requests. Render serialises every item into a single multi-line string.
type Timeline struct {
	mu      sync.Mutex
	items   []TimelineItem
	cap     int
	indexBy map[string]int // event_id → items[idx]
	permIdx map[string]int // permission_id → items[idx] (request rows only)
}

func NewTimeline(cap int) *Timeline {
	return &Timeline{
		cap:     cap,
		indexBy: map[string]int{},
		permIdx: map[string]int{},
	}
}

func (t *Timeline) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.items)
}

// Append adds one event. Special-case: permission_resolved updates the
// matching permission_request's Resolution field rather than adding a row.
func (t *Timeline) Append(ev SSEEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ev.Type == "permission_resolved" {
		var p struct {
			PermissionID string         `json:"permission_id"`
			Decision     map[string]any `json:"decision"`
		}
		_ = json.Unmarshal(ev.Data, &p)
		if idx, ok := t.permIdx[p.PermissionID]; ok && idx < len(t.items) {
			t.items[idx].Resolution = p.Decision
			return
		}
	}
	item := TimelineItem{
		EventID:   ev.LastEventID,
		EventType: ev.Type,
		Payload:   ev.Data,
	}
	t.items = append(t.items, item)
	if ev.LastEventID != "" {
		t.indexBy[ev.LastEventID] = len(t.items) - 1
	}
	if ev.Type == "permission_request" {
		var p struct {
			PermissionID string `json:"permission_id"`
		}
		_ = json.Unmarshal(ev.Data, &p)
		if p.PermissionID != "" {
			t.permIdx[p.PermissionID] = len(t.items) - 1
		}
	}
	if len(t.items) > t.cap {
		drop := len(t.items) - t.cap
		t.items = append([]TimelineItem(nil), t.items[drop:]...)
		// Rebuild indexes after the drop.
		t.indexBy = map[string]int{}
		t.permIdx = map[string]int{}
		for i, it := range t.items {
			if it.EventID != "" {
				t.indexBy[it.EventID] = i
			}
			if it.EventType == "permission_request" {
				var p struct {
					PermissionID string `json:"permission_id"`
				}
				_ = json.Unmarshal(it.Payload, &p)
				if p.PermissionID != "" {
					t.permIdx[p.PermissionID] = i
				}
			}
		}
	}
}

// Render produces a single multi-line string ready for the viewport.
// selfExecID identifies the local executor; tool_use rows tagged with that
// id render with an "executed locally" marker.
func (t *Timeline) Render(_ int, selfExecID string) string {
	t.mu.Lock()
	items := append([]TimelineItem(nil), t.items...)
	t.mu.Unlock()
	var b strings.Builder
	for _, it := range items {
		b.WriteString(renderItem(it, selfExecID))
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	styleUser     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AB7FF"))
	styleAssist   = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8E07F"))
	styleTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D7D7AF"))
	styleResult   = lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAFD7"))
	styleSystem   = lipgloss.NewStyle().Faint(true)
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A7A"))
	styleLocalTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FFF87")).Italic(true)
)

func renderItem(it TimelineItem, selfExecID string) string {
	switch it.EventType {
	case "user_message":
		var p struct{ Text string `json:"text"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleUser.Render("▸ user") + "\n  " + p.Text
	case "assistant_message":
		var p struct{ Text string `json:"text"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleAssist.Render("▸ assistant") + "\n  " + p.Text
	case "tool_use":
		var p struct {
			Tool       string         `json:"tool"`
			ExecutorID string         `json:"executor_id"`
			Args       map[string]any `json:"args"`
		}
		_ = json.Unmarshal(it.Payload, &p)
		tag := "→ " + p.ExecutorID
		if p.ExecutorID == selfExecID && selfExecID != "" {
			tag = styleLocalTag.Render("→ executed locally")
		}
		return styleTool.Render(fmt.Sprintf("▸ tool_use  %s  %s", p.Tool, tag)) + "\n  " + briefArgs(p.Args)
	case "tool_result":
		var p struct {
			Output   string `json:"output"`
			ExitCode int    `json:"exit_code"`
			IsError  bool   `json:"is_error"`
		}
		_ = json.Unmarshal(it.Payload, &p)
		mark := "✓"
		st := styleResult
		if p.IsError || p.ExitCode != 0 {
			mark = "✗"
			st = styleErr
		}
		return st.Render(fmt.Sprintf("▸ tool_result  %s", mark)) + "\n  " + briefOutput(p.Output)
	case "permission_request":
		var p struct {
			PermissionID string `json:"permission_id"`
			Tool         string `json:"tool"`
			ExecutorID   string `json:"executor_id"`
		}
		_ = json.Unmarshal(it.Payload, &p)
		suffix := ""
		if it.Resolution != nil {
			verdict, _ := it.Resolution["verdict"].(string)
			scope, _ := it.Resolution["scope"].(string)
			suffix = styleSystem.Render(fmt.Sprintf("  (%s, %s)", verdict, scope))
		}
		return styleSystem.Render(fmt.Sprintf("▸ permission_request %s %s on %s", p.PermissionID, p.Tool, p.ExecutorID)) + suffix
	case "turn_done", "turn_started", "turn_cancelled":
		return styleSystem.Render(fmt.Sprintf("— %s —", it.EventType))
	case "compaction":
		return styleSystem.Render("─── context compacted ───")
	case "send_message":
		var p struct{ Text string `json:"text"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleAssist.Render("▸ assistant (im)") + "\n  " + p.Text
	case "send_image":
		return styleSystem.Render("▸ image attached (terminal protocol render — v1 stub)")
	case "send_file":
		var p struct{ Filename string `json:"filename"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleSystem.Render("▸ file: " + p.Filename)
	case "ask_user":
		return styleSystem.Render("▸ ask_user — answer in panel")
	case "permission_responder_lost", "permission_responder_changed":
		return styleSystem.Render("⚠ control transferred")
	case "hint":
		var p struct{ Text string `json:"text"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleSystem.Render("ℹ " + p.Text)
	case "fatal_error", "login_failed", "logout_error":
		var p struct{ Error string `json:"error"` }
		_ = json.Unmarshal(it.Payload, &p)
		return styleErr.Render("✗ "+it.EventType+": ") + p.Error
	default:
		return styleSystem.Render("▸ " + it.EventType)
	}
}

func briefArgs(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func briefOutput(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 8 {
		lines = append(lines[:8], "… (truncated)")
	}
	return strings.Join(lines, "\n  ")
}
