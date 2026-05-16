package codexappgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

// stubSup implements just enough of *supervisor.Supervisor for the
// loopback-token lookup branch.
type stubSup struct {
	tokens map[string]string // token → workspace_id
}

func (s *stubSup) LookupWorkspaceForLoopbackToken(tok string) (string, bool) {
	w, ok := s.tokens[tok]
	return w, ok
}

// supLookup is a tiny adapter to let internal_api_test use stubSup
// without instantiating the full Server.NewServer chain (which would
// need S3, agentserver, etc.). We construct a minimal Server-shaped
// struct via a local helper rather than touching production NewServer.

func newInternalAPITest(t *testing.T, tokens map[string]string, connectedFn func(string) ([]execmodel.ConnectedExecutor, error)) *httptest.Server {
	t.Helper()
	srv := &testInternalServer{
		tokens:  tokens,
		connect: connectedFn,
	}
	hs := httptest.NewServer(http.HandlerFunc(srv.handleInternalConnected))
	t.Cleanup(hs.Close)
	return hs
}

type testInternalServer struct {
	tokens  map[string]string
	connect func(string) ([]execmodel.ConnectedExecutor, error)
}

// handleInternalConnected mirrors Server.handleInternalConnected. Kept
// in sync manually; if production handler changes, this fixture must
// too. Alternative would be to wire the real Server in tests, but the
// full-server chain pulls in S3 + agentserver, so the duplication is
// the cheaper trade-off here.
func (s *testInternalServer) handleInternalConnected(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tok := r.Header.Get("X-Loopback-Token")
	if tok == "" {
		http.Error(w, "missing X-Loopback-Token", http.StatusUnauthorized)
		return
	}
	wid, ok := s.tokens[tok]
	if !ok {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	list, err := s.connect(wid)
	if err != nil {
		http.Error(w, "list", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []execmodel.ConnectedExecutor{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func TestInternalConnected_RequiresLoopbackToken(t *testing.T) {
	hs := newInternalAPITest(t, map[string]string{"lb1": "ws_a"}, func(string) ([]execmodel.ConnectedExecutor, error) {
		return nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInternalConnected_RejectsUnknownToken(t *testing.T) {
	hs := newInternalAPITest(t, map[string]string{"lb1": "ws_a"}, func(string) ([]execmodel.ConnectedExecutor, error) {
		return nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL, nil)
	req.Header.Set("X-Loopback-Token", "garbage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInternalConnected_ReturnsList(t *testing.T) {
	last := time.Now().UTC().Truncate(time.Second)
	hs := newInternalAPITest(t, map[string]string{"lb1": "ws_a"}, func(wid string) ([]execmodel.ConnectedExecutor, error) {
		if wid != "ws_a" {
			t.Errorf("connect called with %q", wid)
		}
		return []execmodel.ConnectedExecutor{
			{ExeID: "exe_1", Description: "test-host", LastSeenAt: &last},
		}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL, nil)
	req.Header.Set("X-Loopback-Token", "lb1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var got []execmodel.ConnectedExecutor
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ExeID != "exe_1" {
		t.Errorf("got %+v", got)
	}
}

func TestIsLoopbackRemote(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:54321", true},
		{"127.5.5.5:1", true},
		{"[::1]:1234", true},
		{"10.0.0.1:8080", false},
		{"192.168.0.1:80", false},
		{"[fe80::1]:80", false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := isLoopbackRemote(c.addr); got != c.want {
			t.Errorf("isLoopbackRemote(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestInternalConnected_RejectsNonLoopback uses a contrived request
// constructed with a fake non-loopback RemoteAddr to exercise the
// IP-based reject path. Real httptest.Server always serves over
// 127.0.0.1, so we have to use httptest.NewRecorder + a hand-built
// request here.
func TestInternalConnected_RejectsNonLoopback(t *testing.T) {
	srv := &testInternalServer{
		tokens:  map[string]string{"lb1": "ws_a"},
		connect: func(string) ([]execmodel.ConnectedExecutor, error) { return nil, nil },
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/connected", nil)
	req.RemoteAddr = "10.0.0.5:33445" // pretend to come from the cluster network
	req.Header.Set("X-Loopback-Token", "lb1")
	rr := httptest.NewRecorder()
	srv.handleInternalConnected(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
}
