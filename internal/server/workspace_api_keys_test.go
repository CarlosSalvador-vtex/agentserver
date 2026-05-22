package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
)

// newAPIKeyTestServer returns a Server wired to a real test DB.
// The returned server is only needed by tests that exercise DB paths
// (mint, list, revoke). Tests that only exercise scope validation use
// validateScopes directly and require no DB.
func newAPIKeyTestServer(t *testing.T) *Server {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		d.Exec(`DELETE FROM workspace_api_keys`)
		d.Exec(`DELETE FROM workspace_members`)
		d.Exec(`DELETE FROM workspaces`)
		d.Exec(`DELETE FROM users`)
	})
	return &Server{DB: d}
}

// reqWithUser wraps httptest.NewRequest and sets user+chi params in context.
func reqWithUser(method, target, uid string, body []byte, params map[string]string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := auth.ContextWithUserID(context.Background(), uid)
	r = r.WithContext(ctx)
	for k, v := range params {
		r = withChiURLParam(r, k, v)
	}
	return r
}

// mintKeyViaHandler calls handleMintWorkspaceAPIKey and returns the parsed response.
func mintKeyViaHandler(t *testing.T, srv *Server, wid, uid, name string, scopes []string) WorkspaceAPIKeyMintResponse {
	t.Helper()
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{Name: name, Scopes: scopes})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wid+"/api-keys", uid, body, map[string]string{"wid": wid})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint: want 201, got %d — %s", rr.Code, rr.Body.String())
	}
	var resp WorkspaceAPIKeyMintResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("mint decode: %v", err)
	}
	return resp
}

// TestMintListRevoke is the happy-path round-trip: mint a key, list it, revoke it.
// Requires TEST_DATABASE_URL; skipped otherwise (integration test).
func TestMintListRevoke(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_a", "u1", "owner")

	// Mint.
	resp := mintKeyViaHandler(t, srv, "ws_a", "u1", "my-bot", []string{"turns:submit"})
	if resp.ID == "" || resp.Secret == "" || resp.Prefix == "" {
		t.Fatalf("missing fields: %+v", resp)
	}
	if len(resp.Scopes) != 1 || resp.Scopes[0] != "turns:submit" {
		t.Fatalf("wrong scopes: %v", resp.Scopes)
	}
	if resp.ExpiresAt == "" {
		t.Fatal("mint response should include expires_at")
	}
	if _, err := time.Parse(time.RFC3339, resp.ExpiresAt); err != nil {
		t.Fatalf("expires_at is not RFC3339: %q", resp.ExpiresAt)
	}

	// List.
	listReq := reqWithUser(http.MethodGet, "/api/workspaces/ws_a/api-keys", "u1", nil, map[string]string{"wid": "ws_a"})
	listRR := httptest.NewRecorder()
	srv.handleListWorkspaceAPIKeys(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", listRR.Code)
	}
	var keys []WorkspaceAPIKey
	json.NewDecoder(listRR.Body).Decode(&keys)
	if len(keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(keys))
	}
	if keys[0].ID != resp.ID {
		t.Fatalf("key id mismatch: got %q want %q", keys[0].ID, resp.ID)
	}
	if keys[0].ExpiresAt == "" {
		t.Fatal("list response should include expires_at")
	}

	// Revoke.
	revokeReq := reqWithUser(http.MethodDelete, "/api/workspaces/ws_a/api-keys/"+resp.ID, "u1", nil,
		map[string]string{"wid": "ws_a", "id": resp.ID})
	revokeRR := httptest.NewRecorder()
	srv.handleRevokeWorkspaceAPIKey(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d — %s", revokeRR.Code, revokeRR.Body.String())
	}

	// List again — key still appears but with RevokedAt set.
	listRR2 := httptest.NewRecorder()
	srv.handleListWorkspaceAPIKeys(listRR2, reqWithUser(http.MethodGet, "/api/workspaces/ws_a/api-keys", "u1", nil, map[string]string{"wid": "ws_a"}))
	var keys2 []WorkspaceAPIKey
	json.NewDecoder(listRR2.Body).Decode(&keys2)
	if len(keys2) != 1 {
		t.Fatalf("want 1 key after revoke, got %d", len(keys2))
	}
	if keys2[0].RevokedAt == nil {
		t.Fatal("revoked_at should be set after revoke")
	}
}

