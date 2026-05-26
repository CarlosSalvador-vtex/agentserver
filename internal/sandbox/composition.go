package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
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
// turning DB refs into concrete file payloads.
type ResolvedComposition struct {
	// SoulBody is the prose body extracted from soul.md (frontmatter stripped),
	// ready to append to the agent's system prompt. Empty when no soul.
	SoulBody string
	// SoulConstraints carries the structured frontmatter the runtime cares
	// about (max_turns, refuse_patterns, handoff_to_human_if). Empty map
	// when no soul or schema-v0 soul without constraints.
	SoulConstraints map[string]interface{}
	// SkillNames identifies which skill ConfigMaps to mount. Names match
	// the directory names under deploy/helm/agentserver/skills/<name>/
	// for git refs; for draft refs, we materialize an ephemeral ConfigMap
	// per draft and reuse its name here.
	SkillNames []string
	// EphemeralConfigMaps is the list of K8s ConfigMaps the resolver
	// materialized from drafts. The caller is expected to apply them
	// before pod create + set ownerReferences so they cascade-delete.
	EphemeralConfigMaps []corev1.ConfigMap
	// SkillConfig is the per-skill configSchema input the
	// __OPENCLAW_INJECT_CFG / hermes config templating should pick up.
	SkillConfig map[string]map[string]interface{}
}

// ResolveComposition turns a stored sandbox_compositions row into
// concrete file/config inputs the pod spec builder can consume.
//
// Git refs are looked up against the chart-mounted skill ConfigMaps in
// the agentserver namespace (same path the existing skillVolumesAndMounts
// uses). Draft refs are read from skill_drafts / soul_drafts and
// materialized into per-sandbox ephemeral ConfigMaps named
// "agentserver-draft-<sandboxID>-<kind>-<draftID>".
func (m *Manager) ResolveComposition(ctx context.Context, sandboxID, namespace string) (*ResolvedComposition, error) {
	comp, err := m.db.GetSandboxComposition(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load composition: %w", err)
	}
	if comp == nil {
		// Sandbox booted without composition (legacy / direct create). Empty
		// resolved means the caller skips soul mount + uses chart-default
		// skill ConfigMaps (legacy SANDBOX_SKILL_CONFIGMAPS behavior).
		return &ResolvedComposition{}, nil
	}

	resolved := &ResolvedComposition{
		SkillConfig: comp.SkillConfig,
	}

	// --- Soul ---
	if comp.SoulRef.Valid {
		soulRef, err := ParseCompositionRef(comp.SoulRef.String)
		if err != nil {
			return nil, fmt.Errorf("soul_ref %q: %w", comp.SoulRef.String, err)
		}
		switch soulRef.Kind {
		case "git":
			// Git soul lives in chart ConfigMap "agentserver-soul-<name>",
			// rendered by templates/souls-configmap.yaml. We don't read it
			// from K8s API here — the mount in skillVolumesAndMounts handles
			// it. The constraint frontmatter the runtime cares about is read
			// in-pod via /opt/agent/soul.md by the agent boot script. For the
			// in-Go path we leave SoulBody empty and SoulConstraints nil;
			// boot-time prompt assembly is the agent's responsibility for
			// git-sourced souls.
			resolved.SoulBody = ""
		case "draft":
			soul, err := m.db.GetSoulDraft(soulRef.UUID)
			if err != nil {
				return nil, fmt.Errorf("load soul draft %s: %w", soulRef.UUID, err)
			}
			if soul == nil {
				return nil, fmt.Errorf("soul draft %s: not found", soulRef.UUID)
			}
			resolved.SoulBody = soul.Body
			resolved.SoulConstraints = extractSoulConstraints(soul.Frontmatter)

			// Materialize ephemeral ConfigMap so the mount path stays uniform
			// with git-sourced souls.
			cm, err := buildEphemeralSoulConfigMap(sandboxID, namespace, soul)
			if err != nil {
				return nil, fmt.Errorf("build ephemeral soul configmap: %w", err)
			}
			resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, *cm)
		}
	}

	// --- Skills ---
	for _, refStr := range comp.SkillRefs {
		ref, err := ParseCompositionRef(refStr)
		if err != nil {
			return nil, fmt.Errorf("skill_ref %q: %w", refStr, err)
		}
		switch ref.Kind {
		case "git":
			resolved.SkillNames = append(resolved.SkillNames, ref.Name)
		case "draft":
			skill, err := m.db.GetSkillDraft(ref.UUID)
			if err != nil {
				return nil, fmt.Errorf("load skill draft %s: %w", ref.UUID, err)
			}
			if skill == nil {
				return nil, fmt.Errorf("skill draft %s: not found", ref.UUID)
			}
			cm, err := buildEphemeralSkillConfigMap(sandboxID, namespace, skill)
			if err != nil {
				return nil, fmt.Errorf("build ephemeral skill configmap: %w", err)
			}
			resolved.EphemeralConfigMaps = append(resolved.EphemeralConfigMaps, *cm)
			resolved.SkillNames = append(resolved.SkillNames, skill.Name)
		}
	}

	return resolved, nil
}

// extractSoulConstraints pulls the subset of frontmatter the runtime
// enforces (max_turns / refuse_patterns / handoff_to_human_if). Unknown
// fields are ignored — frontmatter v1 readers tolerate v2-only fields.
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

// buildEphemeralSoulConfigMap renders a draft soul into a per-sandbox
// ConfigMap. The mount path (`/opt/agent/soul.md` for Hermes,
// `/home/agent/.openclaw/soul.md` for OpenClaw) is the same as the
// git-sourced one — only the ConfigMap source name differs.
func buildEphemeralSoulConfigMap(sandboxID, namespace string, soul *db.SoulDraft) (*corev1.ConfigMap, error) {
	fmJSON, err := json.MarshalIndent(soul.Frontmatter, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	body := "---\n" + string(fmJSON) + "\n---\n\n" + soul.Body
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("agentserver-draft-%s-soul-%s", sandboxID[:8], soul.ID[:8]),
			Namespace: namespace,
			Labels: map[string]string{
				"agentserver.io/ephemeral":  "true",
				"agentserver.io/sandbox-id": sandboxID,
				"agentserver.io/draft-kind": "soul",
				"agentserver.io/draft-id":   soul.ID,
			},
		},
		Data: map[string]string{
			"soul.md": body,
		},
	}, nil
}

// buildEphemeralSkillConfigMap turns a draft skill's flat files map
// into a ConfigMap with "/" → "__" key encoding (matching the chart's
// skills-configmap.yaml convention) so the existing mount builder in
// skillVolumesAndMounts can consume it uniformly.
func buildEphemeralSkillConfigMap(sandboxID, namespace string, skill *db.SkillDraft) (*corev1.ConfigMap, error) {
	data := make(map[string]string, len(skill.Files))
	for path, content := range skill.Files {
		key := strings.ReplaceAll(path, "/", "__")
		data[key] = content
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("agentserver-draft-%s-skill-%s", sandboxID[:8], skill.ID[:8]),
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
	}, nil
}
