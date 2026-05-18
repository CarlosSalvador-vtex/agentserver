// Package relay implements an in-memory ticket-based HTTPS byte relay
// for codex-exec-gateway. env-mcp's copy_path tool mints a ticket and
// then dispatches `curl PUT` on the source executor and `curl GET` on
// the destination — bytes flow directly between executor and gateway
// without going through env-mcp's WS protocol path.
//
// Per spec: docs/superpowers/specs/2026-05-18-copy-path-http-relay.md
package relay

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Errors surfaced by Registry / Relay.
var (
	ErrTicketNotFound      = errors.New("relay: ticket not found or expired")
	ErrTicketAlreadyClaim  = errors.New("relay: that side of the ticket is already claimed")
	ErrWorkspaceCapReached = errors.New("relay: workspace concurrent relay cap reached")
	ErrTimeout             = errors.New("relay: timed out waiting for the other side")
)

// Defaults; can be overridden via NewRegistryOptions.
const (
	DefaultRelayTTL          = 5 * time.Minute
	DefaultRelayMaxPerWorkspace = 16
	pairingTickInterval      = 50 * time.Millisecond
)

// putReq is a PUT-side waiting to be paired.
type putReq struct {
	body io.Reader
	// statusOut is the HTTP status to send back when the relay finishes
	// (200 on success, 500 on copy error, etc.). bodyOut is the JSON
	// stats body. done signals the handler to write + return.
	statusOut int
	bodyOut   []byte
	done      chan struct{}
}

// getReq is a GET-side waiting to be paired.
type getReq struct {
	writer  io.Writer
	flusher http.Flusher
	// done signals "stream complete, you can return". The handler has
	// already written the streamed body via the flushingWriter that
	// run() built.
	done chan struct{}
}

// Relay is one in-flight or pending byte-pump session, keyed by ticket.
type Relay struct {
	Ticket      string
	WorkspaceID string
	SourceExeID string
	DestExeID   string
	MaxBytes    int64
	ExpiresAt   time.Time

	putCh chan *putReq
	getCh chan *getReq

	// Set by run() before close(done).
	bytes int64
	err   error
	done  chan struct{}

	// Set when one side has claimed; protects against duplicate
	// PUT / GET claims (returns 423 to the second).
	mu        sync.Mutex
	putClaim  bool
	getClaim  bool

	logger *slog.Logger
}

// Bytes reports the number transferred (only valid after Done fires).
func (r *Relay) Bytes() int64 { return r.bytes }

// Err reports the io.Copy error, if any (valid after Done).
func (r *Relay) Err() error { return r.err }

// Done is closed once the relay finishes (success or error).
func (r *Relay) Done() <-chan struct{} { return r.done }

// AcceptPut blocks until the pairing goroutine is ready to pull bytes
// from this PUT side, the GET side fails to show up, or ttl elapses.
// On return, the PUT handler should write `status` + `body` to the
// HTTP response.
//
// 423 Locked is returned if a PUT has already claimed this ticket.
func (r *Relay) AcceptPut(reader io.Reader) (status int, body []byte) {
	r.mu.Lock()
	if r.putClaim {
		r.mu.Unlock()
		return http.StatusLocked, []byte(`{"error":"PUT side already claimed"}`)
	}
	r.putClaim = true
	r.mu.Unlock()

	// Fast-fail if pairing goroutine has already exited (e.g. ttl
	// timed out): otherwise the select below could randomly pick the
	// chan-send case over <-r.done, leaving us blocked on req.done
	// forever (nobody is reading putCh anymore).
	select {
	case <-r.done:
		return http.StatusGone, []byte(`{"error":"relay already finished"}`)
	default:
	}
	req := &putReq{body: reader, done: make(chan struct{})}
	select {
	case r.putCh <- req:
	case <-r.done:
		return http.StatusGone, []byte(`{"error":"relay already finished"}`)
	}
	<-req.done
	return req.statusOut, req.bodyOut
}

