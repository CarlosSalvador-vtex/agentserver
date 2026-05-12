package codexexecgateway

import (
	"sync"

	"nhooyr.io/websocket"
)

// ConnRegistry tracks the single live inbound /codex-exec/{exe_id} ws conn
// per exe_id. Re-registering an exe_id evicts the prior connection.
type ConnRegistry struct {
	mu           sync.Mutex
	conns        map[string]*websocket.Conn
	bridgeLocked map[string]bool
}

func NewConnRegistry() *ConnRegistry {
	return &ConnRegistry{
		conns:        make(map[string]*websocket.Conn),
		bridgeLocked: make(map[string]bool),
	}
}

// Register inserts conn for exeID. If a previous conn was registered
// for the same exeID, returns it as evicted; the caller MUST close
// the evicted conn — failing to do so leaks the prior handler's
// goroutine (which is blocked in ws.Read on the now-orphaned conn).
//
// Register also clears any bridge lock for exeID: the prior bridge
// session's inbound conn is being replaced, so continuing to pump from
// the old conn would be incorrect.
func (r *ConnRegistry) Register(exeID string, c *websocket.Conn) (evicted *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.conns[exeID]
	r.conns[exeID] = c
	// Any prior bridge session is invalidated by the inbound conn replacement.
	delete(r.bridgeLocked, exeID)
	if prev != nil && prev != c {
		return prev
	}
	return nil
}

// AcquireBridge marks the inbound conn for exeID as having an active bridge
// session. Returns false if another bridge session already holds it; the
// caller should reject the new request with HTTP 409 Conflict in that case.
// Caller MUST call ReleaseBridge when done.
func (r *ConnRegistry) AcquireBridge(exeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bridgeLocked[exeID] {
		return false
	}
	r.bridgeLocked[exeID] = true
	return true
}

// ReleaseBridge releases the bridge lock for exeID. Idempotent.
func (r *ConnRegistry) ReleaseBridge(exeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bridgeLocked, exeID)
}

// Lookup returns the registered conn for `exeID`, if any.
func (r *ConnRegistry) Lookup(exeID string) (*websocket.Conn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.conns[exeID]
	return c, ok
}

// Unregister removes `exeID` only if its current value is `c`. This guards
// against a goroutine for an old conn deleting a new conn after eviction.
func (r *ConnRegistry) Unregister(exeID string, c *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[exeID] == c {
		delete(r.conns, exeID)
	}
}

// ConnectedIDs returns a snapshot of currently registered exe_ids.
func (r *ConnRegistry) ConnectedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.conns))
	for id := range r.conns {
		out = append(out, id)
	}
	return out
}
