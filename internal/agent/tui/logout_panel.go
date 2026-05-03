// internal/agent/tui/logout_panel.go
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmLogoutMsg is emitted when the user confirms logout. Model calls
// AuthController.Logout in response.
type ConfirmLogoutMsg struct{}

type logoutPanel struct{}

func NewLogoutPanel() Panel { return &logoutPanel{} }

func (p *logoutPanel) ID() string { return "logout" }

func (p *logoutPanel) View(width int) string {
	var sb strings.Builder
	sb.WriteString(StylePanelTitle.Render("Logout?"))
	sb.WriteByte('\n')
	sb.WriteString("Local credentials and executor session will be cleared.\n")
	sb.WriteString(StyleHint.Render("[ y ] confirm   [ N ] cancel"))
	return StyleBorder.Render(sb.String())
}

func (p *logoutPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
	switch {
	case keyIs(msg, "y"):
		return p, func() tea.Msg { return ConfirmLogoutMsg{} }, true
	case keyIs(msg, "n"), msg.Type == tea.KeyEsc, msg.Type == tea.KeyEnter:
		return p, nil, true
	}
	return p, nil, false
}
