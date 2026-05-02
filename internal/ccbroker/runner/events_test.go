package runner

import (
	"encoding/json"
	"testing"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

func TestToEventPayload_AssistantMessage(t *testing.T) {
	raw := json.RawMessage(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`)
	msg := agentsdk.SDKMessage{Type: "assistant", Raw: raw}

	evt, err := ToEventPayload(msg)
	if err != nil {
		t.Fatalf("ToEventPayload: %v", err)
	}
	if evt.EventType != "assistant_message" {
		t.Fatalf("EventType=%q, want assistant_message", evt.EventType)
	}
	if !bytesEqual(evt.Payload, raw) {
		t.Fatalf("Payload not preserved verbatim")
	}
	if evt.Ephemeral {
		t.Fatalf("assistant messages must be persisted (Ephemeral=false)")
	}
}

func TestToEventPayload_StreamEventIsEphemeral(t *testing.T) {
	raw := json.RawMessage(`{"type":"stream_event","event":{"type":"content_block_delta"}}`)
	msg := agentsdk.SDKMessage{Type: "stream_event", Raw: raw}

	evt, err := ToEventPayload(msg)
	if err != nil {
		t.Fatalf("ToEventPayload: %v", err)
	}
	if !evt.Ephemeral {
		t.Fatalf("partial stream events must be marked ephemeral")
	}
}

func TestToEventPayload_KnownTypes(t *testing.T) {
	cases := []struct {
		sdkType, sdkSubtype, want string
		ephemeral                 bool
	}{
		{"user", "", "user_message", false},
		{"assistant", "", "assistant_message", false},
		{"tool_result", "", "tool_result", false},
		{"result", "success", "turn_result", false},
		{"system", "init", "system_init", false},
		{"system", "compact_boundary", "compact_boundary", false},
		{"stream_event", "", "stream_event", true},
		{"tool_progress", "", "tool_progress", true},
	}
	for _, c := range cases {
		raw := json.RawMessage(`{"type":"` + c.sdkType + `","subtype":"` + c.sdkSubtype + `"}`)
		msg := agentsdk.SDKMessage{Type: c.sdkType, Subtype: c.sdkSubtype, Raw: raw}
		evt, err := ToEventPayload(msg)
		if err != nil {
			t.Fatalf("[%s/%s] err: %v", c.sdkType, c.sdkSubtype, err)
		}
		if evt.EventType != c.want {
			t.Fatalf("[%s/%s] EventType=%q want %q", c.sdkType, c.sdkSubtype, evt.EventType, c.want)
		}
		if evt.Ephemeral != c.ephemeral {
			t.Fatalf("[%s/%s] Ephemeral=%v want %v", c.sdkType, c.sdkSubtype, evt.Ephemeral, c.ephemeral)
		}
	}
}

func bytesEqual(a, b json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
