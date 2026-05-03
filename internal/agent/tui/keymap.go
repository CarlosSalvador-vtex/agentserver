// internal/agent/tui/keymap.go
package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap centralises every keybinding so the help screen and Update can
// share definitions. Add new bindings here, not scattered in handlers.
type KeyMap struct {
	Send        key.Binding
	Newline     key.Binding
	Cancel      key.Binding
	Quit        key.Binding
	SessionPick key.Binding
	Slash       key.Binding
	Help        key.Binding
	PageUp      key.Binding
	PageDown    key.Binding
	Top         key.Binding
	Bottom      key.Binding
}

func NewKeyMap() KeyMap {
	return KeyMap{
		Send:        key.NewBinding(key.WithKeys("enter"),       key.WithHelp("enter", "send")),
		Newline:     key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline")),
		Cancel:      key.NewBinding(key.WithKeys("esc"),         key.WithHelp("esc", "cancel turn / clear input")),
		Quit:        key.NewBinding(key.WithKeys("ctrl+c"),      key.WithHelp("ctrl+c x2", "quit")),
		SessionPick: key.NewBinding(key.WithKeys("ctrl+t"),      key.WithHelp("ctrl+t", "session switcher")),
		Slash:       key.NewBinding(key.WithKeys("/"),           key.WithHelp("/", "command palette")),
		Help:        key.NewBinding(key.WithKeys("?"),           key.WithHelp("?", "key help")),
		PageUp:      key.NewBinding(key.WithKeys("pgup"),        key.WithHelp("pgup", "page up")),
		PageDown:    key.NewBinding(key.WithKeys("pgdown"),      key.WithHelp("pgdn", "page down")),
		Top:         key.NewBinding(key.WithKeys("home"),        key.WithHelp("home", "top")),
		Bottom:      key.NewBinding(key.WithKeys("end"),         key.WithHelp("end", "bottom")),
	}
}