// TestMint_RejectsEmptyScopes verifies that validateScopes rejects an empty list.
// This is a pure catalog test; no DB required.
func TestMint_RejectsEmptyScopes(t *testing.T) {
	err := validateScopes([]string{})
	if err == nil {
		t.Fatal("expected error for empty scopes, got nil")
	}
}

// TestMint_RejectsUnavailableScope verifies that validateScopes rejects scopes
// with Available=false (e.g. "mailbox:send" in v1). No DB required.
func TestMint_RejectsUnavailableScope(t *testing.T) {
	err := validateScopes([]string{"mailbox:send"})
	if err == nil {
		t.Fatal("expected error for unavailable scope, got nil")
	}
}

// TestMint_RejectsUnknownScope verifies that validateScopes rejects completely
// unknown scope strings. No DB required.
func TestMint_RejectsUnknownScope(t *testing.T) {
	err := validateScopes([]string{"bogus:scope"})
	if err == nil {
		t.Fatal("expected error for unknown scope, got nil")
	}
}

// TestMint_DefaultExpiration verifies that omitting expires_at results in
// a response with expires_at approximately NOW + 90 days.
func TestMint_DefaultExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp1", "u_exp1", "owner")

	resp := mintKeyViaHandler(t, srv, "ws_exp1", "u_exp1", "default-exp", []string{"turns:submit"})
	if resp.ExpiresAt == "" {
		t.Fatal("expires_at must be set")
	}
	exp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %q — %v", resp.ExpiresAt, err)
	}
	lo := time.Now().UTC().Add(89 * 24 * time.Hour)
	hi := time.Now().UTC().Add(91 * 24 * time.Hour)
	if exp.Before(lo) || exp.After(hi) {
		t.Fatalf("expected ~NOW+90d, got %v (lo=%v hi=%v)", exp, lo, hi)
	}
}

// TestMint_RejectsPastExpiration verifies that sending an expires_at in the
// past returns HTTP 422.
func TestMint_RejectsPastExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp2", "u_exp2", "owner")

	pastExp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "past-exp",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: pastExp,
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp2/api-keys", "u_exp2", body, map[string]string{"wid": "ws_exp2"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMint_RejectsTooFarFutureExpiration verifies that sending an expires_at
// more than 365 days in the future returns HTTP 422.
func TestMint_RejectsTooFarFutureExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp3", "u_exp3", "owner")

	tooFar := time.Now().UTC().Add(400 * 24 * time.Hour).Format(time.RFC3339)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "too-far",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: tooFar,
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp3/api-keys", "u_exp3", body, map[string]string{"wid": "ws_exp3"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMint_AcceptsValidExpiration verifies that a valid expires_at in the
// allowed window returns HTTP 201 and echoes the expires_at back.
func TestMint_AcceptsValidExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp4", "u_exp4", "owner")

	exp30d := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "valid-exp",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: exp30d.Format(time.RFC3339),
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp4/api-keys", "u_exp4", body, map[string]string{"wid": "ws_exp4"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d — %s", rr.Code, rr.Body.String())
	}
	var resp WorkspaceAPIKeyMintResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotExp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %q", resp.ExpiresAt)
	}
	// Allow 1s tolerance for round-trip through RFC3339 format.
	if gotExp.Sub(exp30d).Abs() > time.Second {
		t.Fatalf("expires_at mismatch: got %v, want ~%v", gotExp, exp30d)
	}
}

// TestListScopes_ReturnsCatalog verifies that handleListWorkspaceAPIKeyScopes
// returns the full catalog with at least 7 entries and at least one Available=true.
// Requires TEST_DATABASE_URL (needs workspace member check); skipped otherwise.
func TestListScopes_ReturnsCatalog(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_e", "u5", "developer")

	req := reqWithUser(http.MethodGet, "/api/workspaces/ws_e/api-keys/scopes", "u5", nil, map[string]string{"wid": "ws_e"})
	rr := httptest.NewRecorder()
	srv.handleListWorkspaceAPIKeyScopes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", rr.Code, rr.Body.String())
	}
	var scopes []APIKeyScopeDescriptor
	if err := json.NewDecoder(rr.Body).Decode(&scopes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(scopes) < 7 {
		t.Fatalf("want ≥ 7 scopes, got %d", len(scopes))
	}
	hasAvailable := false
	for _, sc := range scopes {
		if sc.Available {
			hasAvailable = true
		}
	}
	if !hasAvailable {
		t.Fatal("expected at least one Available=true scope")
	}
}
