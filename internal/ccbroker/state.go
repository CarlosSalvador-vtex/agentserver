package ccbroker

import "sync"

// activeTurnEntry tracks one in-flight turn per session.
type activeTurnEntry struct {
	TurnID string
	Cancel func()
}

// activeTurnRegistry maps session_id → active turn metadata. Used by:
// - handler_turns: register on entry, clear on exit
// - handler_tui_routes: cancel by (sid, tid)
// - leak worker: query "is this turn still active?"
type activeTurnRegistry struct {
	mu sync.Mutex
	m  map[string]activeTurnEntry
}

func newActiveTurnRegistry() *activeTurnRegistry {
	return &activeTurnRegistry{m: map[string]activeTurnEntry{}}
}

// Set registers (or replaces) the active turn for sid.
func (r *activeTurnRegistry) Set(sid, tid string, cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sid] = activeTurnEntry{TurnID: tid, Cancel: cancel}
}

// Get returns the active turn ID for sid, or "" if none.
func (r *activeTurnRegistry) Get(sid string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[sid]
	return e.TurnID, ok
}

// Cancel cancels the active turn for sid, but only if it matches tid.
// Returns true if a matching turn was found and cancelled.
func (r *activeTurnRegistry) Cancel(sid, tid string) bool {
	r.mu.Lock()
	e, ok := r.m[sid]
	r.mu.Unlock()
	if !ok || e.TurnID != tid {
		return false
	}
	if e.Cancel != nil {
		e.Cancel()
	}
	return true
}

// Clear removes the active-turn entry for sid only if it still matches tid.
// Used by handler_turns at end-of-turn (defer) to avoid clobbering a fresh
// turn with the cleanup of a stale one.
func (r *activeTurnRegistry) Clear(sid, tid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.m[sid]; ok && e.TurnID == tid {
		delete(r.m, sid)
	}
}

// compactQueue is a session-level set: "compact on next turn".
// Set by handleCompactNow, consumed (Take) by handler_turns at next-turn entry.
type compactQueue struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func newCompactQueue() *compactQueue {
	return &compactQueue{set: map[string]struct{}{}}
}

func (c *compactQueue) Set(sid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set[sid] = struct{}{}
}

// IsSet reports whether sid is queued (without consuming).
func (c *compactQueue) IsSet(sid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.set[sid]
	return ok
}

// Take removes sid from the queue and returns whether it was set.
// Used by handler_turns at next-turn entry to convert "queued" → "this turn does it".
func (c *compactQueue) Take(sid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.set[sid]; ok {
		delete(c.set, sid)
		return true
	}
	return false
}
