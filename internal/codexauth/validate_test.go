package codexauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestValidate_BearerHappyPath(t *testing.T) {
	srv := newAuthTestServer(t, "")
	uid := mustCreateTestUser(t, srv.Store.db)
	if err := srv.Store.InsertAccessToken(context.Background(), HashToken("good-bearer"), uid,
		time.Now().Add(1*time.Hour)); err != nil {
		t.Fatalf("InsertAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"scheme": "bearer", "token": "good-bearer"})
	rr := callValidate(t, srv, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UserID != uid {
		t.Errorf("user_id = %q, want %q", resp.UserID, uid)
	}
}

func TestValidate_BearerExpiredReturns401(t *testing.T) {
	srv := newAuthTestServer(t, "")
	uid := mustCreateTestUser(t, srv.Store.db)
	if err := srv.Store.InsertAccessToken(context.Background(), HashToken("expired"), uid,
		time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("InsertAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"scheme": "bearer", "token": "expired"})
	rr := callValidate(t, srv, body)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestValidate_BearerWithAccountIDMismatch_401(t *testing.T) {
	srv := newAuthTestServer(t, "")
	uid := mustCreateTestUser(t, srv.Store.db)
	if err := srv.Store.InsertAccessToken(context.Background(), HashToken("crosscheck"), uid,
		time.Now().Add(1*time.Hour)); err != nil {
		t.Fatalf("InsertAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"scheme":     "bearer",
		"token":      "crosscheck",
		"account_id": "different-user-id",
	})
	rr := callValidate(t, srv, body)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for account_id mismatch", rr.Code)
	}
}

func TestValidate_BearerWithAccountIDMatch_200(t *testing.T) {
	srv := newAuthTestServer(t, "")
	uid := mustCreateTestUser(t, srv.Store.db)
	if err := srv.Store.InsertAccessToken(context.Background(), HashToken("matchcheck"), uid,
		time.Now().Add(1*time.Hour)); err != nil {
		t.Fatalf("InsertAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"scheme":     "bearer",
		"token":      "matchcheck",
		"account_id": uid,
	})
	rr := callValidate(t, srv, body)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for account_id match", rr.Code)
	}
}

func TestValidate_AgentAssertionHappyPath(t *testing.T) {
	srv := newAuthTestServer(t, "")
	ctx := context.Background()
	uid := mustCreateTestUser(t, srv.Store.db)

	mint, err := srv.MintAgentIdentity(ctx, MintAgentIdentityArgs{
		AgentRuntimeID: "exe_v_test", UserID: uid, Email: "u@test",
	})
	if err != nil {
		t.Fatalf("MintAgentIdentity: %v", err)
	}
	// Issue a task_id (simulating codex's prior task/register call).
	if err := srv.Store.InsertAgentTask(ctx, "task_v_test", "exe_v_test", uid,
		time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("InsertAgentTask: %v", err)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	sig := ed25519.Sign(mint.privKey, []byte("exe_v_test:task_v_test:"+ts))
	assertion := map[string]string{
		"agent_runtime_id": "exe_v_test",
		"task_id":          "task_v_test",
		"timestamp":        ts,
		"signature":        base64.StdEncoding.EncodeToString(sig),
	}
	assBytes, _ := json.Marshal(assertion)
	assB64 := base64.RawURLEncoding.EncodeToString(assBytes)

	body, _ := json.Marshal(map[string]string{
		"scheme":     "agent_assertion",
		"assertion":  assB64,
		"account_id": uid,
	})
	rr := callValidate(t, srv, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UserID != uid {
		t.Errorf("user_id = %q, want %q", resp.UserID, uid)
	}
}

func callValidate(t *testing.T, srv *Server, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/internal/codex-auth/validate", srv.HandleValidate)
	req := httptest.NewRequest(http.MethodPost, "/internal/codex-auth/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}
