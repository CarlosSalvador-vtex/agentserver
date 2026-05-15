package codexappgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

func TestExecGatewayClient_Connected_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	want := []execmodel.ConnectedExecutor{
		{ExeID: "exe_a", Description: "alpha", DefaultCwd: "/a", IsDefault: true, LastSeenAt: &now},
		{ExeID: "exe_b", Description: "beta", DefaultCwd: "/b", IsDefault: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer s3cret" {
			t.Errorf("auth header = %q", got)
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/exec-gateway/connected" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("workspace_id"); got != "ws_a" {
			t.Errorf("workspace_id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := NewExecGatewayClient(srv.URL, "s3cret")
	got, err := c.Connected(context.Background(), "ws_a")
	if err != nil {
		t.Fatalf("Connected: %v", err)
	}
	if len(got) != 2 || got[0].ExeID != "exe_a" || !got[0].IsDefault || got[1].ExeID != "exe_b" {
		t.Fatalf("got = %+v", got)
	}
}

func TestExecGatewayClient_Connected_RejectsEmptyWorkspaceID(t *testing.T) {
	c := NewExecGatewayClient("http://example.invalid", "s")
	if _, err := c.Connected(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty workspace_id")
	}
}

func TestExecGatewayClient_Connected_PropagatesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewExecGatewayClient(srv.URL, "s")
	_, err := c.Connected(context.Background(), "ws_a")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestExecGatewayClient_Connected_TolerantToTrailingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/exec-gateway/connected" {
			t.Errorf("path = %q (trailing slash should be normalised)", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewExecGatewayClient(srv.URL+"/", "s")
	if _, err := c.Connected(context.Background(), "ws_a"); err != nil {
		t.Fatalf("Connected: %v", err)
	}
}
