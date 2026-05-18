package relay

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistry_CreateAndLookup(t *testing.T) {
	r := NewRegistry(8, time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws_1",
		SourceExeID: "exe_a",
		DestExeID:   "exe_b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(rel.Ticket, "rly_") {
		t.Errorf("ticket prefix: got %q", rel.Ticket)
	}
	got, ok := r.Lookup(rel.Ticket)
	if !ok || got != rel {
		t.Errorf("Lookup: ok=%v got=%v want=%v", ok, got, rel)
	}
}

func TestRegistry_WorkspaceCap(t *testing.T) {
	r := NewRegistry(2, 5*time.Second, nil)
	for i := 0; i < 2; i++ {
		if _, err := r.Create(CreateOptions{
			WorkspaceID: "ws_x", SourceExeID: "a", DestExeID: "b",
		}); err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}
	_, err := r.Create(CreateOptions{
		WorkspaceID: "ws_x", SourceExeID: "a", DestExeID: "b",
	})
	if err != ErrWorkspaceCapReached {
		t.Errorf("want ErrWorkspaceCapReached, got %v", err)
	}
}

func TestRelay_PutFirstThenGet(t *testing.T) {
	r := NewRegistry(8, time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("hello, relay world! " + strings.Repeat("x", 4096))

	var putStatus int
	var putBody []byte
	putDone := make(chan struct{})
	go func() {
		putStatus, putBody = rel.AcceptPut(bytes.NewReader(payload))
		close(putDone)
	}()

	// Give PUT a moment to claim, then GET.
	time.Sleep(10 * time.Millisecond)
	w := httptest.NewRecorder()
	rel.AcceptGet(w)

	<-putDone

	if putStatus != 200 {
		t.Errorf("put status = %d, want 200; body=%s", putStatus, putBody)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Errorf("get body mismatch: got %d bytes, want %d", w.Body.Len(), len(payload))
	}
	if !strings.Contains(string(putBody), `"status":"ok"`) {
		t.Errorf("put body lacks ok status: %s", putBody)
	}
}

func TestRelay_GetFirstThenPut(t *testing.T) {
	r := NewRegistry(8, time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("get-first payload " + strings.Repeat("y", 1024))

	getDone := make(chan struct{})
	w := httptest.NewRecorder()
	go func() {
		rel.AcceptGet(w)
		close(getDone)
	}()

	time.Sleep(10 * time.Millisecond)
	status, body := rel.AcceptPut(bytes.NewReader(payload))

	<-getDone

	if status != 200 {
		t.Errorf("put status = %d, want 200; body=%s", status, body)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Errorf("body mismatch: got %d bytes", w.Body.Len())
	}
}

func TestRelay_TTLExpiry(t *testing.T) {
	r := NewRegistry(8, 80*time.Millisecond, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, body := rel.AcceptPut(strings.NewReader("never sent"))
	if status != 408 {
		t.Errorf("status = %d, want 408 timeout; body=%s", status, body)
	}
	// Done channel closed.
	select {
	case <-rel.Done():
	case <-time.After(time.Second):
		t.Fatal("relay never reported done after ttl")
	}
	if rel.Err() != ErrTimeout {
		t.Errorf("err = %v, want ErrTimeout", rel.Err())
	}
}

func TestRelay_DoublePutLocked(t *testing.T) {
	r := NewRegistry(8, time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First PUT claims, blocks waiting for GET.
	go func() {
		// drain — we don't care about the result; rely on test teardown
		// when GET arrives or ttl expires.
		_, _ = rel.AcceptPut(strings.NewReader("first"))
	}()

	// Wait for claim to register.
	time.Sleep(20 * time.Millisecond)

	// Second PUT must get 423.
	status, body := rel.AcceptPut(strings.NewReader("second"))
	if status != 423 {
		t.Errorf("second PUT status = %d, want 423; body=%s", status, body)
	}

	// Cleanup: let GET arrive so the first PUT can finish.
	w := httptest.NewRecorder()
	rel.AcceptGet(w)
	<-rel.Done()
}

func TestRelay_DoubleGetLocked(t *testing.T) {
	r := NewRegistry(8, time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		w := httptest.NewRecorder()
		_, _ = rel.AcceptGet(w)
	}()
	time.Sleep(20 * time.Millisecond)

	w := httptest.NewRecorder()
	status, body := rel.AcceptGet(w)
	if status != 423 {
		t.Errorf("second GET status = %d, want 423; body=%s", status, body)
	}

	rel.AcceptPut(strings.NewReader("done"))
	<-rel.Done()
}

func TestRelay_Cleanup(t *testing.T) {
	r := NewRegistry(8, 50*time.Millisecond, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws_clean", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.ActiveCount(); got != 1 {
		t.Errorf("active before = %d, want 1", got)
	}
	// Wait for ttl + cleanup.
	select {
	case <-rel.Done():
	case <-time.After(time.Second):
		t.Fatal("relay never finished")
	}
	// onDone may be called slightly after close(done); give it a tick.
	time.Sleep(20 * time.Millisecond)
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("active after = %d, want 0", got)
	}
	if _, ok := r.Lookup(rel.Ticket); ok {
		t.Error("ticket still in registry after cleanup")
	}
}

func TestRelay_LargePayloadConcurrent(t *testing.T) {
	r := NewRegistry(8, 2*time.Second, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	const size = 2 * 1024 * 1024
	payload := bytes.Repeat([]byte{0xAB}, size)

	var wg sync.WaitGroup
	var putStatus int
	var getBody []byte
	wg.Add(2)
	go func() {
		defer wg.Done()
		putStatus, _ = rel.AcceptPut(bytes.NewReader(payload))
	}()
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		rel.AcceptGet(w)
		getBody = w.Body.Bytes()
	}()
	wg.Wait()

	if putStatus != 200 {
		t.Errorf("status = %d, want 200", putStatus)
	}
	if !bytes.Equal(getBody, payload) {
		t.Errorf("body mismatch: got %d bytes, want %d", len(getBody), size)
	}
	if rel.Bytes() != size {
		t.Errorf("bytes = %d, want %d", rel.Bytes(), size)
	}
}

func TestExtractBearerTicket(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Bearer rly_abc", "rly_abc", true},
		{"bearer rly_abc", "", false},
		{"Bearer ", "", false},
		{"", "", false},
		{"Token rly_abc", "", false},
	}
	for _, c := range cases {
		got, ok := ExtractBearerTicket(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Extract(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Verify the relay returns 410 if AcceptPut arrives after the relay
// has already finished cleanup.
func TestRelay_LateArrival(t *testing.T) {
	r := NewRegistry(8, 30*time.Millisecond, nil)
	rel, err := r.Create(CreateOptions{
		WorkspaceID: "ws", SourceExeID: "a", DestExeID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Let it time out.
	<-rel.Done()

	status, _ := rel.AcceptPut(strings.NewReader("x"))
	if status != 410 {
		t.Errorf("late PUT status = %d, want 410 Gone", status)
	}
}

// Sanity-check flushingWriter handles nil flusher.
func TestFlushingWriter_NilFlusher(t *testing.T) {
	var buf bytes.Buffer
	fw := flushingWriter{w: &buf, f: nil}
	n, err := io.Copy(fw, strings.NewReader("hi"))
	if err != nil || n != 2 || buf.String() != "hi" {
		t.Errorf("flushingWriter nil flusher: n=%d err=%v buf=%q", n, err, buf.String())
	}
}
