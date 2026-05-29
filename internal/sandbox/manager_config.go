package sandbox

import (
	"context"
	"fmt"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/agentserver/agentserver/internal/process"
)

// Sprint 3 PR-4 (improvements.md #10). Helpers extracted out of
// StartContainerWithIP so each per-platform branch lives in a dedicated
// function that can be reasoned about + future-tested in isolation.
//
// The extractions are pure: each takes the same inputs the inline switch
// used and returns the same triple (image, port, cmd | args, env-additions).
// Mount/volume accretion stays in the caller — it interleaves with
// composition + credential mounts whose order matters.

// containerConfigOutput is what each per-platform helper returns to the
// caller of StartContainerWithIP. Either Command (openclaw shell-script
// entrypoint) or Args (hermes positional args) is set, never both.
type containerConfigOutput struct {
	Image   string
	Port    int
	Command []string
	Args    []string
	// EnvAppend is appended after the caller's base env.
	EnvAppend []corev1.EnvVar
}

// applyOpenclawConfig builds the per-pod openclaw config blob, the
// inject-and-exec shell command, and the env vars (HOME, gateway token,
// soul-file hints) the container needs to boot with composition wiring.
func (m *Manager) applyOpenclawConfig(opts process.StartOptions, composition *ResolvedComposition, proxyBaseURL string) (*containerConfigOutput, error) {
	if m.cfg.OpenclawImage == "" {
		return nil, fmt.Errorf("OPENCLAW_IMAGE not configured: set the environment variable to the openclaw container image")
	}
	port := m.cfg.OpenclawPort
	if port == 0 {
		port = 18789
	}

	cfgBaseURL, cfgAPIKey := proxyBaseURL, opts.ProxyToken
	var cfgModels []process.LLMModel
	if opts.BYOKBaseURL != "" {
		cfgBaseURL = opts.BYOKBaseURL
		cfgAPIKey = opts.BYOKAPIKey
		cfgModels = opts.BYOKModels
	}

	// Plugin entries: chart-mounted skills (env-driven) + composition-driven
	// drafts. Both routes deliver ConfigMaps + Volumes; the difference is
	// where the names come from.
	var skillPlugins []string
	for name := range m.cfg.SkillConfigMaps {
		skillPlugins = append(skillPlugins, name)
	}
	var openclawWA []string
	if m.cfg.OpenclawWhatsappAllowed != "" {
		for _, n := range strings.Split(m.cfg.OpenclawWhatsappAllowed, ",") {
			if t := strings.TrimSpace(n); t != "" {
				openclawWA = append(openclawWA, t)
			}
		}
	}
	mergedSkills := append([]string{}, skillPlugins...)
	mergedSkills = append(mergedSkills, composition.EnabledSkillNames...)

	openclawCfg := BuildOpenclawConfig(cfgBaseURL, cfgAPIKey, opts.OpenclawToken, cfgModels, OpenclawConfigOptions{
		EnabledPlugins:  mergedSkills,
		WhatsappAllowed: openclawWA,
		WeixinEnabled:   m.cfg.OpenclawWeixinEnabled,
		SoulBody:        composition.SoulBody,
	})

	out := &containerConfigOutput{
		Image: m.cfg.OpenclawImage,
		Port:  port,
		Command: []string{"sh", "-c", `mkdir -p ~/.openclaw && node -e "
const fs = require('fs');
const path = require('os').homedir() + '/.openclaw/openclaw.json';
let existing = {};
try { existing = JSON.parse(fs.readFileSync(path, 'utf8')); } catch {}
const inject = JSON.parse(process.env.__OPENCLAW_INJECT_CFG);
// Shallow-merge: inject keys override existing. We ALWAYS send plugins.entries
// (possibly empty) so this replaces the image's default plugin set — sandboxes
// boot clean (no bundled openclaw-weixin) unless a plugin is explicitly enabled.
Object.assign(existing, inject);
if (inject.gateway) {
  existing.gateway = existing.gateway || {};
  Object.assign(existing.gateway, inject.gateway);
  if (inject.gateway.controlUi) {
    existing.gateway.controlUi = existing.gateway.controlUi || {};
    Object.assign(existing.gateway.controlUi, inject.gateway.controlUi);
  }
}
if (inject.models) existing.models = inject.models;
// Soul: playground composition emits agent.systemPrompt. Deep-merge so
// the image's default agent.* fields (max_turns, retry settings) stay
// intact while the persona overrides systemPrompt.
if (inject.agent) {
  existing.agent = existing.agent || {};
  Object.assign(existing.agent, inject.agent);
}
fs.writeFileSync(path, JSON.stringify(existing, null, 2));
" && exec node openclaw.mjs gateway --allow-unconfigured --bind lan`},
	}

	out.EnvAppend = []corev1.EnvVar{
		{Name: "__OPENCLAW_INJECT_CFG", Value: openclawCfg},
		{Name: "HOME", Value: "/home/agent"},
	}
	if opts.OpenclawToken != "" {
		out.EnvAppend = append(out.EnvAppend, corev1.EnvVar{Name: "OPENCLAW_GATEWAY_TOKEN", Value: opts.OpenclawToken})
	}
	if composition.SoulBody != "" {
		out.EnvAppend = append(out.EnvAppend,
			corev1.EnvVar{Name: "OPENCLAW_SOUL_FILE", Value: "/home/agent/.openclaw/soul.md"},
			corev1.EnvVar{Name: "AGENTSERVER_SOUL_BODY", Value: composition.SoulBody},
		)
	}
	return out, nil
}

