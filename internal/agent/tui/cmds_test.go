// internal/agent/tui/cmds_test.go
package tui

import "testing"

func TestParseSlashCommand_ClassifiesCorrectly(t *testing.T) {
	cases := []struct {
		in    string
		class CommandClass
		name  string
		args  string
	}{
		{"/quit", LocalClass, "quit", ""},
		{"/cd /tmp", LocalClass, "cd", "/tmp"},
		{"/yolo", LocalClass, "yolo", ""},
		{"/login", LocalClass, "login", ""},
		{"/logout", LocalClass, "logout", ""},
		{"/attach foo.png", LocalClass, "attach", "foo.png"},
		{"/help", LocalClass, "help", ""},
		{"/clear", SessionClass, "clear", ""},
		{"/resume cse_x", SessionClass, "resume", "cse_x"},
		{"/take-control", SessionClass, "take-control", ""},
		{"/observe", SessionClass, "observe", ""},
		{"/model claude-opus-4-7", RemoteClass, "model", "claude-opus-4-7"},
		{"/permission bypass", RemoteClass, "permission", "bypass"},
		{"/compact", RemoteClass, "compact", ""},
		{"/cost", RemoteClass, "cost", ""},
		{"/agents", RemoteClass, "agents", ""},
		{"/whatever", RemoteClass, "whatever", ""},
	}
	for _, c := range cases {
		cmd, ok := ParseSlashCommand(c.in)
		if !ok {
			t.Errorf("%q parse failed", c.in)
			continue
		}
		if cmd.Class != c.class || cmd.Name != c.name || cmd.Args != c.args {
			t.Errorf("%q → %+v want class=%v name=%q args=%q",
				c.in, cmd, c.class, c.name, c.args)
		}
	}
}

func TestParseSlashCommand_RejectsNonSlash(t *testing.T) {
	if _, ok := ParseSlashCommand("not a slash command"); ok {
		t.Error("plain text should not parse as slash")
	}
}
