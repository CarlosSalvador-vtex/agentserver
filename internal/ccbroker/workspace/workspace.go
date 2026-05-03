package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is the ephemeral local filesystem view a single CC turn operates in.
type Workspace struct {
	WorkspaceID string
	SessionID   string

	TempDir    string // root: /tmp/cc-broker/sess_<sessionID>
	ClaudeDir  string // <TempDir>/claude-config — CLAUDE_CONFIG_DIR
	ProjectDir string // <TempDir>/project       — CLI cwd (kept empty; only used for proj_hash)
	MemoryDir  string // <ClaudeDir>/projects/ws_<wid>/memory — auto-memory override
}

// TempDirBase is the parent under which per-session work directories are
// created. Tests override it via t.TempDir(); production uses os.TempDir().
var TempDirBase = ""

func tempDirBase() string {
	if TempDirBase != "" {
		return TempDirBase
	}
	return os.TempDir()
}

// claudeHomeKey is the deterministic S3 object key for a workspace's
// claude-home tarball — workspace-shared files only (skills, settings,
// memory, .claude.json). Per-session subtrees are stored separately under
// sessionJsonlKey.
func claudeHomeKey(workspaceID string) string {
	return fmt.Sprintf("workspaces/%s/claude-home.tar.gz", workspaceID)
}

// sessionJsonlKey is the deterministic S3 object key for a single session's
// conversation jsonl. One key per (workspace, session) pair so concurrent
// sessions in the same workspace cannot overwrite each other's transcript
// via the shared claude-home tarball.
func sessionJsonlKey(workspaceID, sessionID string) string {
	return fmt.Sprintf("workspaces/%s/sessions/%s.jsonl", workspaceID, sessionID)
}

// projHashDir returns the directory name Claude CLI derives from cwd when
// it stores a session's jsonl under <CLAUDE_CONFIG_DIR>/projects/<projHash>/.
// Empirically, the CLI replaces every '/' and '_' in cwd with '-'.
//
// Verified against an actual on-disk layout:
//
//	cwd:  /tmp/cc-broker/sess_cse_<uuid>/project
//	dir:  -tmp-cc-broker-sess-cse-<uuid>-project
func projHashDir(cwd string) string {
	return strings.NewReplacer("/", "-", "_", "-").Replace(cwd)
}

// sessionJsonlLocalPath is the on-disk location of the session jsonl that
// Claude CLI's --resume flag will look for, given this workspace's CWD and
// session ID.
func sessionJsonlLocalPath(ws *Workspace) string {
	uuid := strings.TrimPrefix(ws.SessionID, "cse_")
	return filepath.Join(ws.ClaudeDir, "projects", projHashDir(ws.ProjectDir), uuid+".jsonl")
}

// sessionSubtreeRel is the rel-path (relative to ClaudeDir) of the
// per-session subtree that should be omitted from the claude-home tarball
// because it lives under sessionJsonlKey instead.
func sessionSubtreeRel(ws *Workspace) string {
	return "projects/" + projHashDir(ws.ProjectDir)
}

// Setup creates the temp directory tree, downloads the workspace's
// claude-home tarball, and downloads the per-session jsonl. The returned
// Workspace must be passed to Teardown so the temp directory is removed and
// state is uploaded back.
//
// On any error after the directory tree is created, Setup removes TempDir
// before returning, so callers do not leak per-session directories.
func Setup(ctx context.Context, workspaceID, sessionID string, store *S3Store) (*Workspace, error) {
	// Path is deterministic in (sessionID) so Claude CLI's proj_hash lookup
	// (derived from Cwd = ProjectDir) finds the same session jsonl across
	// turns. Per-session turn serialization is enforced by the in-memory
	// TurnLock in handler_turns. cc-broker runs replicas: 1 in production;
	// multi-replica deployments would need a distributed lock.
	tempDir := filepath.Join(tempDirBase(), "cc-broker", "sess_"+sessionID)
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}

	ws := &Workspace{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		TempDir:     tempDir,
		ClaudeDir:   filepath.Join(tempDir, "claude-config"),
		ProjectDir:  filepath.Join(tempDir, "project"),
	}
	ws.MemoryDir = filepath.Join(ws.ClaudeDir, "projects", "ws_"+workspaceID, "memory")

	for _, d := range []string{ws.ClaudeDir, ws.ProjectDir, ws.MemoryDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	if err := store.DownloadTarGz(ctx, claudeHomeKey(workspaceID), ws.ClaudeDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("download claude-home for workspace %s: %w", workspaceID, err)
	}

	// Per-session jsonl is downloaded AFTER claude-home so it overrides any
	// stale copy that was still present in the tarball (a leftover from
	// pre-split layout). 404 means a brand-new session — the CLI will create
	// the file when --session-id runs.
	if err := store.DownloadFile(ctx, sessionJsonlKey(workspaceID, sessionID), sessionJsonlLocalPath(ws)); err != nil && !errors.Is(err, ErrObjectNotFound) {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("download session jsonl for %s/%s: %w", workspaceID, sessionID, err)
	}

	return ws, nil
}

// Teardown uploads the per-session jsonl, then packages ClaudeDir as a tar.gz
// (excluding this session's subtree) and uploads it. Finally, the temp dir
// is removed. Upload failures are logged but do not propagate — a flaky
// upload must not block the caller's turn response. TempDir is always
// removed.
func Teardown(ctx context.Context, ws *Workspace, store *S3Store) error {
	if ws == nil {
		return nil
	}
	defer func() { _ = os.RemoveAll(ws.TempDir) }()

	jsonlPath := sessionJsonlLocalPath(ws)
	if _, err := os.Stat(jsonlPath); err == nil {
		if err := store.UploadFile(ctx, jsonlPath, sessionJsonlKey(ws.WorkspaceID, ws.SessionID)); err != nil {
			fmt.Fprintf(os.Stderr, "workspace.Teardown: upload session jsonl: %v\n", err)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "workspace.Teardown: stat session jsonl: %v\n", err)
	}

	// Exclude THIS session's subtree from the claude-home tarball — it's
	// stored separately. We deliberately do NOT exclude other sessions'
	// subtrees that may still be present (carried in from pre-split data);
	// they will get migrated out the next time their owning session runs.
	skipSubtree := sessionSubtreeRel(ws)
	excludeRel := func(rel string) bool {
		return rel == skipSubtree || strings.HasPrefix(rel, skipSubtree+"/")
	}
	if err := store.UploadTarGz(ctx, ws.ClaudeDir, claudeHomeKey(ws.WorkspaceID), excludeRel); err != nil {
		fmt.Fprintf(os.Stderr, "workspace.Teardown: upload claude-home: %v\n", err)
	}
	return nil
}
