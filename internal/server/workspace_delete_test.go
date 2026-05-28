package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/sbxstore"
)

func newDeleteWorkspaceTestServer(t *testing.T) *Server {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		d.Exec(`DELETE FROM skill_drafts WHERE workspace_id = 'w-1'`)
		d.Exec(`DELETE FROM soul_drafts WHERE workspace_id = 'w-1'`)
		d.Exec(`DELETE FROM workspace_members WHERE workspace_id = 'w-1'`)
		d.Exec(`DELETE FROM workspaces WHERE id = 'w-1'`)
		d.Exec(`DELETE FROM users WHERE id IN ('u-1', 'u-2')`)
	})
	return &Server{DB: d, Sandboxes: sbxstore.NewStore(d)}
}

func TestHandleDeleteWorkspaceOwnerOnly(t *testing.T) {
	srv := newDeleteWorkspaceTestServer(t)
	seedWorkspaceMember(t, srv.DB, "w-1", "u-1", "owner")
	if _, err := srv.DB.Exec(`INSERT INTO users (id, email) VALUES ('u-2', 'u-2@test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("insert u-2: %v", err)
	}
	if _, err := srv.DB.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ('w-1', 'u-2', 'developer') ON CONFLICT DO NOTHING`,
	); err != nil {
		t.Fatalf("add developer member: %v", err)
	}

	// developer cannot delete
	reqDev := reqWithUser(http.MethodDelete, "/api/workspaces/w-1", "u-2", nil, map[string]string{"id": "w-1"})
	rrDev := httptest.NewRecorder()
	srv.handleDeleteWorkspace(rrDev, reqDev)
	if rrDev.Code != http.StatusForbidden {
		t.Fatalf("dev should get 403, got %d — %s", rrDev.Code, rrDev.Body.String())
	}

	// owner can delete
	reqOwner := reqWithUser(http.MethodDelete, "/api/workspaces/w-1", "u-1", nil, map[string]string{"id": "w-1"})
	rrOwner := httptest.NewRecorder()
	srv.handleDeleteWorkspace(rrOwner, reqOwner)
	if rrOwner.Code != http.StatusNoContent {
		t.Fatalf("owner should get 204, got %d — %s", rrOwner.Code, rrOwner.Body.String())
	}

	// gone from GET
	reqGet := reqWithUser(http.MethodGet, "/api/workspaces/w-1", "u-1", nil, map[string]string{"id": "w-1"})
	rrGet := httptest.NewRecorder()
	srv.handleGetWorkspace(rrGet, reqGet)
	if rrGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d — %s", rrGet.Code, rrGet.Body.String())
	}
}
