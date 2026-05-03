package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleTUIEventStream(w http.ResponseWriter, r *http.Request) {
	if authUserID(r) == "" {
		writeAPIErr(w, http.StatusUnauthorized, "unauthorized", "no authenticated user")
		return
	}
	sid := chi.URLParam(r, "sid")
	sess, err := s.DB.GetAgentSession(sid)
	if err != nil || sess == nil {
		writeAPIErr(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Replay backlog if requested.
	sinceSeq, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if hdr := r.Header.Get("Last-Event-ID"); hdr != "" {
		if v, err := strconv.ParseInt(hdr, 10, 64); err == nil {
			sinceSeq = v
		}
	}
	if sinceSeq > 0 {
		events, _ := s.DB.GetAgentSessionEventsSince(sid, sinceSeq, 500)
		for _, ev := range events {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
				ev.ID, ev.EventType, ev.Payload)
		}
		flusher.Flush()
	} else if tail, _ := strconv.Atoi(r.URL.Query().Get("tail")); tail > 0 {
		events, _ := s.DB.GetAgentSessionEventsTail(sid, tail)
		for _, ev := range events {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
				ev.ID, ev.EventType, ev.Payload)
		}
		flusher.Flush()
	}

	// Subscribe to live broker.
	if s.BridgeHandler == nil || s.BridgeHandler.SSE == nil {
		// Without a bridge, we can only serve replay (already done above).
		// Hold the connection open briefly so the client doesn't tight-loop.
		time.Sleep(time.Second)
		return
	}
	sub := s.BridgeHandler.SSE.Subscribe(sid)
	defer s.BridgeHandler.SSE.Unsubscribe(sid, sub)

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.Ch:
			if !ok || ev == nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
				ev.SequenceNum, ev.EventType, ev.Payload); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
