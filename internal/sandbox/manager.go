package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"hash/fnv"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	"encoding/json"

	credprovider "github.com/agentserver/agentserver/internal/credentialproxy/provider"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/process"
)

const (
	labelManagedBy       = "managed-by"
	labelValue           = "agentserver"
	sandboxNameHashLabel = "agents.x-k8s.io/sandbox-name-hash"
	sandboxContainerName = "agent"
	pollInterval         = 2 * time.Second
	pollTimeout          = 5 * time.Minute
)

// Compile-time interface check.
var _ process.Manager = (*Manager)(nil)

type sessionEntry struct {
	proc        *execProcess
	sandboxName string
	namespace   string
}

// Manager manages Sandbox CRs and remotecommand exec sessions.
type Manager struct {
	cfg       Config
	db        *db.DB
	restCfg   *rest.Config
	k8s       client.Client
	clientset kubernetes.Interface
	mu        sync.RWMutex
	sessions  map[string]*sessionEntry
}

// NewManager creates a sandbox Manager using in-cluster or KUBECONFIG config.
func NewManager(cfg Config, database *db.DB) (*Manager, error) {
	restCfg, err := buildRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(s))

	k8sClient, err := client.New(restCfg, client.Options{Scheme: s})
	if err != nil {
		return nil, fmt.Errorf("controller-runtime client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes clientset: %w", err)
	}

	m := &Manager{
		cfg:       cfg,
		db:        database,
		restCfg:   restCfg,
		k8s:       k8sClient,
		clientset: clientset,
		sessions:  make(map[string]*sessionEntry),
	}

	return m, nil
}

func buildRESTConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// CleanOrphans deletes Sandbox CRs labelled managed-by=agentserver that are NOT in the known set.
// It iterates all provided workspace namespaces.
func (m *Manager) CleanOrphans(knownSandboxNames []string, namespaces []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	known := make(map[string]bool, len(knownSandboxNames))
	for _, name := range knownSandboxNames {
		known[name] = true
	}

	for _, ns := range namespaces {
		var list sandboxv1alpha1.SandboxList
		if err := m.k8s.List(ctx, &list,
			client.InNamespace(ns),
			client.MatchingLabels{labelManagedBy: labelValue},
		); err != nil {
			log.Printf("failed to list orphan sandboxes in %s: %v", ns, err)
			continue
		}
		for i := range list.Items {
			name := list.Items[i].Name
			if known[name] {
				continue
			}
			log.Printf("cleaning orphan sandbox %s in namespace %s", name, ns)
			if err := m.k8s.Delete(ctx, &list.Items[i]); err != nil {
				log.Printf("failed to delete orphan sandbox %s: %v", name, err)
			}
		}
	}
}

