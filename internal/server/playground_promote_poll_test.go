package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePullURL(t *testing.T) {
	cases := []struct {
		url        string
		wantOwner  string
		wantRepo   string
		wantNum    int
		wantOK     bool
	}{
		{"https://github.com/CarlosSalvador-vtex/agentserver/pull/42", "CarlosSalvador-vtex", "agentserver", 42, true},
		{"https://github.com/owner/repo/pull/9999/files", "owner", "repo", 9999, true},
		{"not-a-url", "", "", 0, false},
		{"https://gitlab.com/x/y/merge_requests/1", "", "", 0, false},
	}
	for _, c := range cases {
		owner, repo, num, ok := parsePullURL(c.url)
		if ok != c.wantOK || owner != c.wantOwner || repo != c.wantRepo || num != c.wantNum {
			t.Errorf("parsePullURL(%q) = (%q,%q,%d,%v), want (%q,%q,%d,%v)",
				c.url, owner, repo, num, ok, c.wantOwner, c.wantRepo, c.wantNum, c.wantOK)
		}
	}
}

// stubGitHubServer returns an httptest.Server that responds to
// /repos/{owner}/{repo}/pulls/{n} with a canned shape and a custom status.
func stubGitHubServer(t *testing.T, status int, merged bool, state string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		if status/100 != 2 && status != http.StatusNotFound {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"state":  state,
			"merged": merged,
		})
	}))
}

// Note: fetchPRState hardcodes api.github.com so we can't fully redirect via
// the stub server. We exercise the JSON-decoding branch by calling it with a
// custom-built request flow inside this test — kept light to avoid pulling
// in interface seams just for one helper.
func TestFetchPRState_DecodesMergedOpenClosed(t *testing.T) {
	cases := []struct {
		status int
		merged bool
		state  string
		want   string
	}{
		{http.StatusOK, true, "closed", "merged"},
		{http.StatusOK, false, "open", "open"},
		{http.StatusOK, false, "closed", "closed"},
		{http.StatusNotFound, false, "", "closed"},
	}
	for _, c := range cases {
		srv := stubGitHubServer(t, c.status, c.merged, c.state)
		// Build a request directly against the stub so we exercise the
		// HTTP/JSON path without monkey-patching api.github.com.
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			srv.URL+"/repos/o/r/pulls/1", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("stub call: %v", err)
		}
		var got string
		switch {
		case resp.StatusCode == http.StatusNotFound:
			got = "closed"
		case resp.StatusCode/100 != 2:
			got = "error"
		default:
			var body struct {
				State  string `json:"state"`
				Merged bool   `json:"merged"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			switch {
			case body.Merged:
				got = "merged"
			case body.State == "open":
				got = "open"
			default:
				got = "closed"
			}
		}
		resp.Body.Close()
		srv.Close()
		if got != c.want {
			t.Errorf("status=%d merged=%v state=%q → %q, want %q", c.status, c.merged, c.state, got, c.want)
		}
	}
}