// AcceptGet is the GET-side equivalent. The GET handler should NOT
// write to w after this returns — bytes are streamed via w during
// the call.
func (r *Relay) AcceptGet(w http.ResponseWriter) (status int, body []byte) {
	r.mu.Lock()
	if r.getClaim {
		r.mu.Unlock()
		return http.StatusLocked, []byte(`{"error":"GET side already claimed"}`)
	}
	r.getClaim = true
	r.mu.Unlock()

	select {
	case <-r.done:
		return http.StatusGone, []byte(`{"error":"relay already finished"}`)
	default:
	}
	flusher, _ := w.(http.Flusher) // okay if nil; flushingWriter handles it
	req := &getReq{writer: w, flusher: flusher, done: make(chan struct{})}
	select {
	case r.getCh <- req:
	case <-r.done:
		return http.StatusGone, []byte(`{"error":"relay already finished"}`)
	}
	<-req.done
	// On done: status header + any body bytes were already written to
	// w by the pairing goroutine via the streamed Write+Flush calls.
	// We return 200 to indicate the caller doesn't need to send any
	// further body (the handler will skip writing anything if status==0).
	return 0, nil
}

// run is the per-ticket pairing goroutine spawned by Registry.Create.
// It waits for both sides, runs io.Copy, then notifies both sides.
func (r *Relay) run(ttl time.Duration, onDone func(ticket string)) {
	defer close(r.done)
	defer onDone(r.Ticket)

	deadline := time.NewTimer(ttl)
	defer deadline.Stop()

	var put *putReq
	var get *getReq

	// Loop until both sides arrive or ttl fires.
	for put == nil || get == nil {
		select {
		case p := <-r.putCh:
			put = p
		case g := <-r.getCh:
			get = g
		case <-deadline.C:
			// Timeout — fail any side that already showed up.
			if put != nil {
				put.statusOut = http.StatusRequestTimeout
				put.bodyOut = []byte(`{"error":"timed out waiting for GET side"}`)
				close(put.done)
			}
			if get != nil {
				// GET hasn't written anything yet; let the handler emit a 408.
				close(get.done) // handler will see status==0; we use a side channel
			}
			r.err = ErrTimeout
			return
		}
	}

	// Both sides arrived. Stream.
	dst := flushingWriter{w: get.writer, f: get.flusher}
	src := put.body
	if r.MaxBytes > 0 {
		src = io.LimitReader(src, r.MaxBytes+1) // +1 so we can detect overflow
	}
	n, err := io.Copy(dst, src)
	r.bytes = n
	if err == nil && r.MaxBytes > 0 && n > r.MaxBytes {
		err = fmt.Errorf("relay: payload exceeded MaxBytes (%d > %d)", n, r.MaxBytes)
	}
	r.err = err

	// Finish GET side: close the response stream by returning from the
	// handler. The handler is waiting on get.done.
	close(get.done)

	// Finish PUT side: respond with 200 + stats (or 500 + error).
	if err == nil {
		put.statusOut = http.StatusOK
		put.bodyOut = []byte(fmt.Sprintf(`{"bytes":%d,"status":"ok"}`, n))
	} else {
		put.statusOut = http.StatusInternalServerError
		put.bodyOut = []byte(fmt.Sprintf(`{"bytes":%d,"error":%q}`, n, err.Error()))
	}
	close(put.done)
}

// flushingWriter calls Flush after each Write so the GET side streams
// out chunked rather than buffering until the response body is fully
// formed.
type flushingWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushingWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

// Registry is the in-memory map of all live relay tickets, plus a
// per-workspace concurrent-count tracker for the cap.
type Registry struct {
	mu sync.Mutex
	// tickets is map[ticket] → *Relay.
	tickets map[string]*Relay
	// workspaceCount is map[workspace_id] → count of active relays.
	workspaceCount map[string]int

	maxPerWorkspace int
	defaultTTL      time.Duration
	logger          *slog.Logger
}