func (m *Manager) Start(id, command string, args, env []string, opts process.StartOptions) (process.Process, error) {
	ctx := context.Background()
	sandboxName := "agent-sandbox-" + shortID(id)
	ns := opts.Namespace

	// Build environment variables for the sandbox pod.
	containerEnv := []corev1.EnvVar{{Name: "TERM", Value: "xterm-256color"}}

	// Inject LLM provider credentials via OPENCODE_CONFIG_CONTENT (provider.anthropic.options).
	if opts.BYOKBaseURL != "" {
		opcodeConfig := BuildOpencodeConfig(m.cfg.OpencodeConfigContent, opts.BYOKAPIKey, opts.BYOKBaseURL)
		containerEnv = append(containerEnv, corev1.EnvVar{Name: "OPENCODE_CONFIG_CONTENT", Value: opcodeConfig})
	} else if opts.ProxyToken != "" {
		opcodeConfig := BuildOpencodeConfig(m.cfg.OpencodeConfigContent, opts.ProxyToken, "")
		containerEnv = append(containerEnv, corev1.EnvVar{Name: "OPENCODE_CONFIG_CONTENT", Value: opcodeConfig})
	} else if m.cfg.OpencodeConfigContent != "" {
		containerEnv = append(containerEnv, corev1.EnvVar{Name: "OPENCODE_CONFIG_CONTENT", Value: m.cfg.OpencodeConfigContent})
	}

	// Volume mounts for the main container.
	volumeMounts := []corev1.VolumeMount{
		{Name: "session-data", MountPath: "/home/agent"},
	}
	var volumes []corev1.Volume

	// Mount workspace drive PVCs if provided.
	for i, vol := range opts.WorkspaceVolumes {
		volName := fmt.Sprintf("ws-vol-%d", i)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: volName, MountPath: vol.MountPath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: vol.PVCName,
				},
			},
		})
	}

	// Init container: mount PVC at a temp path, seed it from the original home dir on first use, then fix ownership.
	// This avoids the empty PVC overwriting the agent image's /home/agent (which has claude CLI, dotfiles, etc.).
	initScript := `
set -e
# If the PVC is fresh (only has lost+found or is empty), seed it from the image's home dir.
if [ ! -f /mnt/session-data/.initialized ]; then
  echo "Seeding session PVC from /home/agent..."
  cp -a /home/agent/. /mnt/session-data/ 2>/dev/null || true
  touch /mnt/session-data/.initialized
fi
chown -R 1000:1000 /mnt/session-data
# Ensure projects directory exists (workspace PVC mount point)
mkdir -p /mnt/session-data/projects
`
	// Add chown for each workspace volume.
	for i := range opts.WorkspaceVolumes {
		initScript += fmt.Sprintf("mkdir -p /mnt/ws-vol-%d\nchown -R 1000:1000 /mnt/ws-vol-%d\n", i, i)
	}

	initContainers := []corev1.Container{{
		Name:    "fix-perms",
		Image:   m.cfg.Image,
		Command: []string{"sh", "-c", initScript},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "session-data", MountPath: "/mnt/session-data"},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser: int64Ptr(0),
		},
	}}
	// Also mount workspace drives in init container.
	for i := range opts.WorkspaceVolumes {
		volName := fmt.Sprintf("ws-vol-%d", i)
		initContainers[0].VolumeMounts = append(initContainers[0].VolumeMounts,
			corev1.VolumeMount{Name: volName, MountPath: fmt.Sprintf("/mnt/ws-vol-%d", i)},
		)
	}

	// Build VolumeClaimTemplates for session data.
	storageSize := resource.MustParse(m.cfg.SessionStorageSize)
	vctMeta := sandboxv1alpha1.EmbeddedObjectMetadata{Name: "session-data"}
	vcts := []sandboxv1alpha1.PersistentVolumeClaimTemplate{{
		EmbeddedObjectMetadata: vctMeta,
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
			},
		},
	}}

	// Set storage class if configured.
	if m.cfg.StorageClassName != "" {
		vcts[0].Spec.StorageClassName = &m.cfg.StorageClassName
	}

	// Create the Sandbox CR.
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
			Labels:    map[string]string{labelManagedBy: labelValue},
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			VolumeClaimTemplates: vcts,
			PodTemplate: sandboxv1alpha1.PodTemplate{
				ObjectMeta: sandboxv1alpha1.PodMetadata{
					Labels: map[string]string{labelManagedBy: labelValue},
				},
				Spec: corev1.PodSpec{
					InitContainers: initContainers,
					Containers: []corev1.Container{{
						Name:            sandboxContainerName,
						Image:           m.cfg.Image,
						Command:         []string{"sleep", "infinity"},
						Env:             containerEnv,
						VolumeMounts:    volumeMounts,
						ImagePullPolicy: corev1.PullAlways,
						WorkingDir:      "/home/agent/projects",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: memoryQuantity(opts.Memory),
								corev1.ResourceCPU:    cpuQuantity(opts.CPU),
							},
						},
					}},
					Volumes:          volumes,
					RuntimeClassName: m.runtimeClassName(),
					RestartPolicy:    corev1.RestartPolicyNever,
					Tolerations:      m.cfg.Tolerations,
				},
			},
		},
	}

	if err := m.k8s.Create(ctx, sb); err != nil {
		return nil, fmt.Errorf("create sandbox CR: %w", err)
	}

	// Wait for sandbox to become ready.
	podName, _, err := m.waitForReady(ctx, ns, sandboxName)
	if err != nil {
		_ = m.k8s.Delete(ctx, sb)
		return nil, fmt.Errorf("sandbox not ready: %w", err)
	}

	// Build the full command.
	fullCmd := append([]string{command}, args...)

	// Start remotecommand exec into the pod.
	proc, err := startExec(m.restCfg, m.clientset, ns, podName, sandboxContainerName, fullCmd)
	if err != nil {
		_ = m.k8s.Delete(ctx, sb)
		return nil, fmt.Errorf("exec into sandbox: %w", err)
	}

	m.mu.Lock()
	m.sessions[id] = &sessionEntry{proc: proc, sandboxName: sandboxName, namespace: ns}
	m.mu.Unlock()

	return proc, nil
}

// StartContainer for K8s sandbox creates the Sandbox CR and waits for it to be ready.
// Returns the pod IP for agent server communication.
func (m *Manager) StartContainer(id string, opts process.StartOptions) error {
	_, err := m.Start(id, "sleep", []string{"infinity"}, nil, opts)
	return err
}

