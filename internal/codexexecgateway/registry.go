package codexexecgateway

import (
	"sync"
)

// ConnRegistry tracks the single live inbound /codex-exec/{exe_id}
// connection per exe_id. Each entry is wrapped in an *inboundConn that
// can host multiple concurrent /bridge sessions multiplexed by
// stream_id (per the 2026-05-17 redesign).
//
// Re-registering an exe_id evicts the prior inboundConn; the caller
// MUST call close() on the evicted conn so its routes get fanned out
// and the underlying ws closes.
type ConnRegistry struct {
	mu    sync.Mutex
	conns map[string]*inboundConn
}

func NewConnRegistry() *ConnRegistry {
	return &ConnRegistry{conns: make(map[string]*inboundConn)}
}

// Register inserts ic for exeID. If a previous inboundConn was
// registered for the same exeID, returns it as evicted. The caller
// MUST close the evicted inbound; failing to do so leaks its reader
// goroutine and orphans its bridge sessions.
func (r *ConnRegistry) Register(exeID string, ic *inboundConn) (evicted *inboundConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.conns[exeID]
	r.conns[exeID] = ic
	if prev != nil && prev != ic {
		return prev
	}
	return nil
}

// Lookup returns the registered inbound for exeID, if any.
func (r *ConnRegistry) Lookup(exeID string) (*inboundConn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ic, ok := r.conns[exeID]
	return ic, ok
}

// Unregister removes exeID only if its current value is ic. This
// guards against a goroutine for an old inbound deleting a freshly
// registered one after eviction.
func (r *ConnRegistry) Unregister(exeID string, ic *inboundConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[exeID] == ic {
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
