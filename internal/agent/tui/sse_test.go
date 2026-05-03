package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSE_ReceivesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "id: %d\nevent: tool_use\ndata: {\"n\":%d}\n\n", i, i)
			f.Flush()
		}
	}))
	defer srv.Close()
	bus := NewBus(BusConfig{ServerURL: srv.URL, ExecutorID: "exe_a", WorkspaceID: "ws", Auth: &fakeAuth{tk: "t"}})
	sub := NewSSEConsumer(bus, SSEConfig{SessionID: "cse_x"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch := sub.Run(ctx)

	received := []SSEEvent{}
	deadline := time.After(time.Second)
loop:
	for len(received) < 3 {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			received = append(received, ev)
		case <-deadline:
			break loop
		}
	}
	if len(received) != 3 {
		t.Fatalf("got %d events", len(received))
	}
	if received[2].Type != "tool_use" || received[2].LastEventID != "3" {
		t.Errorf("event[2] = %+v", received[2])
	}
}

func TestSSE_SendsLastEventIDOnReconnect(t *testing.T) {
	var hits atomic.Int32
	var lastID atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		lastID.Store(r.Header.Get("Last-Event-ID"))
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		if n == 1 {
			fmt.Fprintf(w, "id: 7\nevent: x\ndata: {}\n\n")
			f.Flush()
			return
		}
		fmt.Fprintf(w, "id: 8\nevent: x\ndata: {}\n\n")
		f.Flush()
	}))
	defer srv.Close()
	bus := NewBus(BusConfig{ServerURL: srv.URL, ExecutorID: "e", WorkspaceID: "w", Auth: &fakeAuth{tk: "t"}})
	sub := NewSSEConsumer(bus, SSEConfig{
		SessionID:      "cse",
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch := sub.Run(ctx)

	var got []SSEEvent
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case ev, ok := <-ch:
			if !ok {
				break
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("got %d events", len(got))
		}
	}
	if v, _ := lastID.Load().(string); v != "7" {
		t.Errorf("Last-Event-ID on reconnect = %q want 7", v)
	}
}

func TestSSE_IgnoresKeepalivesAndComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		fmt.Fprintf(w, ": keepalive\n\n")
		f.Flush()
		fmt.Fprintf(w, "id: 1\nevent: real\ndata: {\"k\":1}\n\n")
		f.Flush()
	}))
	defer srv.Close()
	bus := NewBus(BusConfig{ServerURL: srv.URL, ExecutorID: "e", WorkspaceID: "w", Auth: &fakeAuth{tk: "t"}})
	sub := NewSSEConsumer(bus, SSEConfig{SessionID: "cse"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch := sub.Run(ctx)
	select {
	case ev := <-ch:
		if ev.Type != "real" {
			t.Errorf("got event %q want real (keepalive comment should be ignored)", ev.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no event received")
	}
}
