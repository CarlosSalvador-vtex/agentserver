package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecutorsClient_Register(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex-exec/register" || r.Method != http.MethodPost {
			t.Errorf("path=%q method=%q", r.URL.Path, r.Method)
		}
		if r.Header.Get("X-Internal-Secret") != "is" {
			t.Errorf("missing X-Internal-Secret")
		}
		if r.Header.Get("X-User-Id") != "user_a" {
			t.Errorf("missing X-User-Id")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Daisy MBP") {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegisterExecutorResponse{ExeID: "exe_xxx"})
	}))
	defer srv.Close()

	c := NewExecutorsClient(srv.URL, "is")
	resp, err := c.Register(context.Background(), "user_a", RegisterExecutorRequest{DisplayName: "Daisy MBP"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.ExeID != "exe_xxx" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestExecutorsClient_Bind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex-exec/workspaces/ws_a/executors" || r.Method != http.MethodPost {
			t.Errorf("path=%q method=%q", r.URL.Path, r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"exe_id":"exe_x"`) {
			t.Errorf("body = %s", body)
		}
		if !strings.Contains(string(body), `"is_default":true`) {
			t.Errorf("body missing is_default: %s", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewExecutorsClient(srv.URL, "is")
	if err := c.Bind(context.Background(), "user_a", "ws_a", "exe_x", "alpha", "alpha host", true); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

func TestExecutorsClient_Unbind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex-exec/workspaces/ws_a/executors/exe_x" || r.Method != http.MethodDelete {
			t.Errorf("path=%q method=%q", r.URL.Path, r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewExecutorsClient(srv.URL, "is")
	if err := c.Unbind(context.Background(), "user_a", "ws_a", "exe_x"); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
}

func TestExecutorsClient_List(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex-exec/workspaces/ws_a/executors" || r.Method != http.MethodGet {
			t.Errorf("path=%q method=%q", r.URL.Path, r.Method)
		}
		_, _ = w.Write([]byte(`[{"exe_id":"exe_x","name":"alpha","description":"d","is_default":true}]`))
	}))
	defer srv.Close()

	c := NewExecutorsClient(srv.URL, "is")
	rows, err := c.List(context.Background(), "user_a", "ws_a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ExeID != "exe_x" || !rows[0].IsDefault {
		t.Errorf("rows = %+v", rows)
	}
}

func TestExecutorsClient_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewExecutorsClient(srv.URL, "is")
	if _, err := c.Register(context.Background(), "u", RegisterExecutorRequest{}); err == nil {
		t.Fatal("want error")
	}
}
