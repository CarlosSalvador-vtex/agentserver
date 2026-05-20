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
