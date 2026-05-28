// Package notif implements outbound notifications (email, etc.) for
// agentserver. v1 ships only the invite email; future B-series tasks
// extend with password-reset, sandbox alerts, etc.
package notif

import (
	"context"
	"log"
	"os"
)

// Mailer abstracts the outbound email backend so tests + dev can use a
// no-op logger while prod uses SES/Resend/Mailgun.
type Mailer interface {
	SendInvite(ctx context.Context, msg InviteMessage) error
}

// InviteMessage carries the fields needed to render the invite email.
type InviteMessage struct {
	To            string
	WorkspaceName string
	WorkspaceSlug string
	Role          string
	InviteURL     string
	ExpiresAt     string // pre-formatted "2026-06-03 12:34 UTC"
	InvitedByName string // optional; falls back to "an admin"
}

// LoadFromEnv picks a Mailer implementation based on NOTIF_MAILER:
//
//	"dev"  → DevMailer (default; logs to stdout)
//	"ses"  → SESMailer (TODO — implement in B01 follow-up if used)
//
// Returns DevMailer if NOTIF_MAILER is empty or unknown so the server
// stays useful in dev without extra config.
func LoadFromEnv() Mailer {
	switch os.Getenv("NOTIF_MAILER") {
	case "ses":
		log.Printf("notif: SES mailer not yet implemented, falling back to dev")
		return &DevMailer{}
	default:
		return &DevMailer{}
	}
}

// DevMailer logs invite mails to stdout. Useful for local development +
// CI; never use in production (it does not actually send email).
type DevMailer struct{}

// SendInvite logs the invite details. Returns nil always.
func (*DevMailer) SendInvite(_ context.Context, msg InviteMessage) error {
	log.Printf(
		"[invite-mail:dev] to=%s ws=%s slug=%s role=%s expires=%s url=%s",
		msg.To, msg.WorkspaceName, msg.WorkspaceSlug, msg.Role, msg.ExpiresAt, msg.InviteURL,
	)
	return nil
}

// BuildInviteURL constructs a workspace-scoped invite URL.
//
//	BuildInviteURL("empresa-a", "agentserver.dev", "abc123")
//	  → "https://empresa-a.agentserver.dev/accept-invite?token=abc123"
func BuildInviteURL(slug, baseDomain, token string) string {
	return BuildTenantURL(slug, baseDomain, "/accept-invite?token="+token)
}