// applyCompositionMounts replicates env-driven skill ConfigMaps + applies the
// playground composition (ephemeral CMs + extra volumes/mounts). Returns the
// updated (volumes, mounts) slices. No-op for non-claw/hermes sandbox types.
func (m *Manager) applyCompositionMounts(ctx context.Context, ns string, opts process.StartOptions, composition *ResolvedComposition, volumes []corev1.Volume, mounts []corev1.VolumeMount) ([]corev1.Volume, []corev1.VolumeMount, error) {
	if opts.SandboxType != SandboxTypeHermes.String() && opts.SandboxType != SandboxTypeOpenclaw.String() {
		return volumes, mounts, nil
	}
	if err := m.replicateSkillConfigMaps(ctx, ns); err != nil {
		return nil, nil, fmt.Errorf("replicate skill configmaps: %w", err)
	}
	skillVols, skillMounts, err := m.skillVolumesAndMounts(ctx, opts.SandboxType)
	if err != nil {
		return nil, nil, fmt.Errorf("build skill mounts: %w", err)
	}
	volumes = append(volumes, skillVols...)
	mounts = append(mounts, skillMounts...)

	// Playground composition (draft refs). Resolution was already done at
	// the top of StartContainerWithIP because the openclaw config emitter
	// needs SoulBody too. Here we apply the ConfigMaps + append vols/mounts.
	for i := range composition.EphemeralConfigMaps {
		cm := composition.EphemeralConfigMaps[i]
		if _, err := m.clientset.CoreV1().ConfigMaps(ns).Create(ctx, &cm, metav1.CreateOptions{}); err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				log.Printf("apply ephemeral configmap %s/%s: %v", ns, cm.Name, err)
			}
		}
	}
	volumes = append(volumes, composition.ExtraVolumes...)
	mounts = append(mounts, composition.ExtraMounts...)
	return volumes, mounts, nil
}

// applyHermesConfig populates the per-pod hermes-agent env vars +
// "gateway run" args. WhatsApp allowlist + GLM key are taken from the
// agentserver pod's env (chart-level config).
func (m *Manager) applyHermesConfig(opts process.StartOptions) (*containerConfigOutput, error) {
	if m.cfg.HermesImage == "" {
		return nil, fmt.Errorf("HERMES_IMAGE not configured: set the environment variable to the hermes-agent container image (ghcr.io/nousresearch/hermes-agent)")
	}
	out := &containerConfigOutput{
		Image: m.cfg.HermesImage,
		Port:  m.cfg.HermesPort,
		Args:  []string{"gateway", "run"},
		EnvAppend: []corev1.EnvVar{
			{Name: "AWS_REGION", Value: "us-east-1"},
			{Name: "AWS_DEFAULT_REGION", Value: "us-east-1"},
			{Name: "HERMES_DASHBOARD", Value: "1"},
			{Name: "HERMES_DASHBOARD_TUI", Value: "1"},
			{Name: "GATEWAY_ALLOW_ALL_USERS", Value: "true"},
		},
	}
	if m.cfg.HermesWhatsappAllowed != "" {
		out.EnvAppend = append(out.EnvAppend,
			corev1.EnvVar{Name: "WHATSAPP_ALLOWED_USERS", Value: m.cfg.HermesWhatsappAllowed},
		)
	}
	if m.cfg.HermesGLMAPIKey != "" {
		out.EnvAppend = append(out.EnvAppend,
			corev1.EnvVar{Name: "GLM_API_KEY", Value: m.cfg.HermesGLMAPIKey},
		)
	}
	_ = opts // reserved for future per-sandbox hermes options
	return out, nil
}
