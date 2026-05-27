package auth

import "strings"

// ResolveWorkspaceSlugFromHost returns the workspace slug when host is
// <slug>.<baseDomain>. Returns "" on apex, sandbox subdomains (claw-*, hermes-*),
// or reserved hosts (e.g. codex-auth).
func ResolveWorkspaceSlugFromHost(host string, baseDomains []string, openclawPrefix, hermesPrefix, codexAuthHost string) string {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if codexAuthHost != "" && host == strings.ToLower(codexAuthHost) {
		return ""
	}

	for _, base := range baseDomains {
		base = strings.ToLower(strings.TrimSpace(base))
		if base == "" {
			continue
		}
		if host == base || host == "www."+base {
			return ""
		}
		suffix := "." + base
		if !strings.HasSuffix(host, suffix) {
			continue
		}
		label := strings.TrimSuffix(host, suffix)
		if label == "" || strings.Contains(label, ".") {
			return ""
		}
		if openclawPrefix != "" && strings.HasPrefix(label, openclawPrefix+"-") {
			return ""
		}
		if hermesPrefix != "" && strings.HasPrefix(label, hermesPrefix+"-") {
			return ""
		}
		return label
	}
	return ""
}

// HostOnlySessionCookie reports whether the session cookie should omit
// Domain (scoped to the tenant subdomain only).
func HostOnlySessionCookie(workspaceSlug string) bool {
	return workspaceSlug != ""
}
