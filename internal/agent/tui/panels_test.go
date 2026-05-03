// internal/agent/tui/panels_test.go
package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPermissionPanel_RendersFields(t *testing.T) {
	p := NewPermissionPanel(PermissionPanelInput{
		PID:        "perm_p1",
		Tool:       "remote_bash",
		ExecutorID: "exe_a",
		SelfExecID: "exe_a",
		Args:       json.RawMessage(`{"command":"git diff"}`),
	})
	out := p.View(80)
	if !strings.Contains(out, "perm_p1") || !strings.Contains(out, "remote_bash") {
		t.Errorf("missing fields: %s", out)
	}
	if !strings.Contains(out, "this machine") {
		t.Errorf("self exec hint missing: %s", out)
	}
	if !strings.Contains(out, "git diff") {
		t.Errorf("args not shown: %s", out)
	}
}

func TestPermissionPanel_KeysProduceCorrectOutcome(t *testing.T) {
	p := NewPermissionPanel(PermissionPanelInput{
		PID: "p1", Tool: "remote_bash", ExecutorID: "e", SelfExecID: "e",
		Args: json.RawMessage(`{}`),
	})
	cases := []struct {
		key       string
		wantVerd  string
		wantScope string
	}{
		{"y", "allow", "once"},
		{"a", "allow", "always"},
		{"n", "deny", "once"},
		{"enter", "deny", "once"},
	}
	for _, c := range cases {
		var msg tea.KeyMsg
		switch c.key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(c.key)}
		}
		np, cmd, dismissed := p.HandleKey(msg)
		_ = np
		if !dismissed {
			t.Errorf("key %q should dismiss panel", c.key)
			continue
		}
		out := cmd()
		sd, ok := out.(SendDecisionMsg)
		if !ok {
			t.Errorf("key %q produced %T", c.key, out)
			continue
		}
		if sd.Verdict != c.wantVerd || sd.Scope != c.wantScope {
			t.Errorf("key %q produced verdict=%q scope=%q want %q %q",
				c.key, sd.Verdict, sd.Scope, c.wantVerd, c.wantScope)
		}
	}
}

func TestPermissionPanel_DisablesAlwaysOnNestedShell(t *testing.T) {
	p := NewPermissionPanel(PermissionPanelInput{
		PID: "p1", Tool: "remote_bash", ExecutorID: "e", SelfExecID: "e",
		Args: json.RawMessage(`{"command":"bash -c \"rm -rf /\""}`),
	})
	out := p.View(80)
	if !strings.Contains(out, "always disabled") {
		t.Errorf("nested shell warning missing: %s", out)
	}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	_, cmd, dismissed := p.HandleKey(msg)
	if dismissed {
		t.Errorf("'a' must NOT dismiss when always is disabled")
	}
	if cmd != nil {
		t.Errorf("'a' must produce no cmd when always is disabled")
	}
}

func TestAskUserPanel_SingleSelect(t *testing.T) {
	p := NewAskUserPanel(AskUserPanelInput{
		QID:      "q1",
		Question: "Pick one:",
		Options:  []string{"foo", "bar", "baz"},
	})
	p2, _, _ := p.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd, dismissed := p2.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !dismissed {
		t.Fatal("not dismissed on enter")
	}
	out := cmd()
	ans, ok := out.(SendAnswerMsg)
	if !ok {
		t.Fatalf("got %T", out)
	}
	if ans.QID != "q1" || ans.Selected[0] != "baz" {
		t.Errorf("answer = %+v", ans)
	}
}

func TestAskUserPanel_MultiSelect(t *testing.T) {
	p := NewAskUserPanel(AskUserPanelInput{
		QID:         "q2",
		Question:    "Pick many",
		Options:     []string{"a", "b", "c"},
		MultiSelect: true,
	})
	p2, _, _ := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}) // toggle a
	p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}) // toggle c
	_, cmd, _ := p2.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	ans := cmd().(SendAnswerMsg)
	if len(ans.Selected) != 2 || ans.Selected[0] != "a" || ans.Selected[1] != "c" {
		t.Errorf("answer = %+v", ans.Selected)
	}
}
