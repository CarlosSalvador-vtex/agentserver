package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyValidator_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != "test-secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workspace_id": "ws-123",
			"key_id":       "wak_abc",
			"scopes":       []string{"turns:submit"},
		})
	}))
	defer srv.Close()

	v := NewAPIKeyValidator(srv.URL, "test-secret")
	got, err := v.Validate(context.Background(), "wak_abc_xyz")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.WorkspaceID != "ws-123" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-123")
	}
	if got.KeyID != "wak_abc" {
		t.Errorf("KeyID = %q, want %q", got.KeyID, "wak_abc")
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "turns:submit" {
		t.Errorf("Scopes = %v, want [turns:submit]", got.Scopes)
	}
}

func TestAPIKeyValidator_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := NewAPIKeyValidator(srv.URL, "x")
	_, err := v.Validate(context.Background(), "wak_invalid")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAPIKeyValidator_NilBaseURL(t *testing.T) {
	v := NewAPIKeyValidator("", "secret")
	_, err := v.Validate(context.Background(), "wak_abc_xyz")
	if err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

func TestAPIKeyValidator_ScopesPropagated(t *testing.T) {
	scopes := []string{"turns:submit", "threads:read"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workspace_id": "ws-456",
			"key_id":       "wak_def",
			"scopes":       scopes,
		})
	}))
	defer srv.Close()

	v := NewAPIKeyValidator(srv.URL, "any")
	got, err := v.Validate(context.Background(), "wak_def_something")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "turns:submit" || got.Scopes[1] != "threads:read" {
		t.Errorf("Scopes = %v, want %v", got.Scopes, scopes)
	}
}
