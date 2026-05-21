package imbridge

import (
	"sync"
	"testing"
)

// TestForwardMessageChannelRoutingOverridesBinding verifies that
// SetChannelRoutingMode's in-memory value wins over the initial
// binding.RoutingMode captured at StartPoller time. This is the
// core property that makes the toggle take effect without restarting
// the poller.
func TestForwardMessageChannelRoutingOverridesBinding(t *testing.T) {
	b := &Bridge{
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}

	// Override with codex in the in-memory map.
	b.SetChannelRoutingMode("ch-abc", "codex")

	// Simulate forwardMessage's routing decision directly. We cannot
	// invoke forwardMessage end-to-end here without a real provider /
	// HTTP target, so we assert on the effective mode computation.
	got := b.getChannelRoutingMode("ch-abc")
	if got != "codex" {
		t.Fatalf("expected in-memory routing=codex, got %q", got)
	}

	// Missing channel → empty string so forwardMessage falls back to
	// binding.RoutingMode.
	if b.getChannelRoutingMode("unknown") != "" {
		t.Fatalf("expected empty routing for unknown channel")
	}
}

// TestSetChannelRoutingModeConcurrent ensures the setter/getter
// are safe under concurrent access (mirrors SetChannelRequireMention
// concurrency assumptions).
func TestSetChannelRoutingModeConcurrent(t *testing.T) {
	b := &Bridge{
		providers:        map[string]Provider{},
		pollers:          map[string]pollerEntry{},
		registeredGroups: map[string]string{},
		channelMention:   map[string]bool{},
		channelRouting:   map[string]string{},
		typingSessions:   map[string]func(){},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.SetChannelRoutingMode("ch1", "codex")
		}()
		go func() {
			defer wg.Done()
			_ = b.getChannelRoutingMode("ch1")
		}()
	}
	wg.Wait()

	if b.getChannelRoutingMode("ch1") != "codex" {
		t.Fatalf("expected codex after concurrent writes")
	}
}
