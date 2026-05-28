package notif

import "testing"

func TestBuildTenantURL(t *testing.T) {
	cases := map[string]struct {
		slug, path, want string
	}{
		"with slug": {
			slug: "empresa-a", path: "/login", want: "https://empresa-a.agentserver.com/login",
		},
		"no slug": {
			slug: "", path: "/login", want: "https://agentserver.com/login",
		},
		"deep path": {
			slug: "acme", path: "/w/abc/sandboxes/xyz", want: "https://acme.agentserver.com/w/abc/sandboxes/xyz",
		},
		"with query": {
			slug: "acme", path: "/accept-invite?token=xxx", want: "https://acme.agentserver.com/accept-invite?token=xxx",
		},
	}
	base := "agentserver.com"
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildTenantURL(tc.slug, base, tc.path)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
