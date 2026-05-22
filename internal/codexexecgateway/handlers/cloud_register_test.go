package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// mockCloudRegisterStore implements CloudRegisterStore and exposes
// UserIDForExecutor (matched by assertExeOwnedByUser's ad-hoc interface
// assertion). Lets us exercise CloudRegister without a real DB.
type mockCloudRegisterStore struct {
	owner      string
	ownerErr   error
	registered bool // false → UserIDForExecutor returns "" (unknown executor)
}

func (m *mockCloudRegisterStore) UserIDForExecutor(ctx context.Context, exeID string) (string, error) {
	if !m.registered {
		return "", m.ownerErr
	}
	return m.owner, m.ownerErr
}

func newStoreWithExecutor(t *testing.T, exeID, userID string) *mockCloudRegisterStore {
	t.Helper()
	return &mockCloudRegisterStore{owner: userID, registered: true}
}

const testTicketSecret = "ticket-secret"

func TestCloudRegister_BearerScheme_DelegatesToAgentserver(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/codex-auth/validate" {
			http.Error(w, "wrong path", 404)
			return
		}
		var req struct {
			Scheme string `json:"scheme"`
			Token  string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Scheme == "bearer" && req.Token == "valid-bearer" {
			json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "no"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL:        agentSrv.URL,
		InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer valid-bearer")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("status = %d body = %s", rr.Code, body)
	}
	var resp cloudRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExecutorID != "exe_x" {
		t.Fatalf("executor_id = %q", resp.ExecutorID)
	}
	if !strings.HasPrefix(resp.URL, "wss://test/codex-exec/exe_x?token=") {
		t.Fatalf("url = %q", resp.URL)
	}

	// Verify the ticket in the URL passes VerifyWSTicket with the same secret.
	const prefix = "wss://test/codex-exec/exe_x?token="
	ticket := strings.TrimPrefix(resp.URL, prefix)
	if err := VerifyWSTicket(ticket, "exe_x", testTicketSecret); err != nil {
		t.Fatalf("ticket should verify: %v", err)
	}
}

// Wrong-owner: agentserver validates token (returns u-2) but executor
// is owned by u-1 → 403.
func TestCloudRegister_BearerScheme_WrongOwner(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-2"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL:        agentSrv.URL,
		InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Without validator configured (BaseURL empty), every register attempt
// must be rejected — no legacy bcrypt fallback anymore.
func TestCloudRegister_RejectsWithoutValidator(t *testing.T) {
	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Codex 0.133 renamed the URL param to `env_id` and the path to
// /cloud/environment/{env_id}/register. The handler accepts either chi
// param name so both the legacy and the new route resolve correctly.
func TestCloudRegister_AcceptsEnvIDParam(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/cloud/environment/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("env_id", "exe_x") // codex 0.133 path uses env_id
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp cloudRegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExecutorID != "exe_x" {
		t.Fatalf("executor_id = %q", resp.ExecutorID)
	}
}

// Validator returning 502 (e.g. agentserver pod restarting) twice
// before serving 200 — classifyAndValidate should retry and succeed
// without surfacing the transient failure. Critical: codex
// register-loop `?` would exit on any non-2xx from us, so dropping
// every client every deploy is what we're preventing here.
func TestCloudRegister_RetriesTransientValidatorErrors(t *testing.T) {
	var attempts int
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer valid-bearer")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 after retries, got %d body=%s", rr.Code, rr.Body.String())
	}
	if attempts != 3 {
		t.Errorf("validator called %d times, want 3 (two failures + one success)", attempts)
	}
}

// Validator returns 401 — that's a hard auth failure, no retries.
// Caller still gets 401 immediately (no extra wait).
func TestCloudRegister_HardAuthFailureSkipsRetries(t *testing.T) {
	var attempts int
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad token"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_x", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)
	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rr.Code, rr.Body.String())
	}
	if attempts != 1 {
		t.Errorf("validator called %d times, want 1 (no retries on hard failure)", attempts)
	}
}

// Both legacy `exe_id` and new `env_id` chi params resolve to the same
// id when both happen to be set (defensive — production routes never
// set both, but document the precedence).
func TestCloudRegister_ExeIDTakesPrecedenceOverEnvID(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"user_id": "u-1"})
	}))
	defer agentSrv.Close()

	store := newStoreWithExecutor(t, "exe_legacy", "u-1")
	h := CloudRegister(store, "wss://test", AgentserverValidator{
		BaseURL: agentSrv.URL, InternalSecret: "shh",
	}, testTicketSecret)

	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_legacy/register", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_legacy")
	rctx.URLParams.Add("env_id", "exe_new") // ignored when exe_id present
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}
