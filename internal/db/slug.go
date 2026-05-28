package db

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	minSlugLen = 2
	maxSlugLen = 63
)

var (
	slugRe         = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	nonAlnumRe     = regexp.MustCompile(`[^a-z0-9]+`)
	reservedSlugs = map[string]struct{}{
		// Core platform (existing)
		"www": {}, "api": {}, "admin": {}, "app": {},
		"root": {}, "auth": {}, "login": {}, "register": {},
		"static": {}, "assets": {}, "agentserver": {},
		"openclaw": {}, "hermes": {},
		// Operational / email
		"mail": {}, "email": {}, "mailbox": {}, "mx": {}, "smtp": {}, "imap": {}, "pop": {},
		// Support
		"support": {}, "help": {}, "helpdesk": {}, "kb": {}, "faq": {}, "contact": {},
		// Status / monitoring
		"status": {}, "health": {}, "metrics": {}, "dashboard": {}, "grafana": {}, "prometheus": {},
		// Docs
		"docs": {}, "documentation": {}, "wiki": {}, "blog": {}, "news": {},
		// Infra
		"cdn": {}, "proxy": {}, "ingress": {}, "lb": {}, "node": {}, "pod": {}, "k8s": {},
		// Commercial
		"billing": {}, "pay": {}, "payments": {}, "pricing": {}, "enterprise": {}, "sales": {},
		// Marketing
		"signup": {}, "trial": {}, "demo": {}, "marketing": {}, "landing": {},
		// Compliance / legal
		"legal": {}, "terms": {}, "privacy": {}, "tos": {}, "gdpr": {}, "lgpd": {}, "compliance": {},
		// Resources
		"media": {}, "images": {}, "files": {}, "download": {}, "upload": {},
		// Special hosts
		"localhost": {}, "internal": {}, "external": {}, "public": {}, "private": {},
	}
	reservedPrefixes = []string{"claw-", "hermes-"}
)

// ValidateSlug rejects slugs that would not be safe as a subdomain label.
func ValidateSlug(s string) error {
	if len(s) < minSlugLen || len(s) > maxSlugLen {
		return fmt.Errorf("slug must be %d-%d chars", minSlugLen, maxSlugLen)
	}
	if !slugRe.MatchString(s) {
		return fmt.Errorf("slug must be lowercase kebab-case (a-z, 0-9, -)")
	}
	if _, reserved := reservedSlugs[s]; reserved {
		return fmt.Errorf("%q is reserved", s)
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(s, p) {
			return fmt.Errorf("slugs starting with %q are reserved (sandbox subdomain prefix)", p)
		}
	}
	return nil
}

// Slugify converts an arbitrary name to a candidate slug. The result is
// not guaranteed unique or non-reserved — callers must validate and dedupe.
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonAlnumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "workspace"
	}
	return s
}
