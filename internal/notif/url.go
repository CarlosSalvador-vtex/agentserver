package notif

import "fmt"

// BuildTenantURL returns an absolute HTTPS URL for a tenant-scoped path.
// When slug is non-empty, the host is {slug}.{baseDomain}; otherwise https://{baseDomain}.
// path must include a leading slash; query strings belong in path.
func BuildTenantURL(slug, baseDomain, path string) string {
	host := baseDomain
	if slug != "" {
		host = slug + "." + baseDomain
	}
	return fmt.Sprintf("https://%s%s", host, path)
}
