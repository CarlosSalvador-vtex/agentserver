package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/agentserver/agentserver/internal/db"
)

// CompositionRef is a parsed reference to a soul or skill template.
type CompositionRef struct {
	Kind string // "git" or "draft"
	Name string // for git refs: the directory name; for draft refs: empty
	Sha  string // for git refs: commit sha or branch; for draft refs: empty
	UUID string // for draft refs: the draft id; for git refs: empty
}

// ParseCompositionRef parses "git:<name>@<sha>" or "draft:<uuid>".
// Empty input returns (nil, nil) — callers treat absence as "no soul / no skill".
func ParseCompositionRef(s string) (*CompositionRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "git:") {
		rest := strings.TrimPrefix(s, "git:")
		at := strings.LastIndex(rest, "@")
		if at < 0 {
			return nil, fmt.Errorf("composition ref %q: git refs require @<sha>", s)
		}
		name := strings.TrimSpace(rest[:at])
		sha := strings.TrimSpace(rest[at+1:])
		if name == "" || sha == "" {
			return nil, fmt.Errorf("composition ref %q: empty name or sha", s)
		}
		return &CompositionRef{Kind: "git", Name: name, Sha: sha}, nil
	}
	if strings.HasPrefix(s, "draft:") {
		uuid := strings.TrimPrefix(s, "draft:")
		if uuid == "" {
			return nil, fmt.Errorf("composition ref %q: draft requires <uuid>", s)
		}
		return &CompositionRef{Kind: "draft", UUID: uuid}, nil
	}
	return nil, fmt.Errorf("composition ref %q: must start with git: or draft:", s)
}

// ResolvedComposition is what the manager hands to buildPodSpec after
// turning DB refs into concrete K8s objects.
//
// EphemeralConfigMaps must be applied to the workspace namespace
// before pod create (the pod references them by name). ExtraVolumes +
// ExtraMounts append directly to the pod spec.
//
// Git refs are intentionally a no-op in this resolver — they require
// the chart-rendered ConfigMap to already be present in the workspace
// namespace, which the existing replicateSkillConfigMaps path handles
// via SANDBOX_SKILL_CONFIGMAPS env. Composition-driven mounts target
// draft refs (the playground iteration loop).
type ResolvedComposition struct {
	EphemeralConfigMaps []corev1.ConfigMap
	ExtraVolumes        []corev1.Volume
	ExtraMounts         []corev1.VolumeMount
	// SoulBody is the body of the soul markdown (frontmatter stripped),
	// ready to append to the agent's system prompt. Only populated for
	// draft soul refs; git refs leave this empty (the body is fetched at
	// runtime from the mounted soul.md).
	SoulBody string
	// SoulConstraints carries the structured frontmatter the runtime cares
	// about (max_turns, refuse_patterns, handoff_to_human_if).
	SoulConstraints map[string]interface{}
	// EnabledSkillNames is the set of skill plugins composition wants
	// enabled in OpenClaw (added to plugins.entries.<name> by the inject
	// emitter). Doesn't include skills declared via the env-based
	// SkillConfigMaps map — those keep their existing enable path.
	EnabledSkillNames []string
}

