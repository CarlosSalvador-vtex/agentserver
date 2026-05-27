package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

func isValidSandboxType(t string) bool {
	return t == "custom"
}

func TestSandboxTypeValidation_Logic(t *testing.T) {
	tests := []struct {
		sandboxType string
		wantValid   bool
	}{
		{"custom", true},
		{"openclaw", false},
		{"hermes", false},
		{"unknown", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.sandboxType, func(t *testing.T) {
			if got := isValidSandboxType(tc.sandboxType); got != tc.wantValid {
				t.Errorf("isValidSandboxType(%q) = %v, want %v", tc.sandboxType, got, tc.wantValid)
			}
		})
	}
}

func TestAgentRegister_TypeValidation_Integration(t *testing.T) {
	dbURL := testDatabaseURL(t)
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	database, err := db.Open(dbURL)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	const testWorkspaceIDVal = "ws-register-test"
	const testUserID = "user-register-test"

	if err = database.EnsureWorkspace(testWorkspaceIDVal, "Register Test Workspace"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { database.Exec(`DELETE FROM workspaces WHERE id = $1`, testWorkspaceIDVal) })

	_, err = database.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer') ON CONFLICT DO NOTHING`,
		testWorkspaceIDVal, testUserID,
	)
	if err != nil {
		t.Fatalf("add workspace member: %v", err)
	}
	t.Cleanup(func() {
		database.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceIDVal, testUserID)
	})

	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		json.NewEncoder(w).Encode(auth.IntrospectionResult{
			Active:  true,
			Subject: testUserID,
			Scope:   "agent:register",
			Extra:   map[string]interface{}{"workspace_id": testWorkspaceIDVal},
		})
	}))
	defer hydra.Close()

	s := &Server{
		DB:          database,
		HydraClient: auth.NewHydraClient(hydra.URL, hydra.URL),
	}

	tests := []struct {
		name        string
		sandboxType string
		wantStatus  int
	}{
		{"custom accepted", "custom", http.StatusCreated},
		{"invalid type rejected", "openclaw", http.StatusBadRequest},
		{"invalid type rejected empty defaults to custom", "", http.StatusCreated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"name": "test-agent", "type": tc.sandboxType})
			req := httptest.NewRequest(http.MethodPost, "/api/agent/register", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer fake-token")
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			s.handleAgentRegister(rr, req)
			if rr.Code != tc.wantStatus {
				t.Errorf("type %q: got status %d, want %d (body: %s)",
					tc.sandboxType, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				msg := rr.Body.String()
				if !bytes.Contains([]byte(msg), []byte("custom")) {
					t.Errorf("error message missing custom: %s", msg)
				}
			}
		})
	}
}
