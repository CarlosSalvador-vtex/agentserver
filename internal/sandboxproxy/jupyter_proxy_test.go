package sandboxproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/sbxstore"
)

func TestJupyterProxy_MissingTokenRejected(t *testing.T) {
	s := &Server{BaseDomains: []string{"agent.test"}}
	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	rr := httptest.NewRecorder()
	s.handleJupyterSubdomainProxy(rr, req, "sbx-1")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing token: status=%d, want 400", rr.Code)
	}
}

func TestJupyterProxy_MissingCookieRedirectsToLogin(t *testing.T) {
	s := &Server{BaseDomains: []string{"agent.test"}}
	req := httptest.NewRequest(http.MethodGet, "/lab", nil)
	rr := httptest.NewRecorder()
	s.handleJupyterSubdomainProxy(rr, req, "sbx-1")
	if rr.Code != http.StatusFound {
		t.Errorf("missing cookie: status=%d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "https://agent.test/" {
		t.Errorf("Location=%q", loc)
	}
}

// Keep the test suite small. Heavier integration coverage (auth
// exchange happy path, membership check, proxy success) would need
// fakes for Auth/DB/Sandboxes — add only if regressions appear.
var _ = sbxstore.Sandbox{}
