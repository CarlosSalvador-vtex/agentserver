package runner

import (
	"encoding/json"

	agentsdk "github.com/agentserver/claude-agent-sdk-go"
)

// Event is the cc-broker-side projection of an SDK message ready to be
// inserted into agent_session_events and broadcast over SSE.
type Event struct {
	EventType string          // canonical short tag for our own queries
	Payload   json.RawMessage // verbatim SDK message JSON
	Ephemeral bool            // true = SSE-only, do not persist
}

// ToEventPayload classifies an SDKMessage. The raw JSON is preserved as
// payload so frontend consumers and audit replay see exactly what the SDK
// produced. The EventType field is our internal tag — useful for indexed
// queries — and is intentionally a small enumeration so new SDK message
// types fall through to a generic "sdk_event" without breaking callers.
func ToEventPayload(msg agentsdk.SDKMessage) (Event, error) {
	if len(msg.Raw) == 0 {
		// Defensive: SDK should always populate Raw, but tolerate empty
		// by emitting a minimal envelope so downstream INSERT does not panic.
		raw, err := json.Marshal(map[string]string{"type": msg.Type, "subtype": msg.Subtype})
		if err != nil {
			return Event{}, err
		}
		msg.Raw = raw
	}
	tag, ephemeral := classify(msg.Type, msg.Subtype)
	return Event{EventType: tag, Payload: msg.Raw, Ephemeral: ephemeral}, nil
}

func classify(sdkType, sdkSubtype string) (string, bool) {
	switch sdkType {
	case "user":
		return "user_message", false
	case "assistant":
		return "assistant_message", false
	case "tool_result":
		return "tool_result", false
	case "result":
		return "turn_result", false
	case "system":
		switch sdkSubtype {
		case "init":
			return "system_init", false
		case "compact_boundary":
			return "compact_boundary", false
		default:
			return "system_" + safeSubtype(sdkSubtype), false
		}
	case "stream_event":
		return "stream_event", true
	case "tool_progress":
		return "tool_progress", true
	default:
		return "sdk_event", false
	}
}

func safeSubtype(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
