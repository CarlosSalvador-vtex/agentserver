package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestInternalValidateAPIKey_HappyPath mints a key via the public handler,
// then validates it via the internal handler. Requires TEST_DATABASE_URL;
// skipped otherwise.
func TestInternalValidateAPIKey_HappyPath(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_v", "u9", "owner")

	// Mint a key via the public handler.
	minted := mintKeyViaHandler(t, srv, "ws_v", "u9", "integration-bot", []string{"turns:submit"})
	if minted.Secret == "" {
		t.Fatal("minted secret is empty")
	}

	// Validate via the internal handler (call directly, no middleware needed).
	body, _ := json.Marshal(internalValidateAPIKeyRequest{Secret: minted.Secret})
	req := httptest.NewRequest(http.MethodPost, "/internal/workspace-api-keys/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleInternalValidateAPIKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", rec.Code, rec.Body.String())
	}
	var out internalValidateAPIKeyResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.WorkspaceID != "ws_v" {
		t.Fatalf("workspace_id mismatch: got %q want %q", out.WorkspaceID, "ws_v")
	}
	if out.KeyID == "" {
		t.Fatal("key_id is empty")
	}
	if len(out.Scopes) != 1 || out.Scopes[0] != "turns:submit" {
		t.Fatalf("scopes mismatch: got %v", out.Scopes)
	}
}

// TestInternalValidateAPIKey_WrongSecret sends a well-formed wak_ token whose
// secret part doesn't match any stored hash; expects 401.
// This calls the handler directly — no DB state needed for the prefix miss.
func TestInternalValidateAPIKey_WrongSecret(t *testing.T) {
	// Skip if no DB: splitAPIKey passes but ValidateWorkspaceAPIKeySecret needs DB.
	srv := newAPIKeyTestServer(t)

	body, _ := json.Marshal(internalValidateAPIKeyRequest{Secret: "wak_xxxxxxxx_YYYY0000000000000000000000000000000000000"})
	req := httptest.NewRequest(http.MethodPost, "/internal/workspace-api-keys/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleInternalValidateAPIKey(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d — %s", rec.Code, rec.Body.String())
	}
}

// TestInternalValidateAPIKey_MissingXInternalSecret sends the request through
// the full router without the X-Internal-Secret header; the inline middleware
// must reject with 401 before the handler runs.
// When INTERNAL_API_SECRET is unset (dev/CI), the middleware allows all callers
// through, so we set the env var for the duration of this test.
// This test does NOT need a real DB — it only exercises the auth middleware
// layer on a nil-DB Server, which is sufficient because the middleware rejects
// the request before any DB call is made.
func TestInternalValidateAPIKey_MissingXInternalSecret(t *testing.T) {
	const fakeSecret = "test-internal-secret-for-unit-test"
	old := os.Getenv("INTERNAL_API_SECRET")
	os.Setenv("INTERNAL_API_SECRET", fakeSecret)
	t.Cleanup(func() { os.Setenv("INTERNAL_API_SECRET", old) })

	// Use a nil-DB server — the middleware rejects before any DB call.
	srv := &Server{}
	router := srv.Router()

	req := httptest.NewRequest(http.MethodPost, "/internal/workspace-api-keys/validate",
		bytes.NewReader([]byte(`{}`)))
	// Deliberately omit X-Internal-Secret.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 from middleware, got %d — %s", rec.Code, rec.Body.String())
	}
}
