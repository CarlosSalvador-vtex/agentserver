package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace is the ephemeral local filesystem view a single CC turn operates in.
type Workspace struct {
	WorkspaceID string
	SessionID   string

	TempDir    string // root: /tmp/cc-broker/sess_<sessionID>
	ClaudeDir  string // <TempDir>/claude-config — CLAUDE_CONFIG_DIR
	ProjectDir string // <TempDir>/project       — CLI cwd
	MemoryDir  string // <ClaudeDir>/projects/ws_<wid>/memory — auto-memory override

	snapshot map[string]FileInfo // captured at Setup, consumed by Teardown
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

// Setup creates the temp directory tree and downloads workspace context from
// OpenViking. The returned Workspace must be passed to Teardown so the temp
// directory is removed and changed files are uploaded back.
//
// Download errors are non-fatal: a missing or partial workspace tree is expected
// on first-turn workspaces that start empty.
func Setup(ctx context.Context, workspaceID, sessionID string, vc *VikingClient) (*Workspace, error) {
	// Path is deterministic in (sessionID) so Claude CLI's proj_hash lookup
	// (which is derived from Cwd = ProjectDir) finds the same session jsonl
	// across turns. Per-session turn serialization is enforced by the
	// in-memory TurnLock in handler_turns. cc-broker runs replicas: 1 in
	// production; multi-replica deployments would need a distributed lock.
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

	// Download claude-home (global config, CLAUDE.md, memory files, etc.).
	// Fail-open: missing tree is normal for brand-new workspaces.
	homeURI := fmt.Sprintf("viking://resources/workspace_%s/claude-home/", workspaceID)
	if err := vc.DownloadTree(ctx, homeURI, ws.ClaudeDir); err != nil {
		fmt.Fprintf(os.Stderr, "workspace.Setup: download claude-home: %v\n", err)
	}

	// Download project tree (source files the agent will operate on).
	projectURI := fmt.Sprintf("viking://resources/workspace_%s/project/", workspaceID)
	if err := vc.DownloadTree(ctx, projectURI, ws.ProjectDir); err != nil {
		fmt.Fprintf(os.Stderr, "workspace.Setup: download project: %v\n", err)
	}

	// Snapshot ClaudeDir so Teardown can diff and upload only what changed.
	ws.snapshot = TakeSnapshot(ws.ClaudeDir)
	return ws, nil
}

// Teardown diffs ClaudeDir against the snapshot taken at Setup, uploads every
// added or modified file back to OpenViking, then removes the temp dir.
//
// - New (added) files use CreateFile (two-step temp_upload + add_resource).
// - Modified files use UploadFile (single content/write call).
// - Removed files are not propagated; OpenViking content writes are
//   append-or-replace only.
//
// Individual upload errors are logged to stderr but do not cause Teardown to
// return an error — a flaky upload must not block the caller's turn response.
func Teardown(ctx context.Context, ws *Workspace, vc *VikingClient) error {
	if ws == nil {
		return nil
	}
	defer func() { _ = os.RemoveAll(ws.TempDir) }()

	changes := DiffSnapshot(ws.ClaudeDir, ws.snapshot)
	for _, c := range changes {
		if c.Kind == "removed" {
			continue
		}

		content, err := os.ReadFile(c.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "workspace.Teardown: read %s: %v\n", c.Path, err)
			continue
		}

		uri := fmt.Sprintf("viking://resources/workspace_%s/claude-home/%s",
			ws.WorkspaceID, c.RelPath)

		switch c.Kind {
		case "added":
			if err := vc.CreateFile(ctx, uri, content); err != nil {
				fmt.Fprintf(os.Stderr, "workspace.Teardown: create %s: %v\n", uri, err)
			}
		case "modified":
			if err := vc.UploadFile(ctx, uri, string(content)); err != nil {
				fmt.Fprintf(os.Stderr, "workspace.Teardown: upload %s: %v\n", uri, err)
			}
		}
	}
	return nil
}