// ResolveComposition turns a stored sandbox_compositions row into the
// K8s objects + payloads the pod spec builder consumes.
func (m *Manager) ResolveComposition(ctx context.Context, sandboxID, namespace, platform string) (*ResolvedComposition, error) {
	comp, err := m.db.GetSandboxComposition(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load composition: %w", err)
	}
	if comp == nil {
		// Sandbox booted without composition (legacy / direct create).
		// Empty resolved keeps existing behaviour intact.
		return &ResolvedComposition{}, nil
	}

	resolved := &ResolvedComposition{}

	// --- Soul ---
	if comp.SoulRef.Valid {
		soulRef, err := ParseCompositionRef(comp.SoulRef.String)
		if err != nil {
			return nil, fmt.Errorf("soul_ref %q: %w", comp.SoulRef.String, err)
		}
		if soulRef != nil && soulRef.Kind == "draft" {
			soul, err := m.db.GetSoulDraft(soulRef.UUID)
			if err != nil {
				return nil, fmt.Errorf("load soul draft %s: %w", soulRef.UUID, err)
			}
			if soul == nil {
				return nil, fmt.Errorf("soul draft %s: not found", soulRef.UUID)
			}
			resolved.SoulBody = soul.Body
			resolved.SoulConstraints = extractSoulConstraints(soul.Frontmatter)

			cm, vol, mount, err := buildSoulConfigMapAndMount(sandboxID, namespace, soul, platform)
			if err != nil {
				return nil, fmt.Errorf("build soul mount: %w", err)
			}
			resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, cm)
			resolved.ExtraVolumes = append(resolved.ExtraVolumes, vol)
			resolved.ExtraMounts = append(resolved.ExtraMounts, mount)
		}
		// git: soul body is read in-pod from the chart-mounted ConfigMap.
		// We don't mount it here — the chart already does via a future
		// souls-configmap.yaml template (out of this PR).
	}

	// --- Skills ---
	for _, refStr := range comp.SkillRefs {
		ref, err := ParseCompositionRef(refStr)
		if err != nil {
			return nil, fmt.Errorf("skill_ref %q: %w", refStr, err)
		}
		if ref == nil || ref.Kind != "draft" {
			// Git skill: the chart's skills-configmap.yaml already mounts
			// it via env-driven SkillConfigMaps. Composition records the
			// intent; the existing path materializes it.
			if ref != nil && ref.Kind == "git" {
				resolved.EnabledSkillNames = append(resolved.EnabledSkillNames, ref.Name)
			}
			continue
		}
		skill, err := m.db.GetSkillDraft(ref.UUID)
		if err != nil {
			return nil, fmt.Errorf("load skill draft %s: %w", ref.UUID, err)
		}
		if skill == nil {
			return nil, fmt.Errorf("skill draft %s: not found", ref.UUID)
		}
		cm, vols, mounts, err := buildSkillConfigMapAndMounts(sandboxID, namespace, skill, platform)
		if err != nil {
			return nil, fmt.Errorf("build skill mount %s: %w", skill.Name, err)
		}
		resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, cm)
		resolved.ExtraVolumes = append(resolved.ExtraVolumes, vols...)
		resolved.ExtraMounts = append(resolved.ExtraMounts, mounts...)
		resolved.EnabledSkillNames = append(resolved.EnabledSkillNames, skill.Name)
	}

	return resolved, nil
}

// extractSoulConstraints pulls the subset of frontmatter the runtime
// enforces. Unknown fields are ignored (forward-compat with v2).
func extractSoulConstraints(fm map[string]interface{}) map[string]interface{} {
	if fm == nil {
		return nil
	}
	c, ok := fm["constraints"].(map[string]interface{})
	if !ok {
		return nil
	}
	out := map[string]interface{}{}
	for _, key := range []string{"max_turns", "refuse_patterns", "handoff_to_human_if"} {
		if v, ok := c[key]; ok {
			out[key] = v
		}
	}
	return out
}

// soulMountPath returns the in-pod path where soul.md lands for each
// platform. Both agents read this file at boot to layer the persona on
// top of their default system prompt.
func soulMountPath(platform string) string {
	switch platform {
	case "openclaw":
		return "/home/agent/.openclaw/soul.md"
	case "hermes":
		return "/opt/agent/soul.md"
	default:
		return ""
	}
}

func buildSoulConfigMapAndMount(sandboxID, namespace string, soul *db.SoulDraft, platform string) (corev1.ConfigMap, corev1.Volume, corev1.VolumeMount, error) {
	path := soulMountPath(platform)
	if path == "" {
		return corev1.ConfigMap{}, corev1.Volume{}, corev1.VolumeMount{},
			fmt.Errorf("soul mount not supported for platform %q", platform)
	}
	fmJSON, err := json.MarshalIndent(soul.Frontmatter, "", "  ")
	if err != nil {
		return corev1.ConfigMap{}, corev1.Volume{}, corev1.VolumeMount{},
			fmt.Errorf("marshal frontmatter: %w", err)
	}
	body := "---\n" + string(fmJSON) + "\n---\n\n" + soul.Body
	cmName := fmt.Sprintf("agentserver-draft-%s-soul-%s", safePrefix(sandboxID), safePrefix(soul.ID))
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: namespace,
			Labels: map[string]string{
				"agentserver.io/ephemeral":  "true",
				"agentserver.io/sandbox-id": sandboxID,
				"agentserver.io/draft-kind": "soul",
				"agentserver.io/draft-id":   soul.ID,
			},
		},
		Data: map[string]string{"soul.md": body},
	}
	vol := corev1.Volume{
		Name: "soul-md",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items: []corev1.KeyToPath{
					{Key: "soul.md", Path: "soul.md"},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "soul-md",
		MountPath: path,
		SubPath:   "soul.md",
		ReadOnly:  true,
	}
	return cm, vol, mount, nil
}