// StartContainerWithIP creates/starts the sandbox and returns the pod IP.
func (m *Manager) StartContainerWithIP(id string, opts process.StartOptions) (string, error) {
	ctx := context.Background()
	sandboxName := "agent-sandbox-" + shortID(id)
	ns := opts.Namespace

	if !SandboxType(opts.SandboxType).Valid() {
		return "", fmt.Errorf("unsupported sandbox type %q: must be openclaw or hermes", opts.SandboxType)
	}

	// Composition resolution — read once at the top so the openclaw
	// config emitter and the skill mount block both consume the same
	// snapshot. Best-effort: failure logs + returns empty so the
	// sandbox can still boot.
	composition, compErr := m.ResolveComposition(ctx, id, ns, opts.SandboxType)
	if compErr != nil {
		log.Printf("composition resolve for %s: %v (continuing without)", id, compErr)
		composition = &ResolvedComposition{}
	}

	// Build environment variables for the sandbox pod.
	containerEnv := []corev1.EnvVar{{Name: "TERM", Value: "xterm-256color"}}

	// Inject LLM provider credentials.
	proxyBaseURL := ExtractProxyBaseURL(m.cfg.OpencodeConfigContent)
	if opts.BYOKBaseURL != "" {
		containerEnv = append(containerEnv,
			corev1.EnvVar{Name: "ANTHROPIC_API_KEY", Value: opts.BYOKAPIKey},
			corev1.EnvVar{Name: "ANTHROPIC_BASE_URL", Value: opts.BYOKBaseURL},
		)
	} else if opts.ProxyToken != "" && proxyBaseURL != "" {
		// NanoClaw's agent-runner inherits process.env (not .env file values).
		// ANTHROPIC_BASE_URL must be a real env var so Claude Code can find the proxy.
		// Strip /v1 because the Anthropic SDK appends it automatically.
		containerEnv = append(containerEnv,
			corev1.EnvVar{Name: "ANTHROPIC_API_KEY", Value: opts.ProxyToken},
			corev1.EnvVar{Name: "ANTHROPIC_BASE_URL", Value: strings.TrimSuffix(proxyBaseURL, "/v1")},
		)
	}
	// Inject Gemini proxy credentials as real env vars (same reason as Anthropic above).
	// Skip when BYOK is active — BYOK bypasses the proxy entirely.
	if m.cfg.GeminiProxyBaseURL != "" && opts.ProxyToken != "" && opts.BYOKBaseURL == "" {
		containerEnv = append(containerEnv,
			corev1.EnvVar{Name: "GEMINI_API_KEY", Value: opts.ProxyToken},
			corev1.EnvVar{Name: "GOOGLE_GEMINI_BASE_URL", Value: m.cfg.GeminiProxyBaseURL},
		)
	}

	// Select image, port, and command based on sandbox type.
	var sandboxImage string
	var containerPort int
	var containerCmd []string
	var containerArgs []string

	switch opts.SandboxType {
	case SandboxTypeOpenclaw.String():
		if m.cfg.OpenclawImage == "" {
			return "", fmt.Errorf("OPENCLAW_IMAGE not configured: set the environment variable to the openclaw container image")
		}
		sandboxImage = m.cfg.OpenclawImage
		containerPort = m.cfg.OpenclawPort
		if containerPort == 0 {
			containerPort = 18789
		}
		// Build openclaw config JSON with gateway settings and LLM provider.
		cfgBaseURL, cfgAPIKey := proxyBaseURL, opts.ProxyToken
		var cfgModels []process.LLMModel
		if opts.BYOKBaseURL != "" {
			cfgBaseURL = opts.BYOKBaseURL
			cfgAPIKey = opts.BYOKAPIKey
			cfgModels = opts.BYOKModels
		}
		// Surface enabled skill plugins (one entry per skill ConfigMap) so the
		// openclaw plugin host loads them on startup. WhatsApp allowlist is
		// taken from the env (HERMES_WHATSAPP_ALLOWED reused for openclaw
		// when set on the agentserver pod).
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
		// Merge env-driven skill list with composition-driven skill list
		// so plugins.entries covers both sources. EnabledSkillNames is
		// populated for both git (chart-mounted) and draft (ephemeral CM)
		// refs in ResolveComposition.
		mergedSkills := append([]string{}, skillPlugins...)
		mergedSkills = append(mergedSkills, composition.EnabledSkillNames...)
		openclawCfg := BuildOpenclawConfig(cfgBaseURL, cfgAPIKey, opts.OpenclawToken, cfgModels, OpenclawConfigOptions{
			EnabledPlugins:  mergedSkills,
			WhatsappAllowed: openclawWA,
			SoulBody:        composition.SoulBody,
		})
		// Merge our gateway/models config into the image's existing openclaw.json
		// (which contains plugin install metadata) instead of overwriting it.
		containerCmd = []string{"sh", "-c", `mkdir -p ~/.openclaw && node -e "
const fs = require('fs');
const path = require('os').homedir() + '/.openclaw/openclaw.json';
let existing = {};
try { existing = JSON.parse(fs.readFileSync(path, 'utf8')); } catch {}
const inject = JSON.parse(process.env.__OPENCLAW_INJECT_CFG);
// Deep-merge: inject keys override existing, but preserve plugins/channels.
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
" && exec node openclaw.mjs gateway --allow-unconfigured --bind lan`}
		containerEnv = append(containerEnv, corev1.EnvVar{Name: "__OPENCLAW_INJECT_CFG", Value: openclawCfg})
		// Ensure ~ resolves to the PVC mount so credentials and conversation
		// data persist across pause/resume.
		containerEnv = append(containerEnv, corev1.EnvVar{Name: "HOME", Value: "/home/agent"})
		if opts.OpenclawToken != "" {
			containerEnv = append(containerEnv, corev1.EnvVar{Name: "OPENCLAW_GATEWAY_TOKEN", Value: opts.OpenclawToken})
		}
		if composition.SoulBody != "" {
			containerEnv = append(containerEnv,
				corev1.EnvVar{Name: "OPENCLAW_SOUL_FILE", Value: "/home/agent/.openclaw/soul.md"},
				corev1.EnvVar{Name: "AGENTSERVER_SOUL_BODY", Value: composition.SoulBody},
			)
		}
	case SandboxTypeHermes.String():
		if m.cfg.HermesImage == "" {
			return "", fmt.Errorf("HERMES_IMAGE not configured: set the environment variable to the hermes-agent container image (ghcr.io/nousresearch/hermes-agent)")
		}
		sandboxImage = m.cfg.HermesImage
		containerPort = m.cfg.HermesPort

		containerArgs = []string{"gateway", "run"}

		containerEnv = append(containerEnv,
			corev1.EnvVar{Name: "AWS_REGION", Value: "us-east-1"},
			corev1.EnvVar{Name: "AWS_DEFAULT_REGION", Value: "us-east-1"},
			corev1.EnvVar{Name: "HERMES_DASHBOARD", Value: "1"},
			corev1.EnvVar{Name: "HERMES_DASHBOARD_TUI", Value: "1"},
			corev1.EnvVar{Name: "GATEWAY_ALLOW_ALL_USERS", Value: "true"},
		)
		if m.cfg.HermesWhatsappAllowed != "" {
			containerEnv = append(containerEnv,
				corev1.EnvVar{Name: "WHATSAPP_ALLOWED_USERS", Value: m.cfg.HermesWhatsappAllowed},
			)
		}
		if m.cfg.HermesGLMAPIKey != "" {
			containerEnv = append(containerEnv,
				corev1.EnvVar{Name: "GLM_API_KEY", Value: m.cfg.HermesGLMAPIKey},
			)
		}
	}

	// Volume mounts for the main container. Hermes-agent expects its data
	// directory at /opt/data (mirrors `-v ~/.hermes:/opt/data` from upstream
	// docs); all other sandbox types use the user's home directory.
	sessionMountPath := "/home/agent"
	if opts.SandboxType == SandboxTypeHermes.String() {
		sessionMountPath = "/opt/data"
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: "session-data", MountPath: sessionMountPath},
	}
	var volumes []corev1.Volume

	// Mount workspace drive PVCs if provided.
	for i, vol := range opts.WorkspaceVolumes {
		volName := fmt.Sprintf("ws-vol-%d", i)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: volName, MountPath: vol.MountPath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: vol.PVCName,
				},
			},
		})
	}

	// Hermes: mount the cluster-wide hermes-config ConfigMap into /opt/data,
	// which is where the hermes-agent docker image expects its config dir
	// (mirrors `-v ~/.hermes:/opt/data` from upstream docs). SubPath ensures
	// only config.yaml is overlaid; runtime files (state.db, sessions/) stay
	// writable on the session PVC.
	//
	// Soul body for hermes lands at /opt/data/SOUL.md (uppercase, the
	// HERMES_HOME convention — hermes-agent auto-loads it as persona on
	// every turn). See soulMountPath in composition.go.
	if opts.SandboxType == SandboxTypeHermes.String() && m.cfg.HermesConfigMapName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "hermes-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.cfg.HermesConfigMapName,
					},
					Items: []corev1.KeyToPath{
						{Key: "config.yaml", Path: "config.yaml"},
					},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "hermes-config",
			MountPath: "/opt/data/config.yaml",
			SubPath:   "config.yaml",
			ReadOnly:  true,
		})
	}

	// Skill ConfigMaps (cobrança, etc.) — mounted under
	// /opt/data/skills/personal/<name>/ for hermes or
	// /home/agent/.openclaw/extensions/<name>/ for openclaw. Two paths
	// feed this:
	//   1. Env-driven SkillConfigMaps (chart-level): the historical
	//      global skill list. Mounted via skillVolumesAndMounts.
	//   2. Composition-driven (playground): per-sandbox refs in
	//      sandbox_compositions. ResolveComposition materializes
	//      ephemeral ConfigMaps + builds the vol/mount payload directly.
	if opts.SandboxType == SandboxTypeHermes.String() || opts.SandboxType == SandboxTypeOpenclaw.String() {
		if err := m.replicateSkillConfigMaps(ctx, ns); err != nil {
			return "", fmt.Errorf("replicate skill configmaps: %w", err)
		}
		skillVols, skillMounts, err := m.skillVolumesAndMounts(ctx, opts.SandboxType)
		if err != nil {
			return "", fmt.Errorf("build skill mounts: %w", err)
		}
		volumes = append(volumes, skillVols...)
		volumeMounts = append(volumeMounts, skillMounts...)

		// Composition (playground) — apply draft soul + skill mounts on
		// top of the env-driven ones. Resolution was already done at
		// the top of StartContainerWithIP (the openclaw config emitter
		// needs SoulBody too). Here we just apply the resulting
		// ConfigMaps + append volumes/mounts.
		for i := range composition.EphemeralConfigMaps {
			cm := composition.EphemeralConfigMaps[i]
			if _, err := m.clientset.CoreV1().ConfigMaps(ns).Create(ctx, &cm, metav1.CreateOptions{}); err != nil {
				if !k8serrors.IsAlreadyExists(err) {
					log.Printf("apply ephemeral configmap %s/%s: %v", ns, cm.Name, err)
				}
			}
		}
		volumes = append(volumes, composition.ExtraVolumes...)
		volumeMounts = append(volumeMounts, composition.ExtraMounts...)
	}

	// Determine the home directory to seed from: openclaw uses /home/node,
	// all other images use /home/agent.
	seedHome := "/home/agent"
	if opts.SandboxType == SandboxTypeOpenclaw.String() {
		seedHome = "/home/node"
	}
	initScript := fmt.Sprintf(`
set -e
if [ ! -f /mnt/session-data/.initialized ]; then
  echo "Seeding session PVC from %s..."
  cp -a %s/. /mnt/session-data/ 2>/dev/null || true
  touch /mnt/session-data/.initialized
fi
# Ensure projects directory exists (workspace PVC mount point)
mkdir -p /mnt/session-data/projects
# chown after mkdir so all directories are owned by UID 1000
chown -R 1000:1000 /mnt/session-data
`, seedHome, seedHome)
	// Add chown for each workspace volume.
	for i := range opts.WorkspaceVolumes {
		initScript += fmt.Sprintf("mkdir -p /mnt/ws-vol-%d\nchown -R 1000:1000 /mnt/ws-vol-%d\n", i, i)
	}

	initContainers := []corev1.Container{{
		Name:    "fix-perms",
		Image:   sandboxImage,
		Command: []string{"sh", "-c", initScript},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "session-data", MountPath: "/mnt/session-data"},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser: int64Ptr(0),
		},
	}}
	for i := range opts.WorkspaceVolumes {
		volName := fmt.Sprintf("ws-vol-%d", i)
		initContainers[0].VolumeMounts = append(initContainers[0].VolumeMounts,
			corev1.VolumeMount{Name: volName, MountPath: fmt.Sprintf("/mnt/ws-vol-%d", i)},
		)
	}

	storageSize := resource.MustParse(m.cfg.SessionStorageSize)
	vctMeta := sandboxv1alpha1.EmbeddedObjectMetadata{Name: "session-data"}
	vcts := []sandboxv1alpha1.PersistentVolumeClaimTemplate{{
		EmbeddedObjectMetadata: vctMeta,
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storageSize},
			},
		},
	}}
	if m.cfg.StorageClassName != "" {
		vcts[0].Spec.StorageClassName = &m.cfg.StorageClassName
	}

	workingDir := "/home/agent/projects"
	if opts.SandboxType == SandboxTypeOpenclaw.String() {
		workingDir = "/app"
	}

	// Inject credential proxy config files (kubeconfig, etc.) if bindings exist.
	credFiles, credEnv, credErr := m.buildCredentialConfig(ctx, opts.WorkspaceID, opts.ProxyToken)
	if credErr != nil {
		log.Printf("warning: credential config: %v", credErr)
	}
	var credSecretName string
	if len(credFiles) > 0 {
		credSecretName = sandboxName + "-creds"
		if err := m.createCredentialSecret(ctx, ns, credSecretName, sandboxName, credFiles); err != nil {
			log.Printf("warning: create credential secret: %v", err)
			credSecretName = ""
		} else {
			defaultMode := int32(0o600)
			volumes = append(volumes, corev1.Volume{
				Name: "cred-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  credSecretName,
						DefaultMode: &defaultMode,
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "cred-config",
				MountPath: "/var/run/agentserver",
				ReadOnly:  true,
			})
			for k, v := range credEnv {
				containerEnv = append(containerEnv, corev1.EnvVar{Name: k, Value: v})
			}
		}
	}

	mainContainer := corev1.Container{
		Name:            sandboxContainerName,
		Image:           sandboxImage,
		Env:             containerEnv,
		VolumeMounts:    volumeMounts,
		ImagePullPolicy: corev1.PullAlways,
		WorkingDir:      workingDir,
		Ports: []corev1.ContainerPort{{
			ContainerPort: int32(containerPort),
			Protocol:      corev1.ProtocolTCP,
		}},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(int32(containerPort)),
				},
			},
			InitialDelaySeconds: 2,
			PeriodSeconds:       2,
			FailureThreshold:    30,
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: memoryQuantity(opts.Memory),
				corev1.ResourceCPU:    cpuQuantity(opts.CPU),
			},
		},
	}
	if len(containerCmd) > 0 {
		mainContainer.Command = containerCmd
	}
	if len(containerArgs) > 0 {
		mainContainer.Args = containerArgs
	}

	var serviceAccountName string
	if opts.SandboxType == SandboxTypeHermes.String() && m.cfg.HermesServiceAccountRoleArn != "" {
		if err := m.ensureHermesServiceAccount(ctx, ns); err != nil {
			return "", fmt.Errorf("ensure hermes ServiceAccount: %w", err)
		}
		serviceAccountName = "hermes"
	}

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
			Labels:    map[string]string{labelManagedBy: labelValue},
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			VolumeClaimTemplates: vcts,
			PodTemplate: sandboxv1alpha1.PodTemplate{
				ObjectMeta: sandboxv1alpha1.PodMetadata{
					Labels: map[string]string{labelManagedBy: labelValue},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					InitContainers:     initContainers,
					Containers:         []corev1.Container{mainContainer},
					Volumes:            volumes,
					RuntimeClassName:   m.runtimeClassNameFor(opts.SandboxType),
					RestartPolicy:      corev1.RestartPolicyNever,
					Tolerations:        m.cfg.Tolerations,
				},
			},
		},
	}

	if err := m.k8s.Create(ctx, sb); err != nil {
		return "", fmt.Errorf("create sandbox CR: %w", err)
	}

	_, podIP, err := m.waitForReady(ctx, ns, sandboxName)
	if err != nil {
		_ = m.k8s.Delete(ctx, sb)
		return "", fmt.Errorf("sandbox not ready: %w", err)
	}

	return podIP, nil
}

