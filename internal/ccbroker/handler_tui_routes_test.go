package ccbroker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/internal/ccbroker/tools"
)

// helper: build a minimal Server with gate + registries wired
func newRoutesTestServer(t *testing.T) *Server {
	s := &Server{
		activeTurns:  newActiveTurnRegistry(),
		compactQueue: newCompactQueue(),
	}
	s.gate = tools.NewGate(func(_ string, _ tools.Event) {})
	return s
}

func TestDecidePermission_HappyPath(t *testing.T) {
	s := newRoutesTestServer(t)
	pid := "perm_xyz"
	s.gate.AddPendingForTest(pid, "s1", "t1")

	body := `{"verdict":"allow","scope":"once"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		"/api/sessions/s1/permissions/"+pid+"/decide",
		strings.NewReader(body))
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status %d body=%s", rr.Code, rr.Body)
	}
}

func TestDecidePermission_AlreadyResolved(t *testing.T) {
	s := newRoutesTestServer(t)
	body := `{"verdict":"allow","scope":"once"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		"/api/sessions/s1/permissions/perm_unknown/decide",
		strings.NewReader(body))
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status %d want 409", rr.Code)
	}
}

func TestCancelTurn_Active(t *testing.T) {
	s := newRoutesTestServer(t)
	cancelled := false
	s.activeTurns.Set("s1", "t1", func() { cancelled = true })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/s1/turns/t1/cancel", nil)
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status %d", rr.Code)
	}
	if !cancelled {
		t.Errorf("cancel func not invoked")
	}
}

func TestCancelTurn_NoOpOnMismatch(t *testing.T) {
	s := newRoutesTestServer(t)
	cancelled := false
	s.activeTurns.Set("s1", "t1", func() { cancelled = true })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/s1/turns/t_DIFFERENT/cancel", nil)
	s.Routes().ServeHTTP(rr, req)
	// Should still succeed (idempotent), but cancel func not called
	if rr.Code != http.StatusAccepted {
		t.Errorf("status %d", rr.Code)
	}
	if cancelled {
		t.Errorf("cancel func should NOT be invoked for mismatched tid")
	}
}

func TestGetActiveTurn_ReportsCurrent(t *testing.T) {
	s := newRoutesTestServer(t)
	s.activeTurns.Set("s1", "t99", func() {})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sessions/s1/turns/active", nil)
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status %d", rr.Code)
	}
	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["turn_id"] != "t99" {
		t.Errorf("turn_id=%v want t99", resp["turn_id"])
	}
}

func TestGetActiveTurn_NoActive_ReturnsNull(t *testing.T) {
	s := newRoutesTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sessions/s_unknown/turns/active", nil)
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"turn_id":null`) {
		t.Errorf("body %s should contain turn_id:null", rr.Body)
	}
}

func TestCompactNow_QueuesForSession(t *testing.T) {
	s := newRoutesTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/s1/compact", nil)
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status %d", rr.Code)
	}
	if !s.compactQueue.IsSet("s1") {
		t.Errorf("compactQueue should mark s1")
	}
}

func TestActiveTurnRegistry_RaceSafe(t *testing.T) {
	r := newActiveTurnRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			r.Set("s", "t", func() {})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = r.Get("s")
		}()
	}
	wg.Wait()
}