func buildSkillConfigMapAndMounts(sandboxID, namespace string, skill *db.SkillDraft, platform string) (corev1.ConfigMap, []corev1.Volume, []corev1.VolumeMount, error) {
	var mountRoot string
	switch platform {
	case "openclaw":
		mountRoot = "/home/agent/.openclaw/extensions"
	case "hermes":
		mountRoot = "/opt/data/skills/personal"
	default:
		return corev1.ConfigMap{}, nil, nil, fmt.Errorf("skill mounts not supported for platform %q", platform)
	}

	cmName := fmt.Sprintf("agentserver-draft-%s-skill-%s", safePrefix(sandboxID), safePrefix(skill.ID))
	data := make(map[string]string, len(skill.Files))
	keys := make([]string, 0, len(skill.Files))
	for path, content := range skill.Files {
		key := strings.ReplaceAll(path, "/", "__")
		data[key] = content
		keys = append(keys, key)
	}
	sort.Strings(keys)

	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: namespace,
			Labels: map[string]string{
				"agentserver.io/ephemeral":  "true",
				"agentserver.io/sandbox-id": sandboxID,
				"agentserver.io/draft-kind": "skill",
				"agentserver.io/draft-id":   skill.ID,
				"agentserver.io/skill-name": skill.Name,
			},
		},
		Data: data,
	}

	volName := fmt.Sprintf("skill-draft-%s", safePrefix(skill.ID))
	items := make([]corev1.KeyToPath, 0, len(keys))
	for _, key := range keys {
		path := strings.ReplaceAll(key, "__", "/")
		items = append(items, corev1.KeyToPath{Key: key, Path: path})
	}
	vol := corev1.Volume{
		Name: volName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items:                items,
			},
		},
	}
	mounts := make([]corev1.VolumeMount, 0, len(items))
	for _, item := range items {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: fmt.Sprintf("%s/%s/%s", mountRoot, skill.Name, item.Path),
			SubPath:   item.Path,
			ReadOnly:  true,
		})
	}
	return cm, []corev1.Volume{vol}, mounts, nil
}

// safePrefix returns the first 8 chars of s, or all of s when shorter,
// for use as a stable prefix in K8s object names that must stay under
// 63 chars after concatenation.
func safePrefix(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// BuildHermesConfigOverride returns a per-sandbox ConfigMap + volume +
// mount that override the cluster-wide hermes-config with one carrying
// the soul body injected as agent.system_prompt. Returns zero values
// when soulBody is empty (caller falls back to the global config).
//
// The base config is the raw config.yaml string from the cluster-wide
// hermes-config ConfigMap; we append (or replace) the agent.system_prompt
// key without parsing YAML — a simple text-level append is enough
// because the chart-rendered base never sets agent.system_prompt.
//
// Mount path stays /opt/data/config.yaml (same as the global config);
// manager.go uses this override volume when present and skips the
// global volume to avoid two mounts at the same path.
func BuildHermesConfigOverride(sandboxID, namespace, baseConfigYAML, soulBody string) (corev1.ConfigMap, corev1.Volume, corev1.VolumeMount, bool) {
	if soulBody == "" {
		return corev1.ConfigMap{}, corev1.Volume{}, corev1.VolumeMount{}, false
	}
	// Hermes registers personas via the top-level `personalities` dict
	// (config.py line 1561) and selects the active one via
	// `display.personality` (config.py line 1088, the field whose value
	// `hermes config show` prints under "◆ Display → Personality").
	// We register a "playground-soul" persona pointing at the soul body
	// and override display.personality to it. The base config never
	// sets display.personality, so a flat append works without needing
	// to merge nested keys.
	indented := indentSoulForYAML(soulBody)
	overlay := baseConfigYAML +
		"\ndisplay:\n  personality: playground-soul\n" +
		"personalities:\n" +
		"  playground-soul:\n" +
		"    description: Soul body injected by playground composition\n" +
		"    system_prompt: |\n" + indented + "\n"

	cmName := "agentserver-draft-" + safePrefix(sandboxID) + "-hermes-config"
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: namespace,
			Labels: map[string]string{
				"agentserver.io/ephemeral":  "true",
				"agentserver.io/sandbox-id": sandboxID,
				"agentserver.io/draft-kind": "hermes-config",
			},
		},
		Data: map[string]string{"config.yaml": overlay},
	}
	vol := corev1.Volume{
		Name: "hermes-config-override",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items: []corev1.KeyToPath{
					{Key: "config.yaml", Path: "config.yaml"},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "hermes-config-override",
		MountPath: "/opt/data/config.yaml",
		SubPath:   "config.yaml",
		ReadOnly:  true,
	}
	return cm, vol, mount, true
}

// indentSoulForYAML indents each line of the soul body by 6 spaces so
// it nests under `personalities.<name>.system_prompt: |` correctly.
// Empty lines stay empty (no trailing whitespace) which keeps strict
// YAML parsers happy.
func indentSoulForYAML(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = "      " + line
	}
	return strings.Join(lines, "\n")
}
