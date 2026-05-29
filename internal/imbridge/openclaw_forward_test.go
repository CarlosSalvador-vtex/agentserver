package imbridge

import (
	"strings"
	"testing"
)

func TestOpenclawSessionIDStable(t *testing.T) {
	a := openclawSessionID("ch-1", "user-42")
	b := openclawSessionID("ch-1", "user-42")
	if a != b {
		t.Fatalf("session id not stable: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "im-") {
		t.Fatalf("expected im- prefix, got %q", a)
	}
	if strings.Contains(a, "@im.") {
		t.Fatalf("session id must not contain @im. suffix: %q", a)
	}
	c := openclawSessionID("ch-2", "user-42")
	if a == c {
		t.Fatalf("expected different session for different channel")
	}
}

func TestBuildOpenclawAgentCommand(t *testing.T) {
	cmd := buildOpenclawAgentCommand("hello world", "im-deadbeef")
	want := []string{"node", "openclaw.mjs", "agent", "--message", "hello world", "--json", "--session-id", "im-deadbeef"}
	if len(cmd) != len(want) {
		t.Fatalf("len=%d want %d: %v", len(cmd), len(want), cmd)
	}
	for i := range want {
		if cmd[i] != want[i] {
			t.Fatalf("cmd[%d]=%q want %q", i, cmd[i], want[i])
		}
	}
}

func TestParseOpenclawAgentStdout(t *testing.T) {
	stdout := `{"payloads":[{"text":"Oi! Como posso ajudar?"}],"meta":{"durationMs":100}}`
	reply, err := parseOpenclawAgentStdout(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Oi! Como posso ajudar?" {
		t.Fatalf("reply=%q", reply)
	}
}

func TestParseOpenclawAgentStdoutMultiplePayloads(t *testing.T) {
	stdout := `{"payloads":[{"text":"line1"},{"text":"line2"}]}`
	reply, err := parseOpenclawAgentStdout(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "line1\n\nline2" {
		t.Fatalf("reply=%q", reply)
	}
}

func TestParseOpenclawAgentStdoutInvalid(t *testing.T) {
	_, err := parseOpenclawAgentStdout("not json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveRoutingModeOpenclaw(t *testing.T) {
	b := &Bridge{
		channelRouting: map[string]string{"ch-x": routingModeOpenclaw},
	}
	b.SetChannelRoutingMode("ch-x", routingModeOpenclaw)
	mode := b.getChannelRoutingMode("ch-x")
	if mode != routingModeOpenclaw {
		t.Fatalf("got %q", mode)
	}
	mode = b.getChannelRoutingMode("missing")
	if mode != "" {
		t.Fatalf("expected empty for missing channel")
	}
}
