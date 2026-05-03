// internal/agent/tui/cmds.go
package tui

import "strings"

// CommandClass distinguishes how a slash command is dispatched:
//
//   - LocalClass: handled in-process by the TUI (e.g. /quit).
//   - SessionClass: changes which session this TUI is attached to.
//   - RemoteClass: forwarded to agentserver /control. The TUI does not
//     interpret these — they're whatever agentserver supports today plus
//     any future R-class command added server-side without TUI changes.
type CommandClass int

const (
	LocalClass CommandClass = iota
	SessionClass
	RemoteClass
)

type ParsedCommand struct {
	Class CommandClass
	Name  string
	Args  string
}

var (
	localCommands = map[string]bool{
		"quit":   true,
		"cd":     true,
		"yolo":   true,
		"login":  true,
		"logout": true,
		"attach": true,
		"help":   true,
	}
	sessionCommands = map[string]bool{
		"clear":        true,
		"resume":       true,
		"take-control": true,
		"observe":      true,
		"sessions":     true,
	}
)

// ParseSlashCommand classifies a slash-prefixed line. Anything that's
// neither local nor session falls through to RemoteClass — agentserver
// decides if it's recognised.
func ParseSlashCommand(line string) (ParsedCommand, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return ParsedCommand{}, false
	}
	rest := strings.TrimPrefix(line, "/")
	parts := strings.SplitN(rest, " ", 2)
	name := parts[0]
	var args string
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	cls := RemoteClass
	switch {
	case localCommands[name]:
		cls = LocalClass
	case sessionCommands[name]:
		cls = SessionClass
	}
	return ParsedCommand{Class: cls, Name: name, Args: args}, true
}
