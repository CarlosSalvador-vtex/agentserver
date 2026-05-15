package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func mintRow(t *testing.T, d *db.DB, id, secret, uid, wid string, exp time.Time, revokedAt *time.Time) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	row := db.CodexToken{
		ID: id, UserID: uid, WorkspaceID: wid, Name: "n",
		TokenHash: string(hash), ExpiresAt: exp,
	}
	if err := d.CreateCodexToken(context.Background(), row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if revokedAt != nil {
		_, _ = d.Exec(`UPDATE codex_remote_tokens SET revoked_at = $1 WHERE id = $2`, *revokedAt, id)
	}
}

func TestHandleVerifyCodexToken_HappyPath(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "supersecret", "u1", "ws_a", time.Now().Add(time.Hour), nil)

	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_supersecret"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		UserID, WorkspaceID string
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.UserID != "u1" || resp.WorkspaceID != "ws_a" {
		t.Fatalf("got %+v", resp)
	}
}

func TestHandleVerifyCodexToken_BadSecret_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "rightsecret", "u1", "ws_a", time.Now().Add(time.Hour), nil)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_wrongsecret"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_Expired_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "s", "u1", "ws_a", time.Now().Add(-time.Hour), nil)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_s"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_Revoked_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	now := time.Now()
	mintRow(t, d, "abc12345", "s", "u1", "ws_a", time.Now().Add(time.Hour), &now)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_s"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_NotFound_401(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	body := bytes.NewReader([]byte(`{"token":"ast_zzzzzzzz_zzzz"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_BadShape_401(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	body := bytes.NewReader([]byte(`{"token":"garbage"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}
