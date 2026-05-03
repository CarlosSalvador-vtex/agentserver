package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/db"
)

// internalRouter wires the turn-finished route.
func internalRouter(s *Server) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/internal/sessions/{sid}/turn-finished", s.handleTurnFinished)
	return r
}

// --- Unit tests (no DB needed) ---

func TestTurnFinished_SessionIDMismatch_Returns400(t *testing.T) {
	s := &Server{}
	r := internalRouter(s)
	body := `{"session_id":"cse_other","turn_id":"trn_1"}`
	req := httptest.NewRequest("POST", "/internal/sessions/cse_x/turn-finished",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d want 400", rr.Code)
	}
}

func TestTurnFinished_SecretRequired_Returns403(t *testing.T) {
	os.Setenv("INTERNAL_API_SECRET", "mysecret")
	defer os.Unsetenv("INTERNAL_API_SECRET")

	s := &Server{}
	r := internalRouter(s)
	body := `{"session_id":"cse_x","turn_id":"trn_1"}`
	req := httptest.NewRequest("POST", "/internal/sessions/cse_x/turn-finished",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-Internal-Secret header.
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status %d want 403", rr.Code)
	}
}

func TestTurnFinished_SecretMatch_Passes(t *testing.T) {
	os.Setenv("INTERNAL_API_SECRET", "mysecret")
	defer os.Unsetenv("INTERNAL_API_SECRET")

	// We still need DB for ClearActiveTurn. Skip if absent.
	s, cleanup := newTestServerTUI(t, "")
	defer cleanup()

	sid := "cse_internal_secret_" + t.Name()
	if err := s.DB.CreateAgentSessionTUI(context.Background(), db.CreateTUISessionParams{
		ID:              sid,
		WorkspaceID:     "ws_test",
		ExternalID:      "tui:exe_a:internal_secret",
		CreatorUserID:   "u_test",
		PermissionMode:  "ask",
	}); err != nil {
		t.Fatalf("CreateAgentSessionTUI: %v", err)
	}
	t.Cleanup(func() { s.DB.Exec(`DELETE FROM agent_sessions WHERE id=$1`, sid) })

	body := `{"session_id":"` + sid + `","turn_id":"trn_1"}`
	req := httptest.NewRequest("POST", "/internal/sessions/"+sid+"/turn-finished",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "mysecret")
	rr := httptest.NewRecorder()
	internalRouter(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status %d want 200", rr.Code)
	}
}

// --- Integration tests (require TEST_DATABASE_URL) ---

func TestTurnFinished_HappyPath_ClearsActiveTurn(t *testing.T) {
	s, cleanup := newTestServerTUI(t, "")
	defer cleanup()

	sid := "cse_tf_happy_" + t.Name()
	if err := s.DB.CreateAgentSessionTUI(context.Background(), db.CreateTUISessionParams{
		ID:              sid,
		WorkspaceID:     "ws_test",
		ExternalID:      "tui:exe_a:tf_happy",
		CreatorUserID:   "u_test",
		PermissionMode:  "ask",
	}); err != nil {
		t.Fatalf("CreateAgentSessionTUI: %v", err)
	}
	t.Cleanup(func() { s.DB.Exec(`DELETE FROM agent_sessions WHERE id=$1`, sid) })

	turnID := "trn_happy_1"
	if _, err := s.DB.ClaimActiveTurn(context.Background(), sid, turnID); err != nil {
		t.Fatalf("ClaimActiveTurn: %v", err)
	}

	body := `{"session_id":"` + sid + `","turn_id":"` + turnID + `"}`
	req := httptest.NewRequest("POST", "/internal/sessions/"+sid+"/turn-finished",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	internalRouter(s).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status %d want 200; body=%s", rr.Code, rr.Body)
	}

	cur, err := s.DB.GetActiveTurn(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetActiveTurn: %v", err)
	}
	if cur != "" {
		t.Errorf("active_turn_id=%q want empty (cleared)", cur)
	}
}

func TestTurnFinished_StaleTurnID_IsNoOp(t *testing.T) {
	s, cleanup := newTestServerTUI(t, "")
	defer cleanup()

	sid := "cse_tf_stale_" + t.Name()
	if err := s.DB.CreateAgentSessionTUI(context.Background(), db.CreateTUISessionParams{
		ID:              sid,
		WorkspaceID:     "ws_test",
		ExternalID:      "tui:exe_a:tf_stale",
		CreatorUserID:   "u_test",
		PermissionMode:  "ask",
	}); err != nil {
		t.Fatalf("CreateAgentSessionTUI: %v", err)
	}
	t.Cleanup(func() { s.DB.Exec(`DELETE FROM agent_sessions WHERE id=$1`, sid) })

	activeTurnID := "trn_current"
	if _, err := s.DB.ClaimActiveTurn(context.Background(), sid, activeTurnID); err != nil {
		t.Fatalf("ClaimActiveTurn: %v", err)
	}

	// Send a turn-finished with an old turn_id — CAS should not clear the current turn.
	body := `{"session_id":"` + sid + `","turn_id":"trn_old"}`
	req := httptest.NewRequest("POST", "/internal/sessions/"+sid+"/turn-finished",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	internalRouter(s).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status %d want 200; body=%s", rr.Code, rr.Body)
	}

	cur, err := s.DB.GetActiveTurn(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetActiveTurn: %v", err)
	}
	if cur != activeTurnID {
		t.Errorf("active_turn_id=%q want %q (should not be cleared by stale turn_id)", cur, activeTurnID)
	}
}