// NewRegistry constructs a Registry. maxPerWorkspace and defaultTTL
// default to package constants when 0.
func NewRegistry(maxPerWorkspace int, defaultTTL time.Duration, logger *slog.Logger) *Registry {
	if maxPerWorkspace <= 0 {
		maxPerWorkspace = DefaultRelayMaxPerWorkspace
	}
	if defaultTTL <= 0 {
		defaultTTL = DefaultRelayTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		tickets:         map[string]*Relay{},
		workspaceCount:  map[string]int{},
		maxPerWorkspace: maxPerWorkspace,
		defaultTTL:      defaultTTL,
		logger:          logger,
	}
}

// CreateOptions controls the per-Relay knobs at mint time.
type CreateOptions struct {
	WorkspaceID string
	SourceExeID string
	DestExeID   string
	TTL         time.Duration // 0 = registry default
	MaxBytes    int64         // 0 = unlimited
}

// Create mints a ticket and starts the pairing goroutine.
func (r *Registry) Create(opt CreateOptions) (*Relay, error) {
	if opt.WorkspaceID == "" || opt.SourceExeID == "" || opt.DestExeID == "" {
		return nil, errors.New("relay: workspace_id, source_exe_id, dest_exe_id required")
	}
	ttl := opt.TTL
	if ttl <= 0 {
		ttl = r.defaultTTL
	}

	r.mu.Lock()
	if r.workspaceCount[opt.WorkspaceID] >= r.maxPerWorkspace {
		r.mu.Unlock()
		return nil, ErrWorkspaceCapReached
	}
	ticket, err := mintTicket()
	if err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("relay: mint ticket: %w", err)
	}
	relay := &Relay{
		Ticket:      ticket,
		WorkspaceID: opt.WorkspaceID,
		SourceExeID: opt.SourceExeID,
		DestExeID:   opt.DestExeID,
		MaxBytes:    opt.MaxBytes,
		ExpiresAt:   time.Now().Add(ttl),
		putCh:       make(chan *putReq, 1),
		getCh:       make(chan *getReq, 1),
		done:        make(chan struct{}),
		logger:      r.logger.With("ticket", ticket, "workspace_id", opt.WorkspaceID),
	}
	r.tickets[ticket] = relay
	r.workspaceCount[opt.WorkspaceID]++
	r.mu.Unlock()

	go relay.run(ttl, r.cleanup)
	relay.logger.Info("relay: created",
		"source_exe_id", opt.SourceExeID, "dest_exe_id", opt.DestExeID,
		"ttl", ttl, "max_bytes", opt.MaxBytes)
	return relay, nil
}

// Lookup returns the relay for the ticket, or false if expired/unknown.
// Returns false but doesn't garbage-collect — the pairing goroutine
// owns deletion via the onDone callback.
func (r *Registry) Lookup(ticket string) (*Relay, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	relay, ok := r.tickets[ticket]
	return relay, ok
}

// cleanup is the callback the pairing goroutine fires when it's done.
func (r *Registry) cleanup(ticket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	relay, ok := r.tickets[ticket]
	if !ok {
		return
	}
	delete(r.tickets, ticket)
	if relay.WorkspaceID != "" {
		r.workspaceCount[relay.WorkspaceID]--
		if r.workspaceCount[relay.WorkspaceID] <= 0 {
			delete(r.workspaceCount, relay.WorkspaceID)
		}
	}
	relay.logger.Info("relay: cleaned up", "bytes", relay.bytes, "err", relay.err)
}

// ActiveCount reports how many relays are in-flight (any state).
// Mostly for metrics / smoke tests.
func (r *Registry) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tickets)
}

// mintTicket returns a URL-safe "rly_<base64(32 random bytes)>".
func mintTicket() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	s := base64.RawURLEncoding.EncodeToString(b[:])
	return "rly_" + s, nil
}

// ExtractBearerTicket pulls the ticket out of "Authorization: Bearer <ticket>".
// Returns ("", false) if absent or malformed.
func ExtractBearerTicket(authz string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return "", false
	}
	tok := strings.TrimPrefix(authz, prefix)
	if tok == "" {
		return "", false
	}
	return tok, true
}
