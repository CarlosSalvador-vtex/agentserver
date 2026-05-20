package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

func bcryptHash(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	return string(h), err
}

// mockCloudRegisterStore implements CloudRegisterStore and also exposes
// UserIDForExecutor (matched by assertExeOwnedByUser's ad-hoc interface
// assertion). Lets us exercise CloudRegister without a real DB.
type mockCloudRegisterStore struct {
	hash       string
	hashErr    error
	owner      string
	ownerErr   error
	registered bool // false → UserIDForExecutor returns "" (unknown executor)
}

func (m *mockCloudRegisterStore) GetRegistrationTokenHash(ctx context.Context, exeID string) (string, error) {
	return m.hash, m.hashErr
}

func (m *mockCloudRegisterStore) UserIDForExecutor(ctx context.Context, exeID string) (string, error) {
	if !m.registered {
		return "", m.ownerErr
	}
	return m.owner, m.ownerErr
}

// newStoreWithExecutor returns a mock store that "knows" an executor
// row exists for exeID owned by userID.
func newStoreWithExecutor(t *testing.T, exeID, userID string) *mockCloudRegisterStore {
	t.Helper()
	return &mockCloudRegisterStore{owner: userID, registered: true}
}

func TestCloudRegister_BearerScheme_DelegatesToAgentserver(t *testing.T) {
	// Stub agentserver: any call to /internal/codex-auth/validate with
	// scheme=bearer + matching token returns user-id "u-1".
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
	})
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
	if resp.URL == "" || resp.URL[:5] != "wss:/" {
		t.Fatalf("url = %q", resp.URL)
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
	})
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

// When the validator is unconfigured (BaseURL=""), the legacy bcrypt
// fallback path must still authenticate codex < 0.132 clients.
func TestCloudRegister_LegacyBcryptFallback(t *testing.T) {
	// bcrypt of "legacy-token" (cost 4 to keep test snappy).
	// Generated via: bcrypt.GenerateFromPassword([]byte("legacy-token"), 4).
	// We compute it at test time to avoid a hardcoded constant.
	hash, err := bcryptHash("legacy-token")
	if err != nil {
		t.Fatalf("bcryptHash: %v", err)
	}
	store := &mockCloudRegisterStore{hash: hash}
	h := CloudRegister(store, "wss://test", AgentserverValidator{})

	req := httptest.NewRequest(http.MethodPost, "/cloud/executor/exe_x/register", nil)
	req.Header.Set("Authorization", "Bearer legacy-token")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("exe_id", "exe_x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}
