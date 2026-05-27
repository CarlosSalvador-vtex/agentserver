package sandboxproxy

import (
	"os"
	"strings"
)

// Config holds sandbox-proxy configuration loaded from environment variables.
type Config struct {
	DatabaseURL             string
	ListenAddr              string
	BaseDomains             []string
	OpenclawSubdomainPrefix string
	HermesSubdomainPrefix   string
	// AgentserverUpstream is the internal URL of the agentserver service.
	// When set, non-claw/non-hermes subdomain traffic (i.e. tenant slug hosts
	// like empresa-a.<base>) is reverse-proxied to this upstream so the
	// agentserver login/UI handlers serve those hosts. Empty disables the
	// fallthrough (legacy behavior — wildcard returns 404 for unknown subs).
	AgentserverUpstream string
}

// LoadConfigFromEnv reads configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		ListenAddr:              os.Getenv("LISTEN_ADDR"),
		OpenclawSubdomainPrefix: os.Getenv("OPENCLAW_SUBDOMAIN_PREFIX"),
		HermesSubdomainPrefix:   os.Getenv("HERMES_SUBDOMAIN_PREFIX"),
		AgentserverUpstream:     os.Getenv("AGENTSERVER_UPSTREAM"),
	}

	if raw := os.Getenv("BASE_DOMAIN"); raw != "" {
		for _, d := range strings.Split(raw, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				cfg.BaseDomains = append(cfg.BaseDomains, d)
			}
		}
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8082"
	}
	if cfg.OpenclawSubdomainPrefix == "" {
		cfg.OpenclawSubdomainPrefix = "claw"
	}
	if cfg.HermesSubdomainPrefix == "" {
		cfg.HermesSubdomainPrefix = "hermes"
	}
	return cfg
}
