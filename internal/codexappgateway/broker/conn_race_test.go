package broker

import (
	"encoding/json"
	"testing"
)

// TestDeliverTurn_BuffersWhenNoPendingReceiver pins the race fix for
// the case where the broker's readLoop dispatches turn/completed
// before Turn() has acquired the mutex to register pendingTurns.
// Without the buffer, the payload is silently dropped and Turn blocks
// until brokerTimeout. The bug intermittently broke prod tests at
// CI's typical scheduling (e.g. TestPoolReusesConnForSameWorkspace),
// surfacing as "ack received, no completion, 5-min timeout".
func TestDeliverTurn_BuffersWhenNoPendingReceiver(t *testing.T) {
	c := &Conn{
		pendingResp:    make(map[int64]chan rpcResponse),
		pendingTurns:   make(map[string]chan turnPayload),
		itemsByTurn:    make(map[string][]json.RawMessage),
		completedTurns: make(map[string]turnPayload),
		closed:         make(chan struct{}),
	}

	// Simulate readLoop receiving turn/completed before Turn() registers.
	want := turnPayload{Raw: json.RawMessage(`{"id":"trn-x","status":"completed"}`)}
	c.deliverTurn("trn-x", want)

	c.mu.Lock()
	got, ok := c.completedTurns["trn-x"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("deliverTurn dropped payload silently when no pendingTurns entry — race fix regressed")
	}
	if string(got.Raw) != string(want.Raw) {
		t.Errorf("buffered payload differs: got %s want %s", got.Raw, want.Raw)
	}
}

// TestDeliverTurn_MergesBufferedItemsBeforeBuffering verifies that when
// deliverTurn buffers a payload (race path), it still folds in the
// item/completed notifications that arrived earlier — same invariant
// the normal delivery path holds. Without this, a Turn that hits the
// buffer path would return Turn.items=[] even when items were streamed.
func TestDeliverTurn_MergesBufferedItemsBeforeBuffering(t *testing.T) {
	c := &Conn{
		pendingResp:    make(map[int64]chan rpcResponse),
		pendingTurns:   make(map[string]chan turnPayload),
		itemsByTurn:    make(map[string][]json.RawMessage),
		completedTurns: make(map[string]turnPayload),
		closed:         make(chan struct{}),
	}
	// One streamed item.
	c.itemsByTurn["trn-x"] = []json.RawMessage{
		json.RawMessage(`{"type":"agentMessage","id":"m1","text":"hi"}`),
	}
	// No pending receiver — falls through to buffer path.
	c.deliverTurn("trn-x", turnPayload{Raw: json.RawMessage(`{"id":"trn-x","items":[]}`)})

	c.mu.Lock()
	got, ok := c.completedTurns["trn-x"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("expected payload in completedTurns")
	}
	// Items field should now contain the streamed agentMessage.
	var decoded struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(got.Raw, &decoded); err != nil {
		t.Fatalf("decode buffered payload: %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0]["text"] != "hi" {
		t.Errorf("expected merged items=[{agentMessage hi}], got %v", decoded.Items)
	}
}
