package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
		return &CompositionRef{Kind: RefKindGit.String(), Name: name, Sha: sha}, nil
	}
	if strings.HasPrefix(s, "draft:") {
		uuid := strings.TrimPrefix(s, "draft:")
		if uuid == "" {
			return nil, fmt.Errorf("composition ref %q: draft requires <uuid>", s)
		}
		return &CompositionRef{Kind: RefKindDraft.String(), UUID: uuid}, nil
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
func (m *Manager) ResolveComposition(ctx context.Context, sandboxID, namespace, platform string) (resolved *ResolvedComposition, err error) {
	start := time.Now()
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
		}
		observeCompositionResolve(result, time.Since(start).Seconds())
	}()
	comp, err := m.db.GetSandboxComposition(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load composition: %w", err)
	}
	if comp == nil {
		// Sandbox booted without composition (legacy / direct create).
		// Empty resolved keeps existing behaviour intact.
		return &ResolvedComposition{}, nil
	}

	// Fetch workspace ID so we can check for published drafts that override
	// git system templates.
	sbx, err := m.db.GetSandbox(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load sandbox for composition: %w", err)
	}
	var workspaceID string
	if sbx != nil {
		workspaceID = sbx.WorkspaceID
	}

	resolved = &ResolvedComposition{}

	// --- Soul ---
	if comp.SoulRef.Valid {
		soulRef, err := ParseCompositionRef(comp.SoulRef.String)
		if err != nil {
			return nil, fmt.Errorf("soul_ref %q: %w", comp.SoulRef.String, err)
		}
		if soulRef != nil && soulRef.Kind == RefKindDraft.String() {
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
		} else if soulRef != nil && soulRef.Kind == RefKindGit.String() && workspaceID != "" {
			// Workspace published draft overrides the git system template.
			pub, err := m.db.GetPublishedSoulDraftByName(soulRef.Name, workspaceID)
			if err != nil {
				return nil, fmt.Errorf("lookup published soul %q: %w", soulRef.Name, err)
			}
			if pub != nil {
				resolved.SoulBody = pub.Body
				resolved.SoulConstraints = extractSoulConstraints(pub.Frontmatter)

				cm, vol, mount, err := buildSoulConfigMapAndMount(sandboxID, namespace, pub, platform)
				if err != nil {
					return nil, fmt.Errorf("build published soul mount: %w", err)
				}
				resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, cm)
				resolved.ExtraVolumes = append(resolved.ExtraVolumes, vol)
				resolved.ExtraMounts = append(resolved.ExtraMounts, mount)
			}
			// No published draft → fall through to git path (chart-mounted ConfigMap).
		}
	}

	// --- Skills ---
	for _, refStr := range comp.SkillRefs {
		ref, err := ParseCompositionRef(refStr)
		if err != nil {
			return nil, fmt.Errorf("skill_ref %q: %w", refStr, err)
		}
		if ref == nil {
			continue
		}
		if ref.Kind == RefKindGit.String() {
			// Workspace published draft overrides the git system template.
			if workspaceID != "" {
				pub, err := m.db.GetPublishedSkillDraftByName(ref.Name, workspaceID)
				if err != nil {
					return nil, fmt.Errorf("lookup published skill %q: %w", ref.Name, err)
				}
				if pub != nil {
					cm, vols, mounts, err := buildSkillConfigMapAndMounts(sandboxID, namespace, pub, platform)
					if err != nil {
						return nil, fmt.Errorf("build published skill mount %s: %w", pub.Name, err)
					}
					resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, cm)
					resolved.ExtraVolumes = append(resolved.ExtraVolumes, vols...)
					resolved.ExtraMounts = append(resolved.ExtraMounts, mounts...)
					resolved.EnabledSkillNames = append(resolved.EnabledSkillNames, pub.Name)
					continue
				}
			}
			// No published draft → chart's skills-configmap.yaml handles it.
			resolved.EnabledSkillNames = append(resolved.EnabledSkillNames, ref.Name)
			continue
		}
		if ref.Kind != RefKindDraft.String() {
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

// soulMountPath returns the in-pod path where the soul file lands for each
// platform. Both Hermes and OpenClaw auto-load SOUL.md from their respective
// workspace dirs on every turn — no hook or skill instruction required.
//
// OpenClaw workspace dir: ~/.openclaw/workspace/ (resolveDefaultAgentWorkspaceDir).
// The file is listed in VALID_BOOTSTRAP_NAMES and loaded by loadWorkspaceBootstrapFiles.
func soulMountPath(platform string) string {
	switch platform {
	case SandboxTypeOpenclaw.String():
		return "/home/agent/.openclaw/workspace/SOUL.md"
	case SandboxTypeHermes.String():
		return "/opt/data/SOUL.md"
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
	// Key matches target filename so SubPath works for both platforms.
	soulKey := filepath.Base(path)
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
		Data: map[string]string{soulKey: body},
	}
	vol := corev1.Volume{
		Name: "soul-md",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items: []corev1.KeyToPath{
					{Key: soulKey, Path: soulKey},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "soul-md",
		MountPath: path,
		SubPath:   soulKey,
		ReadOnly:  true,
	}
	return cm, vol, mount, nil
}

func buildSkillConfigMapAndMounts(sandboxID, namespace string, skill *db.SkillDraft, platform string) (corev1.ConfigMap, []corev1.Volume, []corev1.VolumeMount, error) {
	var mountRoot string
	switch platform {
	case SandboxTypeOpenclaw.String():
		mountRoot = "/home/agent/.openclaw/extensions"
	case SandboxTypeHermes.String():
		mountRoot = "/opt/data/skills/personal"
	default:
		return corev1.ConfigMap{}, nil, nil, fmt.Errorf("skill mounts not supported for platform %q", platform)
	}

	cmName := fmt.Sprintf("agentserver-draft-%s-skill-%s", safePrefix(sandboxID), safePrefix(skill.ID))
	files := skill.Files
	if platform == SandboxTypeOpenclaw.String() {
		files = prependOpenclawSoulHint(files)
	}
	data := make(map[string]string, len(files))
	keys := make([]string, 0, len(files))
	for path, content := range files {
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

// prependOpenclawSoulHint is a no-op: openclaw auto-loads
// ~/.openclaw/workspace/SOUL.md (DEFAULT_SOUL_FILENAME) as a bootstrap
// file on every turn — no manual read instruction in prompt.md needed.
func prependOpenclawSoulHint(files map[string]string) map[string]string {
	return files
}

func safePrefix(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// Hermes config.yaml override was the original strategy for soul
// injection — emit display.personality + personalities.<name>.
// E2E proved that path superfluous: the upstream hermes-agent image
// auto-loads $HERMES_HOME/SOUL.md (=/opt/data/SOUL.md) on every turn
// as the canonical persona file. Mounting our soul.md there directly
// (see soulMountPath) is enough; no config rewrite needed.
