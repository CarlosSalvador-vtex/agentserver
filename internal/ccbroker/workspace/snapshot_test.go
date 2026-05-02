package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTakeAndDiffSnapshot(t *testing.T) {
	dir := t.TempDir()

	// Initial state: a/foo.txt + b/bar.txt
	mustWrite(t, filepath.Join(dir, "a", "foo.txt"), "v1")
	mustWrite(t, filepath.Join(dir, "b", "bar.txt"), "v1")

	snap := TakeSnapshot(dir)
	if got := len(snap); got != 2 {
		t.Fatalf("expected 2 files in snapshot, got %d", got)
	}

	// Mutate: change foo.txt, add c/baz.txt, remove b/bar.txt
	mustWrite(t, filepath.Join(dir, "a", "foo.txt"), "v2-changed")
	mustWrite(t, filepath.Join(dir, "c", "baz.txt"), "new")
	if err := os.Remove(filepath.Join(dir, "b", "bar.txt")); err != nil {
		t.Fatal(err)
	}

	changes := DiffSnapshot(dir, snap)

	byKind := map[string]int{}
	for _, c := range changes {
		byKind[c.Kind]++
	}
	if byKind["added"] != 1 || byKind["modified"] != 1 || byKind["removed"] != 1 {
		t.Fatalf("unexpected kind distribution: %v", byKind)
	}
}

func TestDiffSnapshotEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	snap := TakeSnapshot(dir)
	if got := DiffSnapshot(dir, snap); len(got) != 0 {
		t.Fatalf("expected 0 changes, got %d", len(got))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
