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
	"github.com/agentserver/agentserver/internal/secrets"
)

// mintRow inserts a codex_remote_tokens row using the new secrets module.
// id and full are derived from secrets.Mint; callers that need the full
// token for the verify request should use mintRowFromSpec instead.
func mintRow(t *testing.T, d *db.DB, uid, wid string, exp time.Time, revokedAt *time.Time) (fullToken string) {
	t.Helper()
	tok, err := secrets.Mint(secrets.AgentserverTokenSpec)
	if err != nil {
		t.Fatalf("mintRow: Mint: %v", err)
	}
	row := db.CodexToken{
		ID: tok.ID, UserID: uid, WorkspaceID: wid, Name: "n",
		TokenHash: tok.Hash, ExpiresAt: exp,
	}
	if err := d.CreateCodexToken(context.Background(), row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if revokedAt != nil {
		_, _ = d.Exec(`UPDATE codex_remote_tokens SET revoked_at = $1 WHERE id = $2`, *revokedAt, tok.ID)
	}
	return tok.Full
}

func TestHandleVerifyCodexToken_HappyPath(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	fullToken := mintRow(t, d, "u1", "ws_a", time.Now().Add(time.Hour), nil)

	body := bytes.NewReader([]byte(`{"token":"` + fullToken + `"}`))
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
	_ = mintRow(t, d, "u1", "ws_a", time.Now().Add(time.Hour), nil)
	// Mint a second token — its Full will not match the first row's stored hash.
	otherTok, err := secrets.Mint(secrets.AgentserverTokenSpec)
	if err != nil {
		t.Fatalf("Mint other: %v", err)
	}
	body := bytes.NewReader([]byte(`{"token":"` + otherTok.Full + `"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_Expired_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	fullToken := mintRow(t, d, "u1", "ws_a", time.Now().Add(-time.Hour), nil)
	body := bytes.NewReader([]byte(`{"token":"` + fullToken + `"}`))
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
	fullToken := mintRow(t, d, "u1", "ws_a", time.Now().Add(time.Hour), &now)
	body := bytes.NewReader([]byte(`{"token":"` + fullToken + `"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_NotFound_401(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	// A valid-shape token that doesn't exist in the DB.
	tok, err := secrets.Mint(secrets.AgentserverTokenSpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	body := bytes.NewReader([]byte(`{"token":"` + tok.Full + `"}`))
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
