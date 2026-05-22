package codexappgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/auth"
)

// handlerWithContext builds a turnAPIHandler with a fakeBroker that returns
// a successful turn. It is used across multiple auth-path subtests.
func newSuccessHandler() *turnAPIHandler {
	return &turnAPIHandler{
		runner: &fakeBroker{
			startThreadFn: func(_ context.Context, _ string) (string, error) {
				return "thr-new", nil
			},
			turnFn: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) (json.RawMessage, error) {
				return json.RawMessage(`{"id":"trn-1","status":"completed"}`), nil
			},
		},
	}
}

// makeBody returns a JSON-encoded turn request with the given workspaceId.
func makeBody(workspaceID string) *bytes.Reader {
	b, _ := json.Marshal(map[string]any{
		"workspaceId": workspaceID,
		"params":      map[string]any{"input": []any{}},
	})
	return bytes.NewReader(b)
}

// withBearerCtx injects bearer-path context values (workspace + scopes)
// into r, simulating what requireInternalOrAPIKey sets when the bearer
// token validates successfully.
func withBearerCtx(r *http.Request, workspaceID string, scopes []string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxKeyAuthorizedWorkspace, workspaceID)
	ctx = context.WithValue(ctx, ctxKeyAuthorizedScopes, scopes)
	return r.WithContext(ctx)
}

// --- Handler-level tests (context values injected directly) ---

func TestTurnAPI_BearerAuth_WorkspaceMatch(t *testing.T) {
	h := newSuccessHandler()
	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-A"))
	r = withBearerCtx(r, "ws-A", []string{"turns:submit"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTurnAPI_BearerAuth_WorkspaceMismatch(t *testing.T) {
	h := newSuccessHandler()
	// Bearer key authorizes ws-A; body says ws-B → 403
	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-B"))
	r = withBearerCtx(r, "ws-A", []string{"turns:submit"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTurnAPI_BearerAuth_MissingScope(t *testing.T) {
	h := newSuccessHandler()
	// Bearer key has threads:read but not turns:submit → 403
	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-A"))
	r = withBearerCtx(r, "ws-A", []string{"threads:read"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTurnAPI_BearerAuth_EmptyScopes(t *testing.T) {
	h := newSuccessHandler()
	// Bearer key has no scopes → 403
	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-A"))
	r = withBearerCtx(r, "ws-A", []string{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTurnAPI_InternalSecretAuth_BypassesScopeCheck(t *testing.T) {
	h := newSuccessHandler()
	// No context values set (internal-secret path) → handler runs without 403
	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-A"))
	// No ctxKeyAuthorizedWorkspace or ctxKeyAuthorizedScopes in context.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- Middleware-level test for invalid Bearer key → 401 ---

func TestTurnAPI_BearerAuth_InvalidKey(t *testing.T) {
	// Fake agentserver that always returns 401.
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer fakeSrv.Close()

	validator := auth.NewAPIKeyValidator(fakeSrv.URL, "test-secret")
	s := &Server{
		cfg: ServeConfig{
			AgentserverInternalSecret: "real-secret",
		},
		apiKeyValidator: validator,
	}
	mw := s.requireInternalOrAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest("POST", "/api/turns", makeBody("ws-A"))
	r.Header.Set("Authorization", "Bearer wak_badbadbad_whatever")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", w.Code, w.Body.String())
	}
}
