// internal/agent/tui/panels.go
package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Panel is the interface implemented by every floating overlay (permission,
// ask_user, login, logout, etc.). The Model treats panels uniformly: route
// keys via HandleKey, render via View, dismiss via the boolean.
type Panel interface {
	View(width int) string
	HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool)
	ID() string
}

// ---- Permission Panel ----

type PermissionPanelInput struct {
	PID        string
	Tool       string
	ExecutorID string
	SelfExecID string          // local executor's id (for "this machine" tag)
	Args       json.RawMessage
}

// SendDecisionMsg is emitted by the permission panel when the user picks a
// verdict. The Model converts this into a Bus.PostDecision call.
type SendDecisionMsg struct {
	PID, Verdict, Scope string
}

// RequeuePermissionMsg is emitted by the permission panel when the user
// presses Esc ("answer later"). The Model appends the panel to the back of
// permQueue so the request isn't permanently dismissed.
type RequeuePermissionMsg struct{ Panel Panel }

type permissionPanel struct {
	in            PermissionPanelInput
	nestedDisable bool
}

func NewPermissionPanel(in PermissionPanelInput) Panel {
	p := &permissionPanel{in: in}
	p.nestedDisable = looksLikeNestedShell(in.Args)
	return p
}

// looksLikeNestedShell returns true for `bash -c "..."` style args. The
// `always` scope is unsafe for these because the rule key is just the first
// two tokens (e.g. "bash -c"), which would auto-allow ANY future bash -c.
func looksLikeNestedShell(args json.RawMessage) bool {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	cmd, _ := m["command"].(string)
	head := strings.Fields(cmd)
	if len(head) < 2 {
		return false
	}
	switch head[0] {
	case "bash", "sh", "zsh", "dash", "ash", "fish":
		if head[1] == "-c" {
			return true
		}
	}
	return false
}

func (p *permissionPanel) ID() string { return p.in.PID }

func (p *permissionPanel) View(width int) string {
	var sb strings.Builder
	location := "elsewhere"
	if p.in.ExecutorID == p.in.SelfExecID && p.in.SelfExecID != "" {
		location = "this machine"
	}
	sb.WriteString(StylePanelTitle.Render(fmt.Sprintf("permission_request %s", p.in.PID)))
	sb.WriteByte('\n')
	sb.WriteString(fmt.Sprintf("%s on %s (%s)\n", p.in.Tool, p.in.ExecutorID, location))
	sb.WriteString("  args: ")
	sb.WriteString(briefRaw(p.in.Args, 120))
	sb.WriteByte('\n')
	if p.nestedDisable {
		sb.WriteString(StyleAuthErr.Render("[ a ] always disabled (nested shell command)"))
		sb.WriteByte('\n')
		sb.WriteString("[ y ] allow once   [ N ] deny   [ esc ] later")
	} else {
		sb.WriteString("[ y ] allow once   [ a ] always   [ N ] deny   [ esc ] later")
	}
	return StyleBorder.Render(sb.String())
}

func (p *permissionPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
	var verdict, scope string
	switch {
	case keyIs(msg, "y"):
		verdict, scope = "allow", "once"
	case keyIs(msg, "a"):
		if p.nestedDisable {
			return p, nil, false
		}
		verdict, scope = "allow", "always"
	case keyIs(msg, "n"), msg.Type == tea.KeyEnter:
		verdict, scope = "deny", "once"
	case msg.Type == tea.KeyEsc:
		panel := Panel(p)
		return p, func() tea.Msg { return RequeuePermissionMsg{Panel: panel} }, true // dismissed; Model re-queues
	default:
		return p, nil, false
	}
	pid := p.in.PID
	return p, func() tea.Msg {
		return SendDecisionMsg{PID: pid, Verdict: verdict, Scope: scope}
	}, true
}

func keyIs(msg tea.KeyMsg, s string) bool {
	if msg.Type != tea.KeyRunes {
		return false
	}
	return string(msg.Runes) == s
}

func briefRaw(raw json.RawMessage, max int) string {
	s := string(raw)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// ---- AskUser Panel ----

type AskUserPanelInput struct {
	QID         string
	Question    string
	Options     []string
	MultiSelect bool
}

// SendAnswerMsg is emitted by the ask_user panel when the user submits.
// The Model converts this into a Bus.PostAnswer call (endpoint TBD in
// agent-side; for now the Model can just log it).
type SendAnswerMsg struct {
	QID      string
	Selected []string
}

type askUserPanel struct {
	in     AskUserPanelInput
	cursor int
	picked map[int]bool
}

func NewAskUserPanel(in AskUserPanelInput) Panel {
	return &askUserPanel{in: in, picked: map[int]bool{}}
}

func (p *askUserPanel) ID() string { return p.in.QID }

func (p *askUserPanel) View(width int) string {
	var sb strings.Builder
	sb.WriteString(StylePanelTitle.Render(p.in.Question))
	sb.WriteByte('\n')
	for i, opt := range p.in.Options {
		marker := "  "
		if i == p.cursor {
			marker = "▸ "
		}
		check := ""
		if p.in.MultiSelect {
			if p.picked[i] {
				check = "[x] "
			} else {
				check = "[ ] "
			}
		}
		sb.WriteString(fmt.Sprintf("%s%s%s\n", marker, check, opt))
	}
	if p.in.MultiSelect {
		sb.WriteString(StyleHint.Render("space toggle · enter submit · esc cancel"))
	} else {
		sb.WriteString(StyleHint.Render("enter submit · esc cancel"))
	}
	return StyleBorder.Render(sb.String())
}

func (p *askUserPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
	switch {
	case msg.Type == tea.KeyDown:
		if p.cursor < len(p.in.Options)-1 {
			p.cursor++
		}
	case msg.Type == tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case keyIs(msg, " ") && p.in.MultiSelect:
		p.picked[p.cursor] = !p.picked[p.cursor]
	case msg.Type == tea.KeyEnter:
		var sel []string
		if p.in.MultiSelect {
			for i, opt := range p.in.Options {
				if p.picked[i] {
					sel = append(sel, opt)
				}
			}
		} else {
			sel = []string{p.in.Options[p.cursor]}
		}
		qid := p.in.QID
		return p, func() tea.Msg {
			return SendAnswerMsg{QID: qid, Selected: sel}
		}, true
	case msg.Type == tea.KeyEsc:
		return p, nil, true
	}
	return p, nil, false
}
