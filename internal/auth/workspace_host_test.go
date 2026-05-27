package auth

import "testing"

func TestResolveWorkspaceSlugFromHost(t *testing.T) {
	bases := []string{"agentserver.analytics.vtex.com"}
	openclaw := "claw"
	hermes := "hermes"

	tests := []struct {
		host string
		want string
	}{
		{"empresa-a.agentserver.analytics.vtex.com", "empresa-a"},
		{"agentserver.analytics.vtex.com", ""},
		{"www.agentserver.analytics.vtex.com", ""},
		{"claw-x1.agentserver.analytics.vtex.com", ""},
		{"hermes-foo.agentserver.analytics.vtex.com", ""},
		{"codex-auth.agentserver.analytics.vtex.com", ""},
		{"localhost:8080", ""},
	}
	for _, tc := range tests {
		got := ResolveWorkspaceSlugFromHost(tc.host, bases, openclaw, hermes, "codex-auth.agentserver.analytics.vtex.com")
		if got != tc.want {
			t.Errorf("ResolveWorkspaceSlugFromHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestHostOnlySessionCookie(t *testing.T) {
	if !HostOnlySessionCookie("empresa-a") {
		t.Fatal("expected host-only for tenant slug")
	}
	if HostOnlySessionCookie("") {
		t.Fatal("expected shared cookie domain on apex")
	}
}
