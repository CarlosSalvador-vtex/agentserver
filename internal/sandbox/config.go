package sandbox

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/agentserver/agentserver/internal/process"
)

// Config holds configuration for the K8s sandbox backend.
type Config struct {
	AgentserverNamespace     string
	Image                    string
	SessionStorageSize       string
	StorageClassName         string
	RuntimeClassName         string
	OpencodePort             int
	OpencodeConfigContent    string // JSON config injected via OPENCODE_CONFIG_CONTENT
	OpenclawImage            string
	OpenclawPort             int
	OpenclawRuntimeClassName string
	OpenclawWeixinEnabled    bool
	NanoclawImage            string
	NanoclawRuntimeClassName string
	NanoclawIMBridgeEnabled  bool
	NanoclawBridgeBaseURL    string // agentserver internal URL for NanoClaw pods to call back (e.g. "http://agentserver:8080")
	NanoclawModel            string // Claude Code model override (e.g. "claude-opus-4-6")
	GeminiProxyBaseURL       string // Gemini proxy base URL without path (e.g. "http://llmproxy:8081")
	ClaudeCodeImage            string
	ClaudeCodeRuntimeClassName string
	ClaudeCodePort             int    // default 7681 (ttyd)
	JupyterImage               string
	JupyterPort                int
	JupyterRuntimeClassName    string
	HermesImage                string
	HermesPort                 int
	HermesRuntimeClassName     string
	// HermesConfigMapName, when non-empty, is mounted at
	// /home/agent/.hermes/config.yaml inside hermes sandbox pods so the
	// container starts non-interactively with a preconfigured provider.
	HermesConfigMapName        string
	// HermesGLMAPIKey is injected as the GLM_API_KEY env var so the
	// zai/glm-5.1 fallback provider can authenticate.
	HermesGLMAPIKey            string
	// HermesServiceAccountRoleArn, when non-empty, makes the sandbox
	// manager ensure a ServiceAccount named "hermes" exists in the
	// workspace namespace, annotated with eks.amazonaws.com/role-arn
	// so the pod assumes the role via IRSA (for Bedrock access).
	HermesServiceAccountRoleArn string
	// CodexExecGatewayURL is the HTTP base URL the agentserver Python SDK
	// (installed in the jupyter image) dials for the REST exec-gateway.
	// Example: "http://agentserver-codex-exec-gateway.agentserver.svc:6060".
	// Empty leaves the SDK on its localhost default, which fails with
	// ECONNREFUSED inside a jupyter sandbox.
	CodexExecGatewayURL string
	AgentServerInternalURL     string // agentserver API URL for sandbox MCP bridge (e.g. "http://agentserver.agentserver.svc:8080")
	CredproxyPublicURL         string // URL sandboxes use to reach credentialproxy (e.g. "http://credentialproxy.agentserver.svc:8083")
	// Tolerations are applied to every sandbox pod template so they can schedule
	// onto tainted nodes. Loaded from SANDBOX_TOLERATIONS (JSON-encoded
	// []corev1.Toleration). Empty by default.
	Tolerations []corev1.Toleration
	// SkillConfigMaps maps a skill name → ConfigMap name that the manager
	// replicates into each workspace namespace and mounts inside hermes /
	// openclaw sandbox pods. Loaded from SANDBOX_SKILL_CONFIGMAPS as a
	// comma-separated list of "<skillName>=<configMapName>" pairs.
	SkillConfigMaps map[string]string
	// HermesWhatsappAllowed is injected as WHATSAPP_ALLOWED_USERS env on
	// hermes pods (comma-separated E.164 numbers). Empty disables the
	// allowlist (Hermes will deny all unauthenticated users by default).
	HermesWhatsappAllowed string
	// OpenclawWhatsappAllowed is injected into the openclaw config
	// (channels.whatsapp.allowFrom + plugins.entries.whatsapp). Comma list.
	OpenclawWhatsappAllowed string
}

