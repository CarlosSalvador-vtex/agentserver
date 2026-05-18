package codexexecgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/relay"
	"github.com/go-chi/chi/v5"
)

// ────────────────────────────────────────────────────────────────────
// Public PUT/GET endpoints (ticket Bearer auth)
// ────────────────────────────────────────────────────────────────────

// handleRelayPut accepts the upload half of a relay session. The
// ticket must match between URL and Authorization header (defence in
// depth: prevents accidental cross-ticket use if a proxy rewrites
// the path).
func (s *Server) handleRelayPut(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil {
		http.Error(w, "relay disabled (no public HTTPS base URL configured)", http.StatusNotFound)
		return
	}
	urlTicket := chi.URLParam(r, "ticket")
	authTicket, ok := relay.ExtractBearerTicket(r.Header.Get("Authorization"))
	if !ok || authTicket != urlTicket {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rel, found := s.relayRegistry.Lookup(urlTicket)
	if !found {
		http.Error(w, "ticket not found or expired", http.StatusGone)
		return
	}
	status, body := rel.AcceptPut(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handleRelayGet accepts the download half. Streams body chunked.
func (s *Server) handleRelayGet(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil {
		http.Error(w, "relay disabled (no public HTTPS base URL configured)", http.StatusNotFound)
		return
	}
	urlTicket := chi.URLParam(r, "ticket")
	authTicket, ok := relay.ExtractBearerTicket(r.Header.Get("Authorization"))
	if !ok || authTicket != urlTicket {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rel, found := s.relayRegistry.Lookup(urlTicket)
	if !found {
		http.Error(w, "ticket not found or expired", http.StatusGone)
		return
	}
	// Set Content-Type before AcceptGet because the pairing goroutine's
	// first Write implicitly calls WriteHeader(200) (success path) and
	// any headers we set after that would be silently dropped.
	//
	// We do NOT set Transfer-Encoding: chunked — Go's HTTP server
	// applies it automatically when there's no Content-Length, and
	// setting it manually here would conflict with the framework's own
	// framing on the error path (small JSON body).
	w.Header().Set("Content-Type", "application/octet-stream")
	status, body := rel.AcceptGet(w)
	// status==0: streamed successfully; headers + 200 already flushed.
	// status!=0: pairing failed before any byte was written, emit the
	// status + JSON body. Override the Content-Type since the body is
	// JSON, not octet-stream.
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

// ────────────────────────────────────────────────────────────────────
// Internal: relay ticket mint (X-Internal-Secret auth applied at route)
// ────────────────────────────────────────────────────────────────────

type relayCreateRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SourceExeID string `json:"source_exe_id"`
	DestExeID   string `json:"dest_exe_id"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	MaxBytes    int64  `json:"max_bytes,omitempty"`
}

type relayCreateResponse struct {
	Ticket      string    `json:"ticket"`
	UploadURL   string    `json:"upload_url"`
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *Server) handleRelayCreate(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil || s.config.PublicHTTPSBaseURL == "" {
		writeJSONErr(w, http.StatusServiceUnavailable, "relay not enabled (PublicHTTPSBaseURL unset)")
		return
	}

	var req relayCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.WorkspaceID == "" || req.SourceExeID == "" || req.DestExeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "workspace_id, source_exe_id, dest_exe_id required")
		return
	}

	// Workspace ownership check — both executors must belong to the
	// caller's workspace. Two separate queries keep error messages
	// specific without leaking information about the other side.
	if s.store != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for _, exeID := range []string{req.SourceExeID, req.DestExeID} {
			owns, err := s.store.OwnsExecutor(ctx, req.WorkspaceID, exeID)
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "ownership check failed")
				return
			}
			if !owns {
				writeJSONErr(w, http.StatusForbidden, "executor not in workspace: "+exeID)
				return
			}
		}
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	rel, err := s.relayRegistry.Create(relay.CreateOptions{
		WorkspaceID: req.WorkspaceID,
		SourceExeID: req.SourceExeID,
		DestExeID:   req.DestExeID,
		TTL:         ttl, // 0 → registry default
		MaxBytes:    req.MaxBytes,
	})
	if err != nil {
		switch err {
		case relay.ErrWorkspaceCapReached:
			writeJSONErr(w, http.StatusTooManyRequests, err.Error())
		default:
			writeJSONErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	url := strings.TrimRight(s.config.PublicHTTPSBaseURL, "/") + "/relay/" + rel.Ticket
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(relayCreateResponse{
		Ticket:      rel.Ticket,
		UploadURL:   url,
		DownloadURL: url,
		ExpiresAt:   rel.ExpiresAt,
	})
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
