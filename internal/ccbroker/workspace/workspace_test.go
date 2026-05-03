package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupAndTeardown_RoundTrip(t *testing.T) {
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	// Pre-load one file so the first Setup has something to download.
	fake.putObject(claudeHomeKey("ws1"), makeTarGz(t, map[string]string{
		"CLAUDE.md": "global-claude",
	}), "etag-v1")

	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	ws, err := Setup(ctx, "ws1", "cse_abc", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Downloaded file present in ClaudeDir.
	got, err := os.ReadFile(filepath.Join(ws.ClaudeDir, "CLAUDE.md"))
	if err != nil || string(got) != "global-claude" {
		t.Fatalf("CLAUDE.md mismatch: got=%q err=%v", got, err)
	}
	// Memory dir created at the deterministic path.
	wantMem := filepath.Join(ws.ClaudeDir, "projects", "ws_ws1", "memory")
	if _, err := os.Stat(wantMem); err != nil {
		t.Fatalf("memory dir missing: %v", err)
	}

	// Mutate one file + add a new one. Teardown must upload everything.
	if err := os.WriteFile(filepath.Join(ws.ClaudeDir, "CLAUDE.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.MemoryDir, "MEMORY.md"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Teardown(ctx, ws, store); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// TempDir gone.
	if _, err := os.Stat(ws.TempDir); !os.IsNotExist(err) {
		t.Fatalf("TempDir should be removed; err=%v", err)
	}

	// One object uploaded under this workspace's key.
	if _, ok := fake.uploads[claudeHomeKey("ws1")]; !ok {
		t.Fatalf("expected upload at %s, uploads=%v", claudeHomeKey("ws1"), keysOf(fake.uploads))
	}

	// Round-trip: stage the upload as the new object, fresh Setup gets the
	// mutated content back. Bump ETag to simulate the natural change.
	fake.putObject(claudeHomeKey("ws1"), fake.uploads[claudeHomeKey("ws1")], "etag-v2")
	ws2, err := Setup(ctx, "ws1", "cse_abc", store)
	if err != nil {
		t.Fatalf("Setup #2: %v", err)
	}
	defer Teardown(ctx, ws2, store)
	got2, _ := os.ReadFile(filepath.Join(ws2.ClaudeDir, "CLAUDE.md"))
	if string(got2) != "changed" {
		t.Fatalf("post-roundtrip CLAUDE.md: got %q want %q", got2, "changed")
	}
	got2, _ = os.ReadFile(filepath.Join(ws2.MemoryDir, "MEMORY.md"))
	if string(got2) != "note" {
		t.Fatalf("post-roundtrip MEMORY.md: got %q want %q", got2, "note")
	}
}

func TestSetup_EmptyWorkspaceWhenObjectMissing(t *testing.T) {
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ws, err := Setup(context.Background(), "ws_new", "cse_x", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer Teardown(context.Background(), ws, store)

	// ClaudeDir exists but has no files (only the memory subtree we mkdir).
	entries, err := os.ReadDir(ws.ClaudeDir)
	if err != nil {
		t.Fatal(err)
	}
	// Only the "projects" directory we created for MemoryDir.
	if len(entries) != 1 || entries[0].Name() != "projects" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ClaudeDir should only contain the projects/ scaffold; got %v", names)
	}
}

func TestProjHashDir_MatchesObservedClaudeCLILayout(t *testing.T) {
	// Locked-in expectation against an actual on-disk layout extracted from
	// a real workspace's claude-home.tar.gz. If the CLI ever changes its
	// hashing algorithm this test fails loudly.
	cwd := "/tmp/cc-broker/sess_cse_5e265cf6-9a9c-447e-b717-1f6dba7e3500/project"
	want := "-tmp-cc-broker-sess-cse-5e265cf6-9a9c-447e-b717-1f6dba7e3500-project"
	if got := projHashDir(cwd); got != want {
		t.Fatalf("projHashDir(%q) = %q, want %q", cwd, got, want)
	}
}

func TestSetupAndTeardown_PerSessionJsonlRoundTrip(t *testing.T) {
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	ws, err := Setup(ctx, "ws1", "cse_abc", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Simulate Claude CLI writing a session jsonl during the turn.
	jsonlPath := sessionJsonlLocalPath(ws)
	if err := os.MkdirAll(filepath.Dir(jsonlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonlPath, []byte("turn1-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Teardown(ctx, ws, store); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// Per-session jsonl must have been uploaded under its own key.
	jsonlKey := sessionJsonlKey("ws1", "cse_abc")
	uploadedJsonl, ok := fake.uploads[jsonlKey]
	if !ok {
		t.Fatalf("expected jsonl upload at %s; uploads=%v", jsonlKey, keysOf(fake.uploads))
	}
	if string(uploadedJsonl) != "turn1-line\n" {
		t.Fatalf("jsonl content mismatch: %q", uploadedJsonl)
	}

	// claude-home tarball must NOT contain the per-session subtree.
	tarBytes, ok := fake.uploads[claudeHomeKey("ws1")]
	if !ok {
		t.Fatalf("expected claude-home upload; uploads=%v", keysOf(fake.uploads))
	}
	if hasTarEntry(t, tarBytes, sessionSubtreeRel(ws)+"/") {
		t.Fatalf("claude-home tarball must not include session subtree %s", sessionSubtreeRel(ws))
	}

	// Round-trip: stage uploads as objects, fresh Setup must reconstruct
	// the jsonl from the per-session key.
	fake.putObject(claudeHomeKey("ws1"), tarBytes, "etag-after-t1")
	fake.objects[jsonlKey] = uploadedJsonl

	ws2, err := Setup(ctx, "ws1", "cse_abc", store)
	if err != nil {
		t.Fatalf("Setup #2: %v", err)
	}
	defer Teardown(ctx, ws2, store)
	got, err := os.ReadFile(sessionJsonlLocalPath(ws2))
	if err != nil || string(got) != "turn1-line\n" {
		t.Fatalf("post-roundtrip jsonl: got=%q err=%v", got, err)
	}
}

func TestTeardown_TwoSessionsDoNotOverwriteEachOther(t *testing.T) {
	// Workspace W has two concurrent sessions A and B. Each writes its own
	// jsonl. Whichever Teardown runs second must not destroy the other's
	// jsonl. With per-session keys, each lives in its own object.
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	wsA, err := Setup(ctx, "wsX", "cse_A", store)
	if err != nil {
		t.Fatalf("Setup A: %v", err)
	}
	wsB, err := Setup(ctx, "wsX", "cse_B", store)
	if err != nil {
		t.Fatalf("Setup B: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(sessionJsonlLocalPath(wsA)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionJsonlLocalPath(wsA), []byte("A-data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sessionJsonlLocalPath(wsB)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionJsonlLocalPath(wsB), []byte("B-data\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A finishes first, then B.
	if err := Teardown(ctx, wsA, store); err != nil {
		t.Fatalf("Teardown A: %v", err)
	}
	if err := Teardown(ctx, wsB, store); err != nil {
		t.Fatalf("Teardown B: %v", err)
	}

	// Both jsonls present at distinct keys.
	if string(fake.uploads[sessionJsonlKey("wsX", "cse_A")]) != "A-data\n" {
		t.Fatalf("A jsonl missing or wrong: uploads=%v", keysOf(fake.uploads))
	}
	if string(fake.uploads[sessionJsonlKey("wsX", "cse_B")]) != "B-data\n" {
		t.Fatalf("B jsonl missing or wrong: uploads=%v", keysOf(fake.uploads))
	}
}

// hasTarEntry reports whether a tar.gz blob contains an entry with the given
// name (matched as a prefix to handle both "dir" and "dir/" forms).
func hasTarEntry(t *testing.T, tarGz []byte, namePrefix string) bool {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(tarGz))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if strings.HasPrefix(hdr.Name, namePrefix) {
			return true
		}
	}
}

func TestTeardown_ConcurrentClaudeHomeWriteDropsLoserChanges(t *testing.T) {
	// Two sessions in the same workspace both Setup, both modify a memory
	// file, both Teardown. With optimistic locking, the first Teardown wins;
	// the second sees ETag mismatch and drops its claude-home upload (logs).
	// Per-session jsonls remain untouched.
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	// Pre-existing claude-home with a known ETag both sessions will see.
	fake.putObject(claudeHomeKey("wsX"), makeTarGz(t, map[string]string{
		"memory.md": "v0",
	}), "etag-orig")

	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	wsA, err := Setup(ctx, "wsX", "cse_A", store)
	if err != nil {
		t.Fatalf("Setup A: %v", err)
	}
	wsB, err := Setup(ctx, "wsX", "cse_B", store)
	if err != nil {
		t.Fatalf("Setup B: %v", err)
	}

	// Each session modifies the shared memory file differently.
	if err := os.WriteFile(filepath.Join(wsA.ClaudeDir, "memory.md"), []byte("A-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsB.ClaudeDir, "memory.md"), []byte("B-edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A teardown wins (fake auto-bumps ETag, but we simulate by forcing the
	// new ETag after A uploads). To exercise the optimistic-lock path we
	// rotate the ETag between A's and B's PUT.
	if err := Teardown(ctx, wsA, store); err != nil {
		t.Fatalf("Teardown A: %v", err)
	}
	// A's upload was unconditional from A's POV (it had etag-orig that
	// matched). The fake doesn't bump etags on PUT, so simulate the bump:
	fake.etags[claudeHomeKey("wsX")] = "etag-after-A"

	// B teardown: still has wsB.claudeHomeETag = "etag-orig", but server now
	// has "etag-after-A" → 412 → drop.
	if err := Teardown(ctx, wsB, store); err != nil {
		t.Fatalf("Teardown B: %v", err)
	}

	// Final state: A's upload is the latest claude-home in `uploads`.
	// B's PUT was rejected (412) → no update beyond A's.
	uploads := keysOf(fake.uploads)
	uploadedTimes := 0
	for _, k := range uploads {
		if k == claudeHomeKey("wsX") {
			uploadedTimes++
		}
	}
	if uploadedTimes != 1 {
		t.Fatalf("expected exactly 1 successful claude-home PUT (A's), got %d (uploads=%v)", uploadedTimes, uploads)
	}

	// Crucially, B's PUT body was NEVER captured by the fake — verify by
	// content check on the recorded upload.
	got := fake.uploads[claudeHomeKey("wsX")]
	if !hasTarFileContent(t, got, "memory.md", "A-edit") {
		t.Fatalf("expected memory.md=A-edit in surviving upload; got something else")
	}
}

func TestTeardown_FreshWorkspaceCreateOnlyContention(t *testing.T) {
	// Two sessions both observe a 404 on Setup (no claude-home yet). Each
	// uploads with IfNoneMatch:"*". The first wins; the second sees the
	// object now exists and gets 412 → drop.
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ctx := context.Background()
	wsA, err := Setup(ctx, "wsFresh", "cse_A", store)
	if err != nil {
		t.Fatalf("Setup A: %v", err)
	}
	wsB, err := Setup(ctx, "wsFresh", "cse_B", store)
	if err != nil {
		t.Fatalf("Setup B: %v", err)
	}
	if wsA.claudeHomeETag != "" || wsB.claudeHomeETag != "" {
		t.Fatalf("expected empty ETag on 404 Setup; got A=%q B=%q", wsA.claudeHomeETag, wsB.claudeHomeETag)
	}

	if err := os.WriteFile(filepath.Join(wsA.ClaudeDir, "marker.md"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsB.ClaudeDir, "marker.md"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Teardown(ctx, wsA, store); err != nil {
		t.Fatalf("Teardown A: %v", err)
	}
	// After A's create-only PUT, the object exists. B's IfNoneMatch:"*" must
	// now fail with 412.
	if err := Teardown(ctx, wsB, store); err != nil {
		t.Fatalf("Teardown B: %v", err)
	}

	got := fake.uploads[claudeHomeKey("wsFresh")]
	if !hasTarFileContent(t, got, "marker.md", "A") {
		t.Fatalf("first writer should win; expected marker.md=A in upload")
	}
}

// hasTarFileContent inspects a tar.gz blob and returns true iff it contains
// a regular-file entry named `name` whose contents equal `want`.
func hasTarFileContent(t *testing.T, tarGz []byte, name, want string) bool {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(tarGz))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name != name {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %s: %v", name, err)
		}
		return string(buf) == want
	}
}

func TestTeardown_UploadFailureStillCleansTempDir(t *testing.T) {
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	ws, err := Setup(context.Background(), "ws_fail", "cse_y", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Now make the upstream PUT fail. Teardown must log + continue, returning
	// nil and removing TempDir even though the upload failed.
	fake.failPUT = true

	if err := Teardown(context.Background(), ws, store); err != nil {
		t.Fatalf("Teardown: want nil even when upload fails, got %v", err)
	}
	if _, err := os.Stat(ws.TempDir); !os.IsNotExist(err) {
		t.Fatalf("TempDir should be removed even after upload failure; err=%v", err)
	}
	if len(fake.uploads) != 0 {
		t.Fatalf("no upload should have been recorded; got %d", len(fake.uploads))
	}
}