// ResumeContainer scales a paused sandbox back to 1 replica and waits for it
// to be ready, without starting an exec session (the sidecar handles exec).
// Returns the pod IP.
func (m *Manager) ResumeContainer(id string) error {
	_, err := m.ResumeContainerWithIP(id)
	return err
}

// ResumeContainerWithIP scales a paused sandbox back to 1 replica and returns the pod IP.
func (m *Manager) ResumeContainerWithIP(id string) (string, error) {
	sandboxName := "agent-sandbox-" + shortID(id)
	ctx := context.Background()

	ns, err := m.lookupNamespace(id)
	if err != nil {
		return "", fmt.Errorf("resolve namespace for resume: %w", err)
	}

	// Patch sandbox replicas to 1.
	patch := []byte(`{"spec":{"replicas":1}}`)
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
		},
	}
	if err := m.k8s.Patch(ctx, sb, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return "", fmt.Errorf("patch sandbox replicas to 1: %w", err)
	}

	// Wait for pod to be ready.
	_, podIP, err := m.waitForReady(ctx, ns, sandboxName)
	if err != nil {
		return "", fmt.Errorf("sandbox not ready after resume: %w", err)
	}
	return podIP, nil
}

// Pause scales the sandbox to 0 replicas. Pod goes away, PVC stays.
func (m *Manager) Pause(id string) error {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if ok {
		// Close exec stream if one exists.
		entry.proc.close()
	}

	// Patch sandbox replicas to 0.
	sandboxName := "agent-sandbox-" + shortID(id)
	var ns string
	if ok {
		sandboxName = entry.sandboxName
		ns = entry.namespace
	}
	if ns == "" {
		var err error
		ns, err = m.lookupNamespace(id)
		if err != nil {
			return fmt.Errorf("resolve namespace for pause: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	patch := []byte(`{"spec":{"replicas":0}}`)
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
		},
	}
	if err := m.k8s.Patch(ctx, sb, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("patch sandbox replicas to 0: %w", err)
	}
	return nil
}

// Resume scales the sandbox back to 1, waits for ready, and starts a new exec.
func (m *Manager) Resume(id, sandboxName, command string, args []string) (process.Process, error) {
	ctx := context.Background()

	ns, err := m.lookupNamespace(id)
	if err != nil {
		return nil, fmt.Errorf("resolve namespace for resume: %w", err)
	}

	// Patch sandbox replicas to 1.
	patch := []byte(`{"spec":{"replicas":1}}`)
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
		},
	}
	if err := m.k8s.Patch(ctx, sb, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return nil, fmt.Errorf("patch sandbox replicas to 1: %w", err)
	}

	// Wait for pod to be ready.
	podName, _, err := m.waitForReady(ctx, ns, sandboxName)
	if err != nil {
		return nil, fmt.Errorf("sandbox not ready after resume: %w", err)
	}

	// Start remotecommand exec.
	fullCmd := append([]string{command}, args...)
	proc, err := startExec(m.restCfg, m.clientset, ns, podName, sandboxContainerName, fullCmd)
	if err != nil {
		return nil, fmt.Errorf("exec into resumed sandbox: %w", err)
	}

	m.mu.Lock()
	m.sessions[id] = &sessionEntry{proc: proc, sandboxName: sandboxName, namespace: ns}
	m.mu.Unlock()

	return proc, nil
}

