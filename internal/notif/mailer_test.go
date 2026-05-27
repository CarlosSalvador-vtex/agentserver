package notif

import (
	"context"
	"testing"
)

func TestBuildInviteURL(t *testing.T) {
	cases := map[string]struct {
		slug, base, tok, want string
	}{
		"with slug": {
			"empresa-a", "agentserver.dev", "tok-1",
			"https://empresa-a.agentserver.dev/accept-invite?token=tok-1",
		},
		"empty slug falls back to base": {
			"", "agentserver.dev", "tok-2",
			"https://agentserver.dev/accept-invite?token=tok-2",
		},
		"long base": {
			"acme", "agentserver.analytics.vtex.com", "tok-3",
			"https://acme.agentserver.analytics.vtex.com/accept-invite?token=tok-3",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildInviteURL(tc.slug, tc.base, tc.tok)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDevMailer_SendInvite(t *testing.T) {
	m := &DevMailer{}
	err := m.SendInvite(context.Background(), InviteMessage{
		To: "x@example.com", WorkspaceName: "X", WorkspaceSlug: "x",
		Role: "developer", InviteURL: "https://x.example/accept",
	})
	if err != nil {
		t.Fatalf("DevMailer must not error, got %v", err)
	}
}
