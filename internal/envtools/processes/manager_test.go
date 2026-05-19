package processes

import (
	"testing"
	"time"
)

func TestManager_RegisterGet(t *testing.T) {
	m := NewManager(30 * time.Minute)
	s := &Session{ID: "sid-1", WorkspaceID: "ws-1"}
	m.Register(s)
	got, ok := m.Get("sid-1")
	if !ok || got.ID != "sid-1" {
		t.Fatalf("Get returned %+v ok=%v", got, ok)
	}
}

func TestManager_Forget(t *testing.T) {
	m := NewManager(30 * time.Minute)
	m.Register(&Session{ID: "sid-1", WorkspaceID: "ws-1"})
	m.Forget("sid-1")
	if _, ok := m.Get("sid-1"); ok {
		t.Fatal("expected Get to fail after Forget")
	}
}

func TestSession_AppendAndRead(t *testing.T) {
	s := &Session{ID: "sid", WorkspaceID: "ws"}
	s.Append("stdout", []byte("hello"))
	s.Append("stderr", []byte("world"))
	chunks, exit, alive := s.OutputSince(0)
	if len(chunks) != 2 || exit != nil || !alive {
		t.Fatalf("got chunks=%d exit=%v alive=%v", len(chunks), exit, alive)
	}
	chunks, _, _ = s.OutputSince(1)
	if len(chunks) != 1 || chunks[0].Stream != "stderr" {
		t.Fatalf("since=1 got chunks=%+v", chunks)
	}
}

func TestSession_RingBufferTruncates(t *testing.T) {
	s := &Session{ID: "sid", WorkspaceID: "ws"}
	big := make([]byte, 600_000)
	s.Append("stdout", big)
	s.Append("stdout", big) // total 1.2 MiB > 1 MiB cap
	chunks, _, _ := s.OutputSince(0)
	var total int
	for _, c := range chunks {
		total += len(c.Data)
	}
	if total > MaxBufferBytes {
		t.Errorf("buffer exceeded cap: %d > %d", total, MaxBufferBytes)
	}
	if s.LostBytes() == 0 {
		t.Error("expected LostBytes > 0 after truncation")
	}
}

func TestManager_SweepIdle(t *testing.T) {
	m := NewManager(50 * time.Millisecond)
	s := &Session{ID: "sid", WorkspaceID: "ws"}
	m.Register(s)
	time.Sleep(100 * time.Millisecond)
	m.Sweep()
	if _, ok := m.Get("sid"); ok {
		t.Fatal("expected session swept after idle timeout")
	}
}