// waitForReady polls until the Sandbox has Ready=True and returns the backing pod name and IP.
func (m *Manager) waitForReady(ctx context.Context, namespace, sandboxName string) (podName string, podIP string, err error) {
	deadline := time.Now().Add(pollTimeout)
	nameHash := nameHash(sandboxName)

	for time.Now().Before(deadline) {
		var sb sandboxv1alpha1.Sandbox
		key := client.ObjectKey{Namespace: namespace, Name: sandboxName}
		if err := m.k8s.Get(ctx, key, &sb); err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if isSandboxReady(&sb) {
			podList, err := m.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: sandboxNameHashLabel + "=" + nameHash,
			})
			if err != nil {
				time.Sleep(pollInterval)
				continue
			}
			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					return pod.Name, pod.Status.PodIP, nil
				}
			}
		}
		time.Sleep(pollInterval)
	}
	return "", "", fmt.Errorf("timed out waiting for sandbox %s", sandboxName)
}

func isSandboxReady(sb *sandboxv1alpha1.Sandbox) bool {
	for _, c := range sb.Status.Conditions {
		if c.Type == string(sandboxv1alpha1.SandboxConditionReady) && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func nameHash(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }

// cpuQuantity converts millicores to a K8s resource.Quantity.
// Falls back to 2000m (2 cores) if zero.
func cpuQuantity(millis int) resource.Quantity {
	if millis == 0 {
		millis = 2000
	}
	return *resource.NewMilliQuantity(int64(millis), resource.DecimalSI)
}

// memoryQuantity converts bytes to a K8s resource.Quantity.
// Falls back to 2Gi if zero.
func memoryQuantity(bytes int64) resource.Quantity {
	if bytes == 0 {
		bytes = 2 * 1024 * 1024 * 1024
	}
	return *resource.NewQuantity(bytes, resource.BinarySI)
}

func (m *Manager) runtimeClassName() *string {
	if m.cfg.RuntimeClassName == "" {
		return nil
	}
	return strPtr(m.cfg.RuntimeClassName)
}

func (m *Manager) runtimeClassNameFor(sandboxType string) *string {
	switch sandboxType {
	case SandboxTypeOpenclaw.String():
		if m.cfg.OpenclawRuntimeClassName != "" {
			return strPtr(m.cfg.OpenclawRuntimeClassName)
		}
	case SandboxTypeHermes.String():
		if m.cfg.HermesRuntimeClassName != "" {
			return strPtr(m.cfg.HermesRuntimeClassName)
		}
	}
	return m.runtimeClassName()
}

// lookupNamespace resolves the K8s namespace for a sandbox by looking up
// sandbox → workspace → k8s_namespace in the database.
func (m *Manager) lookupNamespace(sandboxID string) (string, error) {
	if m.db == nil {
		return "", fmt.Errorf("no database reference for namespace lookup")
	}
	sbx, err := m.db.GetSandbox(sandboxID)
	if err != nil {
		return "", fmt.Errorf("get sandbox %s: %w", sandboxID, err)
	}
	if sbx == nil {
		return "", fmt.Errorf("sandbox %s not found", sandboxID)
	}
	ws, err := m.db.GetWorkspace(sbx.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("get workspace %s: %w", sbx.WorkspaceID, err)
	}
	if ws == nil {
		return "", fmt.Errorf("workspace %s not found", sbx.WorkspaceID)
	}
	if !ws.K8sNamespace.Valid || ws.K8sNamespace.String == "" {
		return "", fmt.Errorf("workspace %s has no k8s namespace", ws.ID)
	}
	return ws.K8sNamespace.String, nil
}

func (m *Manager) Get(id string) (process.Process, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	return entry.proc, true
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if ok {
		entry.proc.close()
	}

	sandboxName := "agent-sandbox-" + shortID(id)
	var ns string
	if ok {
		sandboxName = entry.sandboxName
		ns = entry.namespace
	}
	if ns == "" {
		var err error
		ns, err = m.lookupNamespace(id)
		if err != nil {
			log.Printf("failed to resolve namespace for stop %s: %v", id, err)
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: ns,
		},
	}
	if err := m.k8s.Delete(ctx, sb); err != nil {
		log.Printf("failed to delete sandbox %s: %v", sandboxName, err)
	}

	// Clean up credential Secret (if any).
	m.deleteCredentialSecret(ctx, ns, sandboxName)

	return nil
}

// StopBySandboxName deletes a Sandbox CR by its name in the given namespace.
func (m *Manager) StopBySandboxName(namespace, sandboxName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: namespace,
		},
	}
	return m.k8s.Delete(ctx, sb)
}