// DefaultConfig returns a Config populated from environment variables with sensible defaults.
func DefaultConfig() Config {
	return Config{
		AgentserverNamespace:     envOrDefault("AGENTSERVER_NAMESPACE", "default"),
		Image:                    envOrDefault("AGENT_IMAGE", "agentserver-agent:latest"),
		SessionStorageSize:       envOrDefault("SESSION_STORAGE_SIZE", "5Gi"),
		StorageClassName:         os.Getenv("STORAGE_CLASS"),
		RuntimeClassName:         os.Getenv("RUNTIME_CLASS"),
		OpencodePort:             4096,
		OpencodeConfigContent:    os.Getenv("OPENCODE_CONFIG_CONTENT"),
		OpenclawImage:            os.Getenv("OPENCLAW_IMAGE"),
		OpenclawPort:             18789,
		OpenclawRuntimeClassName: os.Getenv("OPENCLAW_RUNTIME_CLASS"),
		OpenclawWeixinEnabled:    os.Getenv("OPENCLAW_WEIXIN_ENABLED") == "true",
		NanoclawImage:            os.Getenv("NANOCLAW_IMAGE"),
		NanoclawRuntimeClassName: os.Getenv("NANOCLAW_RUNTIME_CLASS"),
		NanoclawIMBridgeEnabled:  os.Getenv("NANOCLAW_IM_BRIDGE_ENABLED") == "true" || os.Getenv("NANOCLAW_WEIXIN_ENABLED") == "true",
		NanoclawBridgeBaseURL:    os.Getenv("NANOCLAW_BRIDGE_BASE_URL"),
		NanoclawModel:            os.Getenv("NANOCLAW_MODEL"),
		GeminiProxyBaseURL:       os.Getenv("GOOGLE_GEMINI_BASE_URL"),
		ClaudeCodeImage:            os.Getenv("CLAUDECODE_IMAGE"),
		ClaudeCodeRuntimeClassName: os.Getenv("CLAUDECODE_RUNTIME_CLASS"),
		ClaudeCodePort:             7681,
		JupyterImage:               os.Getenv("JUPYTER_IMAGE"),
		JupyterPort:                8888,
		JupyterRuntimeClassName:    os.Getenv("JUPYTER_RUNTIME_CLASS"),
		HermesImage:                 os.Getenv("HERMES_IMAGE"),
		HermesPort:                  9119, // Hermes Web UI / dashboard (gateway is on 8642)
		HermesRuntimeClassName:      os.Getenv("HERMES_RUNTIME_CLASS"),
		HermesConfigMapName:         os.Getenv("HERMES_CONFIGMAP_NAME"),
		HermesGLMAPIKey:             os.Getenv("HERMES_GLM_API_KEY"),
		HermesServiceAccountRoleArn: os.Getenv("HERMES_SA_ROLE_ARN"),
		CodexExecGatewayURL:        os.Getenv("CODEX_EXEC_GATEWAY_URL"),
		AgentServerInternalURL:     os.Getenv("AGENTSERVER_INTERNAL_URL"),
		CredproxyPublicURL:         os.Getenv("CREDPROXY_PUBLIC_URL"),
		Tolerations:                parseTolerations(os.Getenv("SANDBOX_TOLERATIONS")),
		SkillConfigMaps:            parseSkillConfigMaps(os.Getenv("SANDBOX_SKILL_CONFIGMAPS")),
		HermesWhatsappAllowed:      os.Getenv("HERMES_WHATSAPP_ALLOWED"),
		OpenclawWhatsappAllowed:    os.Getenv("OPENCLAW_WHATSAPP_ALLOWED"),
	}
}

