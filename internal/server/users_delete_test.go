package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

func newAdminDeleteUserTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM audit_events`)
		_, _ = d.Exec(`DELETE FROM auth_tokens`)
		_, _ = d.Exec(`DELETE FROM user_credentials`)
		_, _ = d.Exec(`DELETE FROM workspace_members`)
		_, _ = d.Exec(`DELETE FROM workspaces`)
		_, _ = d.Exec(`DELETE FROM users`)
	})
	return &Server{DB: d, Auth: auth.New(d)}, d
}

func insertUserForDeleteTest(t *testing.T, d *db.DB, id, email, role string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO users (id, email, role) VALUES ($1, $2, $3)`, id, email, role)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func TestDeleteUser_requiresAdmin(t *testing.T) {
	srv, d := newAdminDeleteUserTestServer(t)
	insertUserForDeleteTest(t, d, "usr-admin-del", "admin@example.com", "admin")
	insertUserForDeleteTest(t, d, "usr-regular-del", "regular@example.com", "user")

	req := reqWithUser(http.MethodDelete, "/api/admin/users/usr-target-del", "usr-regular-del", nil, nil)
	req = withChiURLParam(req, "id", "usr-target-del")
	rr := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestDeleteUser_notFound(t *testing.T) {
	srv, d := newAdminDeleteUserTestServer(t)
	insertUserForDeleteTest(t, d, "usr-admin-del", "admin@example.com", "admin")

	req := reqWithUser(http.MethodDelete, "/api/admin/users/usr-missing-del", "usr-admin-del", nil, nil)
	req = withChiURLParam(req, "id", "usr-missing-del")
	rr := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestDeleteUser_lastOwnerConflict(t *testing.T) {
	srv, d := newAdminDeleteUserTestServer(t)
	insertUserForDeleteTest(t, d, "usr-admin-del", "admin@example.com", "admin")
	insertUserForDeleteTest(t, d, "usr-owner-del", "owner@example.com", "user")

	_, err := d.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES ('ws-solo-del', 'Solo', 'usr-owner-del')`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	_, err = d.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ('ws-solo-del', 'usr-owner-del', 'owner')`)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	req := reqWithUser(http.MethodDelete, "/api/admin/users/usr-owner-del", "usr-admin-del", nil, nil)
	req = withChiURLParam(req, "id", "usr-owner-del")
	rr := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	ws, ok := body["workspaces"].([]any)
	if !ok || len(ws) != 1 {
		t.Fatalf("expected workspaces array with one entry, got %+v", body["workspaces"])
	}
}

func TestDeleteUser_alreadyAnonymized(t *testing.T) {
	srv, d := newAdminDeleteUserTestServer(t)
	insertUserForDeleteTest(t, d, "usr-admin-del", "admin@example.com", "admin")

	targetID := "usr-anon-del"
	_, err := d.Exec(`INSERT INTO users (id, email, role, anonymized_at) VALUES ($1, 'gone@example.com', 'user', NOW())`, targetID)
	if err != nil {
		t.Fatalf("insert anonymized user: %v", err)
	}

	req := reqWithUser(http.MethodDelete, "/api/admin/users/"+targetID, "usr-admin-del", nil, nil)
	req = withChiURLParam(req, "id", targetID)
	rr := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rr, req)
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
}

func TestDeleteUser_success(t *testing.T) {
	srv, d := newAdminDeleteUserTestServer(t)
	insertUserForDeleteTest(t, d, "usr-admin-del", "admin@example.com", "admin")

	targetID := "usr-victim-del"
	email := "victim@example.com"
	insertUserForDeleteTest(t, d, targetID, email, "user")
	_, err := d.Exec(`INSERT INTO user_credentials (user_id, provider, subject, email) VALUES ($1, 'github', 'gh-del', $2)`, targetID, email)
	if err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	_, err = d.Exec(`INSERT INTO auth_tokens (user_id, token_hash, expires_at) VALUES ($1, 'cafebabe', NOW() + INTERVAL '1 day')`, targetID)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	req := reqWithUser(http.MethodDelete, "/api/admin/users/"+targetID, "usr-admin-del", nil, nil)
	req = withChiURLParam(req, "id", targetID)
	rr := httptest.NewRecorder()
	srv.handleAdminDeleteUser(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}

	ctx := context.Background()
	var gotEmail string
	var anonymizedAt *string
	err = d.QueryRowContext(ctx, `SELECT email, anonymized_at::text FROM users WHERE id = $1`, targetID).Scan(&gotEmail, &anonymizedAt)
	if err != nil {
		t.Fatalf("select user: %v", err)
	}
	if anonymizedAt == nil || *anonymizedAt == "" {
		t.Fatal("expected anonymized_at set")
	}
	if gotEmail != "deleted-"+targetID+"@anonymized.local" {
		t.Fatalf("email = %q, want placeholder", gotEmail)
	}

	var credCount, tokCount int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_credentials WHERE user_id = $1`, targetID).Scan(&credCount)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_tokens WHERE user_id = $1`, targetID).Scan(&tokCount)
	if credCount != 0 || tokCount != 0 {
		t.Fatalf("expected credentials and tokens removed, cred=%d tok=%d", credCount, tokCount)
	}
}