// ExecSimple runs a command in a sandbox pod and returns its stdout.
// It is a one-shot exec (no stdin/TTY) intended for short-lived commands
// like writing config files or restarting a gateway.
func (m *Manager) ExecSimple(ctx context.Context, sandboxID string, command []string) (string, error) {
	// Resolve pod namespace and name.
	ns, err := m.lookupNamespace(sandboxID)
	if err != nil {
		return "", err
	}
	sandboxName := "agent-sandbox-" + shortID(sandboxID)
	podName, _, err := m.waitForReady(ctx, ns, sandboxName)
	if err != nil {
		return "", fmt.Errorf("pod not ready: %w", err)
	}

	req := m.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: sandboxContainerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	wsExec, err := remotecommand.NewWebSocketExecutor(m.restCfg, "POST", req.URL().String())
	if err != nil {
		return "", err
	}
	spdyExec, err := remotecommand.NewSPDYExecutor(m.restCfg, "POST", req.URL())
	if err != nil {
		return "", err
	}
	executor, err := remotecommand.NewFallbackExecutor(wsExec, spdyExec, func(error) bool { return true })
	if err != nil {
		return "", err
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return "", fmt.Errorf("exec: %w (stderr: %s)", err, stderr.String())
	}
	return stdout.String(), nil
}

