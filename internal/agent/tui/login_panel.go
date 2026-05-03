// internal/agent/tui/login_panel.go
package tui

import (
	"bytes"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mdp/qrterminal/v3"
	"github.com/pkg/browser"
)

// CancelLoginMsg is emitted by the login panel when the user presses Esc.
// The Model converts this to AuthController.CancelLogin().
type CancelLoginMsg struct{}

// openBrowser is a test seam — production calls browser.OpenURL.
var openBrowser = func(u string) error { return browser.OpenURL(u) }

type loginPanel struct {
	info LoginInfo
	qr   string
}

func NewLoginPanel(info LoginInfo) Panel {
	return &loginPanel{
		info: info,
		qr:   renderQR(firstNonEmpty(info.VerifyURLFull, info.VerifyURL)),
	}
}

func (p *loginPanel) ID() string { return "login" }

func (p *loginPanel) View(width int) string {
	var sb strings.Builder
	sb.WriteString(StylePanelTitle.Render("Authenticate to agentserver"))
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("  Visit: %s\n", firstNonEmpty(p.info.VerifyURLFull, p.info.VerifyURL)))
	sb.WriteString(fmt.Sprintf("  Code:  %s\n\n", p.info.UserCode))
	sb.WriteString(p.qr)
	sb.WriteByte('\n')
	sb.WriteString(StyleHint.Render("[ o ] open browser   [ esc ] cancel"))
	return StyleBorder.Render(sb.String())
}

func (p *loginPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
	switch {
	case keyIs(msg, "o"):
		u := firstNonEmpty(p.info.VerifyURLFull, p.info.VerifyURL)
		return p, func() tea.Msg {
			_ = openBrowser(u)
			return nil
		}, false
	case msg.Type == tea.KeyEsc:
		return p, func() tea.Msg { return CancelLoginMsg{} }, true
	}
	return p, nil, false
}

func renderQR(url string) string {
	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(url, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         &buf,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		QuietZone:      1,
	})
	return buf.String()
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
