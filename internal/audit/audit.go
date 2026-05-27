// Package audit provides an async session-level audit logger.
//
// Service.Log enqueues an event on a buffered channel; a background worker
// flushes to PostgreSQL. The request path never blocks on insert — if the
// buffer overflows we log + drop rather than 500 the user action.
package audit

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// Service is the audit pipeline. Construct with NewService, defer Shutdown.
type Service struct {
	db   *db.DB
	ch   chan db.AuditEvent
	quit chan struct{}
	wg   sync.WaitGroup
}

// NewService starts the background worker. bufferSize=0 picks 1000.
func NewService(database *db.DB, bufferSize int) *Service {
	if bufferSize <= 0 {
		bufferSize = 1000
	}
	s := &Service{
		db:   database,
		ch:   make(chan db.AuditEvent, bufferSize),
		quit: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.worker()
	return s
}

// Log enqueues an event. Reads userID + activeWorkspaceID from ctx (set by
// auth.Middleware). Non-blocking — drops + logs on overflow.
//
// Pass details=nil for minimal events; map keys go into JSONB unchanged.
func (s *Service) Log(ctx context.Context, eventType string, details map[string]any) {
	if s == nil {
		return
	}
	e := db.AuditEvent{
		UserID:      auth.UserIDFromContext(ctx),
		WorkspaceID: auth.ActiveWorkspaceFromContext(ctx),
		EventType:   eventType,
		Details:     details,
	}
	s.enqueue(e)
}

// LogRequest is like Log but stamps an HTTP request snapshot (method, path,
// status, ip, ua). Useful from middleware.
func (s *Service) LogRequest(ctx context.Context, eventType string, method, path, ip, userAgent string, status int, details map[string]any) {
	if s == nil {
		return
	}
	e := db.AuditEvent{
		UserID:         auth.UserIDFromContext(ctx),
		WorkspaceID:    auth.ActiveWorkspaceFromContext(ctx),
		EventType:      eventType,
		Details:        details,
		RequestMethod:  method,
		RequestPath:    path,
		ResponseStatus: status,
		IP:             ip,
		UserAgent:      userAgent,
	}
	s.enqueue(e)
}

// LogAnonymous bypasses ctx (for events fired before auth context is set,
// e.g., a 401 login attempt). Caller fills userID/workspaceID directly.
func (s *Service) LogAnonymous(eventType string, e db.AuditEvent) {
	if s == nil {
		return
	}
	e.EventType = eventType
	s.enqueue(e)
}

func (s *Service) enqueue(e db.AuditEvent) {
	select {
	case s.ch <- e:
	default:
		log.Printf("audit: channel full (size=%d), dropping event %s", cap(s.ch), e.EventType)
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case e := <-s.ch:
			if err := s.db.InsertAuditEvent(e); err != nil {
				log.Printf("audit: insert failed for %s: %v", e.EventType, err)
			}
		case <-s.quit:
			// Drain remaining buffered events on graceful shutdown.
			for {
				select {
				case e := <-s.ch:
					if err := s.db.InsertAuditEvent(e); err != nil {
						log.Printf("audit: drain insert failed for %s: %v", e.EventType, err)
					}
				default:
					return
				}
			}
		}
	}
}

// Shutdown closes the quit channel and waits for the worker to drain.
// Bound the wait with maxWait to avoid hanging on a stuck DB.
func (s *Service) Shutdown(maxWait time.Duration) {
	if s == nil {
		return
	}
	close(s.quit)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(maxWait):
		log.Printf("audit: shutdown timed out after %s; some events may be lost", maxWait)
	}
}