func (m *Manager) Close() error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		m.Stop(id)
	}
	return nil
}

// buildCredentialConfig iterates registered credential providers and builds
// config files + env vars to inject into a sandbox pod.
func (m *Manager) buildCredentialConfig(ctx context.Context, workspaceID, proxyToken string) (map[string][]byte, map[string]string, error) {
	if m.cfg.CredproxyPublicURL == "" || m.db == nil {
		return nil, nil, nil
	}

	files := make(map[string][]byte)
	envVars := make(map[string]string)

	for _, prov := range credprovider.All() {
		metas, err := m.db.ListCredentialBindingsMeta(workspaceID, prov.Kind())
		if err != nil {
			return nil, nil, fmt.Errorf("list bindings for %s: %w", prov.Kind(), err)
		}
		if len(metas) == 0 {
			continue
		}

		// Convert DB metas to provider BindingMetas.
		bindings := make([]*credprovider.BindingMeta, len(metas))
		for i, meta := range metas {
			var pm map[string]any
			if len(meta.PublicMeta) > 0 {
				json.Unmarshal(meta.PublicMeta, &pm)
			}
			bindings[i] = &credprovider.BindingMeta{
				ID:          meta.ID,
				WorkspaceID: meta.WorkspaceID,
				Kind:        meta.Kind,
				DisplayName: meta.DisplayName,
				ServerURL:   meta.ServerURL,
				PublicMeta:  pm,
				AuthType:    meta.AuthType,
				IsDefault:   meta.IsDefault,
			}
		}

		cfgFiles, err := prov.BuildSandboxConfig(bindings, proxyToken, m.cfg.CredproxyPublicURL)
		if err != nil {
			return nil, nil, fmt.Errorf("build config for %s: %w", prov.Kind(), err)
		}
		for _, f := range cfgFiles {
			files[f.SubPath] = f.Content
			for k, v := range f.EnvVars {
				envVars[k] = v
			}
		}
	}

	return files, envVars, nil
}

// createCredentialSecret creates a K8s Secret with the given data in the namespace.
// The sandbox-name label enables cleanup when the sandbox is deleted.
func (m *Manager) createCredentialSecret(ctx context.Context, namespace, name, sandboxName string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				labelManagedBy:   labelValue,
				"sandbox-name":   sandboxName,
			},
		},
		Data: data,
	}
	_, err := m.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// deleteCredentialSecret deletes the credential secret for a sandbox if it exists.
func (m *Manager) deleteCredentialSecret(ctx context.Context, namespace, sandboxName string) {
	secretName := sandboxName + "-creds"
	err := m.clientset.CoreV1().Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil {
		// Not found is fine — the secret may not have been created.
		log.Printf("delete credential secret %s/%s: %v", namespace, secretName, err)
	}
}

// ensureHermesServiceAccount creates the "hermes" ServiceAccount in the given
// namespace with the IRSA role-arn annotation if it does not yet exist.
// Idempotent — silently succeeds when the SA already exists. The role ARN
// comes from Config.HermesServiceAccountRoleArn (env HERMES_SA_ROLE_ARN).
func (m *Manager) ensureHermesServiceAccount(ctx context.Context, namespace string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hermes",
			Namespace: namespace,
			Labels:    map[string]string{labelManagedBy: labelValue},
			Annotations: map[string]string{
				"eks.amazonaws.com/role-arn": m.cfg.HermesServiceAccountRoleArn,
			},
		},
	}
	_, err := m.clientset.CoreV1().ServiceAccounts(namespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create hermes SA in %s: %w", namespace, err)
	}
	if m.cfg.HermesConfigMapName != "" {
		if err := m.replicateHermesConfigMap(ctx, namespace); err != nil {
			return fmt.Errorf("replicate hermes config map: %w", err)
		}
	}
	return nil
}

// replicateHermesConfigMap copies the cluster-wide hermes config map from the
// agentserver release namespace into the workspace namespace. Thin wrapper
// over the generic replicateConfigMap.
func (m *Manager) replicateHermesConfigMap(ctx context.Context, targetNS string) error {
	return m.replicateConfigMap(ctx, m.cfg.HermesConfigMapName, targetNS)
}

