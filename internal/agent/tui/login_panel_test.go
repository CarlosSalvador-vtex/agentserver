// internal/agent/tui/login_panel_test.go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLoginPanel_ShowsCodeAndURL(t *testing.T) {
	p := NewLoginPanel(LoginInfo{
		UserCode:      "ABCD-EFGH",
		VerifyURL:     "https://example/device",
		VerifyURLFull: "https://example/device?code=ABCD-EFGH",
		ExpiresIn:     900,
	})
	out := p.View(80)
	if !strings.Contains(out, "ABCD-EFGH") || !strings.Contains(out, "https://example/device") {
		t.Errorf("missing details: %s", out)
	}
	if !strings.Contains(out, "[ o ] open browser") {
		t.Errorf("hint missing: %s", out)
	}
}

func TestLoginPanel_OOpensURL(t *testing.T) {
	var openedURL string
	origOpen := openBrowser
	openBrowser = func(u string) error { openedURL = u; return nil }
	defer func() { openBrowser = origOpen }()

	p := NewLoginPanel(LoginInfo{UserCode: "X", VerifyURL: "u1", VerifyURLFull: "u1full"})
	_, cmd, dismissed := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if dismissed {
		t.Errorf("'o' should not dismiss")
	}
	// Force the cmd to execute so openBrowser is called.
	if cmd != nil {
		_ = cmd()
	}
	if openedURL != "u1full" {
		t.Errorf("openedURL=%q want u1full", openedURL)
	}
}

func TestLoginPanel_EscDismissesAndCancels(t *testing.T) {
	p := NewLoginPanel(LoginInfo{UserCode: "X", VerifyURL: "u"})
	_, cmd, dismissed := p.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !dismissed {
		t.Errorf("esc should dismiss")
	}
	if msg := cmd(); msg == nil {
		t.Errorf("esc should produce a CancelLoginMsg")
	} else if _, ok := msg.(CancelLoginMsg); !ok {
		t.Errorf("esc msg type = %T want CancelLoginMsg", msg)
	}
}
