package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
)

// FileInfo captures the identity of a file at snapshot time.
type FileInfo struct {
	ModTime int64  // Unix nanoseconds
	Size    int64
	SHA256  string // hex
}

// FileChange is one entry in a snapshot diff.
type FileChange struct {
	Path    string // absolute path
	RelPath string // path relative to the snapshot root
	Kind    string // "added" | "modified" | "removed"
}

// TakeSnapshot walks `dir` and records (mtime, size, sha256) for every regular
// file. Symlinks and directories are skipped. The returned map is keyed by
// path relative to `dir`.
func TakeSnapshot(dir string) map[string]FileInfo {
	out := make(map[string]FileInfo)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		out[rel] = FileInfo{
			ModTime: info.ModTime().UnixNano(),
			Size:    info.Size(),
			SHA256:  hashFile(path),
		}
		return nil
	})
	return out
}

// DiffSnapshot scans `dir` and returns every file that was added, modified
// (size or sha256 changed), or removed since `old` was captured.
func DiffSnapshot(dir string, old map[string]FileInfo) []FileChange {
	current := TakeSnapshot(dir)
	var changes []FileChange

	for rel, cur := range current {
		prev, existed := old[rel]
		if !existed {
			changes = append(changes, FileChange{
				Path:    filepath.Join(dir, rel),
				RelPath: rel,
				Kind:    "added",
			})
			continue
		}
		if prev.Size != cur.Size || prev.SHA256 != cur.SHA256 {
			changes = append(changes, FileChange{
				Path:    filepath.Join(dir, rel),
				RelPath: rel,
				Kind:    "modified",
			})
		}
	}
	for rel := range old {
		if _, stillThere := current[rel]; !stillThere {
			changes = append(changes, FileChange{
				Path:    filepath.Join(dir, rel),
				RelPath: rel,
				Kind:    "removed",
			})
		}
	}
	return changes
}

func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
