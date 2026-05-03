// internal/agent/tui/styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	StyleBorder       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	StylePanelTitle   = lipgloss.NewStyle().Bold(true)
	StyleStatusBar    = lipgloss.NewStyle().Background(lipgloss.Color("#222")).Foreground(lipgloss.Color("#ccc"))
	StyleStatusBarErr = StyleStatusBar.Foreground(lipgloss.Color("#FF7A7A"))
	StyleHint         = lipgloss.NewStyle().Faint(true)
	StyleAuthErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A7A")).Bold(true)
	StyleAuthOk       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FFF87"))
)