// replicateConfigMap copies a ConfigMap from the agentserver release
// namespace into the workspace namespace so the sandbox pod can mount it.
// ConfigMaps are namespace-scoped, so without this every workspace ns would
// need its own copy. Idempotent — on existing target it overwrites
// Data/BinaryData. Empty name is a no-op (logs + returns nil).
func (m *Manager) replicateConfigMap(ctx context.Context, name, targetNS string) error {
	if name == "" {
		return nil
	}
	srcNS := m.cfg.AgentserverNamespace
	if srcNS == "" {
		srcNS = "default"
	}
	src, err := m.clientset.CoreV1().ConfigMaps(srcNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get source configmap %s/%s: %w", srcNS, name, err)
	}
	dst := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: targetNS,
			Labels:    map[string]string{labelManagedBy: labelValue},
		},
		Data:       src.Data,
		BinaryData: src.BinaryData,
	}
	existing, err := m.clientset.CoreV1().ConfigMaps(targetNS).Get(ctx, dst.Name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		_, err = m.clientset.CoreV1().ConfigMaps(targetNS).Create(ctx, dst, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Data = dst.Data
	existing.BinaryData = dst.BinaryData
	_, err = m.clientset.CoreV1().ConfigMaps(targetNS).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// replicateSkillConfigMaps copies every skill ConfigMap declared in
// Config.SkillConfigMaps into the target namespace. Failures on individual
// skills are returned as the first non-nil error — the caller decides
// whether to abort sandbox creation or proceed.
func (m *Manager) replicateSkillConfigMaps(ctx context.Context, targetNS string) error {
	for skillName, cmName := range m.cfg.SkillConfigMaps {
		if err := m.replicateConfigMap(ctx, cmName, targetNS); err != nil {
			return fmt.Errorf("replicate skill %q configmap %q: %w", skillName, cmName, err)
		}
	}
	return nil
}

// skillVolumesAndMounts builds Volume + VolumeMount entries for every skill
// ConfigMap declared in Config.SkillConfigMaps, scoped to the given platform.
// Files inside a skill ConfigMap use "__" as the slash separator (K8s data
// key rule); this helper maps each key back to its nested path under the
// mount root via items[].path.
//
// platform must be "hermes" or "openclaw" — drives the on-disk parent dir.
func (m *Manager) skillVolumesAndMounts(ctx context.Context, platform string) ([]corev1.Volume, []corev1.VolumeMount, error) {
	if len(m.cfg.SkillConfigMaps) == 0 {
		return nil, nil, nil
	}
	var mountRoot string
	switch platform {
	case SandboxTypeHermes.String():
		mountRoot = "/opt/data/skills/personal"
	case SandboxTypeOpenclaw.String():
		// OpenClaw discovers plugins under ~/.openclaw/extensions/. The
		// upstream openclaw image runs as user `agent` (warning at boot:
		// "discovered non-bundled plugins may auto-load: openclaw-weixin
		// (/home/agent/.openclaw/extensions/openclaw-weixin/...)"), so
		// the mount has to land under that home dir. Previous attempt
		// at /home/node/.openclaw/extensions/ landed outside the loader
		// scan path, leaving plugins.entries.<name> dangling and the
		// loader logging "plugin not found: <name> (stale config entry)".
		mountRoot = "/home/agent/.openclaw/extensions"
	default:
		return nil, nil, fmt.Errorf("skill mounts not supported for sandbox type %q", platform)
	}

	srcNS := m.cfg.AgentserverNamespace
	if srcNS == "" {
		srcNS = "default"
	}

	var vols []corev1.Volume
	var mounts []corev1.VolumeMount
	// Deterministic iteration order so manifests don't churn under
	// `kubectl diff` from one create to the next.
	names := make([]string, 0, len(m.cfg.SkillConfigMaps))
	for n := range m.cfg.SkillConfigMaps {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, skillName := range names {
		cmName := m.cfg.SkillConfigMaps[skillName]
		// Read the ConfigMap keys so we know which files to mount. Read
		// from the release namespace (always present) rather than the
		// workspace ns (race with replicateConfigMap).
		src, err := m.clientset.CoreV1().ConfigMaps(srcNS).Get(ctx, cmName, metav1.GetOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("read skill configmap %s/%s: %w", srcNS, cmName, err)
		}
		if len(src.Data) == 0 {
			continue
		}
		volName := fmt.Sprintf("skill-%s", skillName)
		items := make([]corev1.KeyToPath, 0, len(src.Data))
		for key := range src.Data {
			// Restore "/" from the "__" encoding used in ConfigMap keys.
			path := strings.ReplaceAll(key, "__", "/")
			items = append(items, corev1.KeyToPath{Key: key, Path: path})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
		vols = append(vols, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
					Items:                items,
				},
			},
		})
		// Per-file subPath mount so the parent directory on the PVC stays
		// writable (state/ subdir + agreements.log). SubPath must match
		// item.Path (the nested layout K8s materializes inside the volume),
		// NOT item.Key (the flat ConfigMap data key with "__" encoding).
		// Using item.Key as SubPath made K8s create empty directories at
		// the mount target instead of mounting the file.
		for _, item := range items {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: fmt.Sprintf("%s/%s/%s", mountRoot, skillName, item.Path),
				SubPath:   item.Path,
				ReadOnly:  true,
			})
		}
	}
	return vols, mounts, nil
}

// ensureOpenclawDeps replicates skill ConfigMaps into the workspace namespace
// for an OpenClaw sandbox. Mirrors ensureHermesServiceAccount but without the
// ServiceAccount + IRSA branch (openclaw doesn't currently need IRSA).
func (m *Manager) ensureOpenclawDeps(ctx context.Context, namespace string) error {
	return m.replicateSkillConfigMaps(ctx, namespace)
}