// parseSkillConfigMaps reads SANDBOX_SKILL_CONFIGMAPS — a comma-separated list
// of "<skillName>=<configMapName>" pairs — into a map. Pairs missing the
// "=" separator are ignored. Empty input returns nil.
func parseSkillConfigMaps(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.Index(pair, "=")
		if eq <= 0 || eq == len(pair)-1 {
			log.Printf("sandbox: ignoring malformed skill pair %q in SANDBOX_SKILL_CONFIGMAPS", pair)
			continue
		}
		out[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseTolerations(s string) []corev1.Toleration {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var t []corev1.Toleration
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		log.Printf("sandbox: invalid SANDBOX_TOLERATIONS json, ignoring: %v", err)
		return nil
	}
	return t
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// BuildOpencodeConfig merges the per-sandbox proxy token into the base opencode
// config JSON. When overrideBaseURL is non-empty (BYOK mode), it also replaces
// provider.anthropic.options.baseURL.
func BuildOpencodeConfig(baseConfig, apiKey, overrideBaseURL string) string {
	// Parse the user-provided base config (from OPENCODE_CONFIG_CONTENT / values.yaml).
	var cfg map[string]interface{}
	if baseConfig != "" {
		if err := json.Unmarshal([]byte(baseConfig), &cfg); err != nil {
			cfg = make(map[string]interface{})
		}
	} else {
		cfg = make(map[string]interface{})
	}

	// Inject provider.anthropic.options.apiKey with per-sandbox token.
	if apiKey != "" {
		provider, _ := cfg["provider"].(map[string]interface{})
		if provider == nil {
			provider = make(map[string]interface{})
		}
		anthropic, _ := provider["anthropic"].(map[string]interface{})
		if anthropic == nil {
			anthropic = make(map[string]interface{})
		}
		options, _ := anthropic["options"].(map[string]interface{})
		if options == nil {
			options = make(map[string]interface{})
		}
		options["apiKey"] = apiKey
		if overrideBaseURL != "" {
			options["baseURL"] = overrideBaseURL
		}
		anthropic["options"] = options
		provider["anthropic"] = anthropic
		cfg["provider"] = provider
	}

	b, _ := json.Marshal(cfg)
	return string(b)
}

// ExtractProxyBaseURL extracts provider.anthropic.options.baseURL from the
// opencode config JSON. Used by sandbox managers that need the proxy URL
// (e.g. for openclaw config).
func ExtractProxyBaseURL(configJSON string) string {
	if configJSON == "" {
		return ""
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return ""
	}
	provider, _ := cfg["provider"].(map[string]interface{})
	if provider == nil {
		return ""
	}
	anthropic, _ := provider["anthropic"].(map[string]interface{})
	if anthropic == nil {
		return ""
	}
	options, _ := anthropic["options"].(map[string]interface{})
	if options == nil {
		return ""
	}
	baseURL, _ := options["baseURL"].(string)
	return baseURL
}

// OpenclawConfigOptions bundles optional extensions to the openclaw.json
// injection — plugin enable flags, WhatsApp allowlist, and the playground
// soul body. Keeps the BuildOpenclawConfig signature stable as features
// land.
type OpenclawConfigOptions struct {
	// EnabledPlugins becomes plugins.entries.<name> = { enabled: true } in
	// the injected config. The Node init wrapper deep-merges this into the
	// existing openclaw.json so bundled plugin manifests are preserved.
	EnabledPlugins []string
	// WhatsappAllowed becomes channels.whatsapp.allowFrom + implicitly
	// enables the bundled "whatsapp" plugin (added to EnabledPlugins).
	// Empty disables the WA channel entirely.
	WhatsappAllowed []string
	// WeixinEnabled re-enables the bundled "openclaw-weixin" plugin. The
	// OpenClaw image ships it enabled by default; we now always overwrite
	// plugins.entries (see below) to start sandboxes clean, so weixin is OFF
	// unless this flag is set (from sandbox.openclaw.weixinEnabled).
	WeixinEnabled bool
	// SoulBody is the persona body to inject as agent.systemPrompt in the
	// merged openclaw.json. Set by the playground composition layer; the
	// upstream openclaw agent reads agent.systemPrompt and prepends it to
	// every turn. Empty leaves the field unset (no override).
	SoulBody string
}

// BuildOpenclawConfig returns the openclaw.json content with gateway settings
// and optional Anthropic proxy credentials. The gatewayToken is written into
// gateway.auth.token so that the gateway and Control UI share the same secret;
// without this, OpenClaw v2026.3.12+ auto-generates a random token on startup
// that won't match the token our proxy injects.
//
// opts is optional — pass an empty OpenclawConfigOptions{} (or rely on the
// zero value) when no plugin / WhatsApp wiring is needed.
func BuildOpenclawConfig(proxyBaseURL, proxyToken, gatewayToken string, customModels []process.LLMModel, opts OpenclawConfigOptions) string {
	type modelDef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type provider struct {
		BaseURL string     `json:"baseUrl"`
		APIKey  string     `json:"apiKey"`
		API     string     `json:"api"`
		Models  []modelDef `json:"models"`
	}
	type gatewayAuth struct {
		Token string `json:"token,omitempty"`
	}
	type pluginEntry struct {
		Enabled bool `json:"enabled"`
	}
	type pluginInstall struct {
		Source      string `json:"source"`      // "local" for ConfigMap-mounted skills
		InstallPath string `json:"installPath"`
		Version     string `json:"version,omitempty"`
	}
	type whatsappChannel struct {
		Enabled   bool     `json:"enabled"`
		AllowFrom []string `json:"allowFrom,omitempty"`
	}
	type config struct {
		Gateway struct {
			Auth           *gatewayAuth `json:"auth,omitempty"`
			TrustedProxies []string     `json:"trustedProxies,omitempty"`
			ControlUI      struct {
				Enabled             bool `json:"enabled,omitempty"`
				AllowInsecureAuth   bool `json:"allowInsecureAuth,omitempty"`
				AllowOriginFallback bool `json:"dangerouslyAllowHostHeaderOriginFallback,omitempty"`
				DisableDeviceAuth   bool `json:"dangerouslyDisableDeviceAuth,omitempty"`
			} `json:"controlUi"`
		} `json:"gateway"`
		Models *struct {
			Providers map[string]provider `json:"providers"`
		} `json:"models,omitempty"`
		Plugins *struct {
			Entries  map[string]pluginEntry   `json:"entries"`
			Installs map[string]pluginInstall `json:"installs,omitempty"`
		} `json:"plugins,omitempty"`
		Channels *struct {
			Whatsapp *whatsappChannel `json:"whatsapp,omitempty"`
		} `json:"channels,omitempty"`
		Agent *struct {
			SystemPrompt string `json:"systemPrompt,omitempty"`
		} `json:"agent,omitempty"`
	}

	var c config
	if gatewayToken != "" {
		c.Gateway.Auth = &gatewayAuth{Token: gatewayToken}
	}
	// Trust cluster-internal proxy IPs so the gateway reads our injected
	// Authorization header and X-Forwarded-For on WebSocket upgrades.
	c.Gateway.TrustedProxies = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	c.Gateway.ControlUI.Enabled = true
	c.Gateway.ControlUI.AllowInsecureAuth = true
	c.Gateway.ControlUI.AllowOriginFallback = true
	c.Gateway.ControlUI.DisableDeviceAuth = true

	if proxyBaseURL != "" && proxyToken != "" {
		models := []modelDef{
			{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
			{ID: "claude-opus-4-5", Name: "Claude Opus 4.5"},
			{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
			{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"},
		}
		if len(customModels) > 0 {
			models = make([]modelDef, len(customModels))
			for i, m := range customModels {
				models[i] = modelDef{ID: m.ID, Name: m.Name}
			}
		}
		c.Models = &struct {
			Providers map[string]provider `json:"providers"`
		}{
			Providers: map[string]provider{
				"anthropic": {
					BaseURL: proxyBaseURL,
					APIKey:  proxyToken,
					API:     "anthropic-messages",
					Models:  models,
				},
			},
		}
	}

	// Plugins. If WhatsApp is enabled but the caller didn't list the
	// "whatsapp" plugin explicitly, add it implicitly so the channel
	// adapter is loaded. Each enabled plugin also gets a matching
	// installs entry pointing at the on-disk path the sandbox mount
	// produced — OpenClaw's plugin loader ignores `entries.<name>`
	// without a corresponding `installs.<name>` record ("stale config
	// entry" warning) and silently drops it on save.
	plugins := map[string]pluginEntry{}
	// Skill plugins land in /home/agent/.openclaw/extensions/<name>/ via
	// the ConfigMap mount (see manager.go::skillVolumesAndMounts). We do
	// NOT emit an `installs` entry for them: OpenClaw's plugin loader
	// treats path-source installs without an integrity hash as "stale"
	// and refuses to load the matching `entries.<name>` row. With no
	// `installs` row, the loader falls back to auto-discovery of the
	// extensions/ directory and picks the skill up like a bundled
	// plugin.
	for _, p := range opts.EnabledPlugins {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		plugins[p] = pluginEntry{Enabled: true}
	}
	// WhatsApp lives in the bundled extensions dir, not in plugins/.
	if len(opts.WhatsappAllowed) > 0 {
		plugins["whatsapp"] = pluginEntry{Enabled: true}
	}
	// Re-enable the bundled WeChat plugin only when explicitly asked.
	if opts.WeixinEnabled {
		plugins["openclaw-weixin"] = pluginEntry{Enabled: true}
	}
	// ALWAYS emit plugins.entries (even empty). The inject wrapper shallow-
	// merges this object over the image's openclaw.json, REPLACING its default
	// plugins set — the image ships "openclaw-weixin" enabled, so without this
	// a sandbox with no composed skills would silently boot with WeChat on.
	// Emitting (possibly empty) entries makes sandboxes start clean: only the
	// plugins we list here are enabled.
	c.Plugins = &struct {
		Entries  map[string]pluginEntry   `json:"entries"`
		Installs map[string]pluginInstall `json:"installs,omitempty"`
	}{Entries: plugins}

	// WhatsApp channel.
	if len(opts.WhatsappAllowed) > 0 {
		allow := make([]string, 0, len(opts.WhatsappAllowed))
		for _, n := range opts.WhatsappAllowed {
			if t := strings.TrimSpace(n); t != "" {
				allow = append(allow, t)
			}
		}
		c.Channels = &struct {
			Whatsapp *whatsappChannel `json:"whatsapp,omitempty"`
		}{
			Whatsapp: &whatsappChannel{
				Enabled:   true,
				AllowFrom: allow,
			},
		}
	}

	// Soul body: tried emitting agent.systemPrompt at root, but the
	// OpenClaw config schema rejects "agent" as unrecognized
	// ("<root>: Unrecognized key: \"agent\""). Until we find the
	// right schema field for system prompt injection, the soul.md
	// file mount (see manager.go::ResolveComposition) is the source
	// of truth — the agent reads it from /home/agent/.openclaw/soul.md
	// at turn time.
	_ = opts.SoulBody

	b, _ := json.Marshal(c)
	return string(b)
}

// BuildNanoclawConfig returns the environment variable content for a nanoclaw
// container. When byokBaseURL and byokAPIKey are non-empty (BYOK mode), they
// override the default proxy credentials.
func BuildNanoclawConfig(proxyBaseURL, proxyToken, assistantName string, imBridgeURL, bridgeSecret string, byokBaseURL, byokAPIKey string, geminiProxyBaseURL string) string {
	baseURL := proxyBaseURL
	apiKey := proxyToken
	if byokBaseURL != "" {
		baseURL = byokBaseURL
		apiKey = byokAPIKey
	}
	var lines []string
	lines = append(lines, "ANTHROPIC_BASE_URL="+baseURL)
	lines = append(lines, "ANTHROPIC_API_KEY="+apiKey)
	if assistantName == "" {
		assistantName = "Andy"
	}
	lines = append(lines, "ASSISTANT_NAME="+assistantName)
	lines = append(lines, "NANOCLAW_NO_CONTAINER=true")
	if imBridgeURL != "" {
		lines = append(lines, "NANOCLAW_BRIDGE_URL="+imBridgeURL)
		// Backwards compat (remove after all pods updated)
		lines = append(lines, "NANOCLAW_WEIXIN_BRIDGE_URL="+imBridgeURL)
	}
	if bridgeSecret != "" {
		lines = append(lines, "NANOCLAW_BRIDGE_SECRET="+bridgeSecret)
	}
	// Inject Gemini proxy credentials only when not in BYOK mode.
	// BYOK bypasses the proxy, so the BYOK key is not a valid proxy token.
	if geminiProxyBaseURL != "" && byokBaseURL == "" {
		lines = append(lines, "GOOGLE_GEMINI_BASE_URL="+geminiProxyBaseURL)
		lines = append(lines, "GEMINI_API_KEY="+proxyToken)
	}
	return strings.Join(lines, "\n") + "\n"
}
