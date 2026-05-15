package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteVerifier_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Secret"); got != "s3cret" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/internal/codex/tokens/verify" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(body.Token, "ast_") {
			t.Errorf("body token = %q", body.Token)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"user_id": "u1", "workspace_id": "ws_a",
		})
	}))
	defer srv.Close()

	v := NewRemoteVerifier(srv.URL, "s3cret")
	id, err := v.Verify(context.Background(), "ast_a3k9_secret")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.UserID != "u1" || id.WorkspaceID != "ws_a" {
		t.Errorf("identity = %+v", id)
	}
}

func TestRemoteVerifier_401_ReturnsErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	v := NewRemoteVerifier(srv.URL, "s")
	_, err := v.Verify(context.Background(), "ast_x_y")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRemoteVerifier_500_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	v := NewRemoteVerifier(srv.URL, "s")
	_, err := v.Verify(context.Background(), "ast_x_y")
	if err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want non-401 error, got %v", err)
	}
}

func TestRemoteVerifier_NetworkError(t *testing.T) {
	v := NewRemoteVerifier("http://127.0.0.1:1", "s")
	v.httpClient.Timeout = 200 * time.Millisecond
	_, err := v.Verify(context.Background(), "ast_x_y")
	if err == nil {
		t.Fatal("want error on unreachable host")
	}
}
