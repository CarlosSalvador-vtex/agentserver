package processes

import (
	"sync"
	"time"
)

// Manager owns the in-process session table. Call Run() at startup
// to spawn the background goroutine that calls Sweep() every minute;
// sessions inactive for IdleTimeout are dropped (the SDK polling stops
// returning their output and the session ID goes 404).
type Manager struct {
	IdleTimeout time.Duration
	mu          sync.RWMutex
	sessions    map[string]*Session
	stop        chan struct{}
}

func NewManager(idleTimeout time.Duration) *Manager {
	return &Manager{
		IdleTimeout: idleTimeout,
		sessions:    map[string]*Session{},
		stop:        make(chan struct{}),
	}
}

func (m *Manager) Register(s *Session) {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) Forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Sweep removes sessions whose lastActivity is older than IdleTimeout.
// Call from a background goroutine via Run() or invoke directly in
// tests.
func (m *Manager) Sweep() {
	cutoff := time.Now().Add(-m.IdleTimeout)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.LastActivity().Before(cutoff) {
			delete(m.sessions, id)
		}
	}
}

// Run starts a goroutine that calls Sweep every minute until Stop().
func (m *Manager) Run() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				m.Sweep()
			case <-m.stop:
				return
			}
		}
	}()
}

func (m *Manager) Stop() { close(m.stop) }
