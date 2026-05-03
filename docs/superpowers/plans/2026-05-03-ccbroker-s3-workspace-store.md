# ccbroker S3-compatible Workspace Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace OpenViking as ccbroker's per-turn workspace persistence backend with a generic S3-compatible object store. One `claude-home.tar.gz` per workspace; OpenViking code, Helm subchart, and docker-compose service deleted.

**Architecture:** New `internal/ccbroker/workspace/s3store.go` wraps `minio-go/v7`. `workspace.Setup` does one streaming `GET → gunzip → untar` into `ClaudeDir`. `workspace.Teardown` does one streaming `tar → gzip → PUT` from `ClaudeDir`. `ProjectDir` is created empty (its only role is being a stable cwd for Claude CLI's proj_hash; no files are synced for it because every built-in filesystem tool is disabled). The `Server` struct holds one `*S3Store` constructed at startup.

**Tech Stack:** Go 1.22+, `github.com/minio/minio-go/v7`, stdlib `archive/tar` + `compress/gzip` + `io.Pipe`.

**Spec:** `docs/superpowers/specs/2026-05-03-ccbroker-s3-workspace-store-design.md`

---

## File Map

**Create:**
- `internal/ccbroker/workspace/s3store.go` — S3 client + tar.gz upload/download
- `internal/ccbroker/workspace/s3store_test.go` — httptest-fake S3 unit tests

**Modify:**
- `internal/ccbroker/workspace/workspace.go` — Setup/Teardown signatures, drop snapshot field
- `internal/ccbroker/workspace/workspace_test.go` — fake S3 instead of fake Viking
- `internal/ccbroker/config.go` — drop `OPENVIKING_*`, add `S3_*` fields
- `internal/ccbroker/server.go` — hold `*S3Store`, construct in NewServer
- `internal/ccbroker/handler_turns.go` — use `s.store`, drop per-request VikingClient
- `internal/ccbroker/handler_turns_test.go` — change seam parameter types
- `internal/ccbroker/tools/context.go` — drop `Viking` field
- `internal/ccbroker/tools/workspace.go` — update workspace_write description
- `go.mod` / `go.sum` — add `minio-go/v7`
- `docker-compose.yml` — drop `openviking`, add `minio` + `minio-init` services
- `deploy/helm/agentserver/Chart.yaml` — drop openviking dependency
- `deploy/helm/agentserver/values.yaml` — drop openviking blocks, add `ccbroker.s3`
- `deploy/helm/agentserver/templates/cc-broker.yaml` — replace OpenViking env block with S3 env

**Delete:**
- `internal/ccbroker/workspace/viking_client.go`
- `internal/ccbroker/workspace/snapshot.go`
- `internal/ccbroker/workspace/snapshot_test.go`
- `deploy/helm/agentserver/charts/openviking/` (entire directory)

---

## Task 1: Add minio-go dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dep**

Run:
```bash
cd /root/agentserver && go get github.com/minio/minio-go/v7@latest
```
Expected: new entry in `go.mod`, populated `go.sum`.

- [ ] **Step 2: Verify build still works**

Run: `cd /root/agentserver && go build ./...`
Expected: success, no errors.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add go.mod go.sum && git commit -m "chore(ccbroker): add minio-go/v7 dependency"
```

---

## Task 2: S3Store skeleton + DownloadTarGz happy path (TDD)

**Files:**
- Create: `internal/ccbroker/workspace/s3store.go`
- Create: `internal/ccbroker/workspace/s3store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/ccbroker/workspace/s3store_test.go`:

```go
package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeS3 serves the minimal subset of the S3 API that S3Store touches.
//   GET  /<bucket>/<key>  → bytes the test pre-loaded, or 404 NoSuchKey
//   PUT  /<bucket>/<key>  → captures bytes into uploads
type fakeS3 struct {
	bucket  string
	objects map[string][]byte // key → content (pre-loaded responses)
	uploads map[string][]byte // key → content (captured PUTs)
}

func newFakeS3(bucket string) *fakeS3 {
	return &fakeS3{
		bucket:  bucket,
		objects: make(map[string][]byte),
		uploads: make(map[string][]byte),
	}
}

func (f *fakeS3) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path-style: /<bucket>/<key>
		path := strings.TrimPrefix(r.URL.Path, "/")
		path, _ = url.PathUnescape(path)
		path = strings.TrimPrefix(path, f.bucket+"/")

		switch r.Method {
		case http.MethodGet, http.MethodHead:
			data, ok := f.objects[path]
			if !ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			f.uploads[path] = body
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// makeTarGz builds an in-memory tar.gz from the given path→content map.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func newTestStore(t *testing.T, fake *fakeS3) (*S3Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	u, _ := url.Parse(srv.URL)
	store, err := NewS3Store(S3Config{
		Endpoint:        u.Host,
		Region:          "us-east-1",
		Bucket:          fake.bucket,
		AccessKeyID:     "test",
		SecretAccessKey: "test",
		UseSSL:          false,
		PathStyle:       true,
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store, srv
}

func TestDownloadTarGz_HappyPath(t *testing.T) {
	fake := newFakeS3("ccbroker")
	fake.objects["workspaces/ws1/claude-home.tar.gz"] = makeTarGz(t, map[string]string{
		"CLAUDE.md":            "global-instructions",
		"projects/p/session.jsonl": "line1\nline2\n",
	})

	store, srv := newTestStore(t, fake)
	defer srv.Close()

	dest := t.TempDir()
	if err := store.DownloadTarGz(context.Background(), "workspaces/ws1/claude-home.tar.gz", dest); err != nil {
		t.Fatalf("DownloadTarGz: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "CLAUDE.md"))
	if err != nil || string(got) != "global-instructions" {
		t.Fatalf("CLAUDE.md mismatch: got=%q err=%v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dest, "projects/p/session.jsonl"))
	if err != nil || string(got) != "line1\nline2\n" {
		t.Fatalf("session.jsonl mismatch: got=%q err=%v", got, err)
	}
}
```

- [ ] **Step 2: Run, expect compile failure (S3Store / S3Config / NewS3Store undefined)**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestDownloadTarGz_HappyPath -v`
Expected: build error mentioning `undefined: S3Store` (or similar).

- [ ] **Step 3: Implement minimum S3Store + DownloadTarGz**

Create `internal/ccbroker/workspace/s3store.go`:

```go
package workspace

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config carries the S3-compatible endpoint configuration. Endpoint is
// host:port without scheme; UseSSL controls https vs http. PathStyle must
// be true for MinIO and most on-prem S3 implementations; false for AWS S3
// (virtual-hosted-style is required) and most public clouds.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	PathStyle       bool
}

// S3Store is the workspace persistence backend. One instance is held by the
// server and shared across all turns.
type S3Store struct {
	client *minio.Client
	bucket string
}

func NewS3Store(cfg S3Config) (*S3Store, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("s3: endpoint required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket required")
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookup(cfg.PathStyle),
	})
	if err != nil {
		return nil, fmt.Errorf("s3: new client: %w", err)
	}
	return &S3Store{client: c, bucket: cfg.Bucket}, nil
}

func bucketLookup(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupDNS
}

// DownloadTarGz streams the object at key, gunzip-untars it into destDir.
// Returns nil if the object does not exist (treated as empty workspace).
// Tar entries with paths escaping destDir are skipped.
func (s *S3Store) DownloadTarGz(ctx context.Context, key, destDir string) error {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3: get object: %w", err)
	}
	defer obj.Close()

	gr, err := gzip.NewReader(obj)
	if err != nil {
		// minio-go's GetObject is lazy: it doesn't return an error until first
		// Read. A 404 surfaces here as a gzip read failure on an XML error doc.
		// Discriminate on the underlying minio error.
		if errResp := minio.ToErrorResponse(err); errResp.Code == "NoSuchKey" {
			return nil
		}
		// gzip.NewReader returned its own error (e.g. "unexpected EOF" on the
		// XML error body). Re-check by stat-ing the object.
		if _, statErr := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); statErr != nil {
			if minio.ToErrorResponse(statErr).Code == "NoSuchKey" {
				return nil
			}
		}
		return fmt.Errorf("s3: gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("s3: tar next: %w", err)
		}
		dest, ok := safeJoin(destDir, hdr.Name)
		if !ok {
			fmt.Fprintf(os.Stderr, "s3: skipping unsafe tar entry: %q\n", hdr.Name)
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("s3: mkdir %s: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("s3: mkdir parent %s: %w", dest, err)
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("s3: create %s: %w", dest, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("s3: copy %s: %w", dest, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("s3: close %s: %w", dest, err)
			}
		default:
			// Skip symlinks, char/block devices, etc.
		}
	}
}

// safeJoin returns the cleaned absolute join of base and rel, plus a bool that
// is false if rel resolves outside base. Rejects absolute paths and any rel
// containing ".." segments that escape base.
func safeJoin(base, rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return "", false
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return "", false
	}
	full := filepath.Join(base, cleaned)
	rel2, err := filepath.Rel(base, full)
	if err != nil || rel2 == ".." || strings.HasPrefix(rel2, "..") {
		return "", false
	}
	return full, true
}
```

- [ ] **Step 4: Run, expect pass**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestDownloadTarGz_HappyPath -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/s3store.go internal/ccbroker/workspace/s3store_test.go && git commit -m "feat(ccbroker/workspace): S3Store with DownloadTarGz happy path"
```

---

## Task 3: DownloadTarGz handles 404 as empty workspace (TDD)

**Files:**
- Modify: `internal/ccbroker/workspace/s3store_test.go`

- [ ] **Step 1: Add the failing test**

Append to `s3store_test.go`:

```go
func TestDownloadTarGz_NotFoundIsEmpty(t *testing.T) {
	fake := newFakeS3("ccbroker")
	// no objects pre-loaded → every GET returns 404
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	dest := t.TempDir()
	if err := store.DownloadTarGz(context.Background(), "workspaces/missing/claude-home.tar.gz", dest); err != nil {
		t.Fatalf("DownloadTarGz on missing key: want nil, got %v", err)
	}
	// destDir should remain empty
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("destDir should be empty, got %d entries", len(entries))
	}
}
```

- [ ] **Step 2: Run, expect pass (already implemented in Task 2)**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestDownloadTarGz_NotFoundIsEmpty -v`
Expected: PASS. (If FAIL, the 404 detection in `DownloadTarGz` needs fixing — the gzip.NewReader path should catch the StatObject NoSuchKey case.)

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/s3store_test.go && git commit -m "test(ccbroker/workspace): DownloadTarGz returns nil on NoSuchKey"
```

---

## Task 4: DownloadTarGz rejects path-traversal entries (TDD)

**Files:**
- Modify: `internal/ccbroker/workspace/s3store_test.go`

- [ ] **Step 1: Add the failing test**

Append to `s3store_test.go`:

```go
func TestDownloadTarGz_RejectsPathTraversal(t *testing.T) {
	fake := newFakeS3("ccbroker")
	fake.objects["workspaces/ws1/claude-home.tar.gz"] = makeTarGz(t, map[string]string{
		"../escape.txt":   "should-not-write",
		"/abs/escape.txt": "should-not-write",
		"safe.txt":        "ok",
	})

	store, srv := newTestStore(t, fake)
	defer srv.Close()

	dest := t.TempDir()
	parent := filepath.Dir(dest)

	if err := store.DownloadTarGz(context.Background(), "workspaces/ws1/claude-home.tar.gz", dest); err != nil {
		t.Fatalf("DownloadTarGz: %v", err)
	}

	// Safe entry written
	if _, err := os.Stat(filepath.Join(dest, "safe.txt")); err != nil {
		t.Fatalf("safe.txt missing: %v", err)
	}
	// Escape attempts must NOT have written outside dest
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal write succeeded; want IsNotExist, got %v", err)
	}
	if _, err := os.Stat("/abs/escape.txt"); !os.IsNotExist(err) {
		t.Fatalf("absolute-path write succeeded; want IsNotExist, got %v", err)
	}
}
```

- [ ] **Step 2: Run, expect pass (safeJoin rejects both)**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestDownloadTarGz_RejectsPathTraversal -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/s3store_test.go && git commit -m "test(ccbroker/workspace): DownloadTarGz skips path-traversal entries"
```

---

## Task 5: UploadTarGz round-trip (TDD)

**Files:**
- Modify: `internal/ccbroker/workspace/s3store.go` (add UploadTarGz)
- Modify: `internal/ccbroker/workspace/s3store_test.go` (add round-trip test)

- [ ] **Step 1: Write the failing test**

Append to `s3store_test.go`:

```go
func TestUploadTarGz_RoundTrip(t *testing.T) {
	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "CLAUDE.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "skills", "foo.md"), []byte("a skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := "workspaces/ws1/claude-home.tar.gz"
	if err := store.UploadTarGz(context.Background(), src, key); err != nil {
		t.Fatalf("UploadTarGz: %v", err)
	}

	// Round-trip: stage the captured upload as if it were a pre-existing object,
	// then download into a fresh dir and compare.
	uploaded, ok := fake.uploads[key]
	if !ok {
		t.Fatalf("no upload captured; uploads=%v", keysOf(fake.uploads))
	}
	fake.objects[key] = uploaded

	dest := t.TempDir()
	if err := store.DownloadTarGz(context.Background(), key, dest); err != nil {
		t.Fatalf("DownloadTarGz: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "CLAUDE.md"))
	if string(got) != "hi" {
		t.Fatalf("CLAUDE.md round-trip mismatch: %q", got)
	}
	got, _ = os.ReadFile(filepath.Join(dest, "skills", "foo.md"))
	if string(got) != "a skill" {
		t.Fatalf("skills/foo.md round-trip mismatch: %q", got)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run, expect compile failure (UploadTarGz undefined)**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestUploadTarGz_RoundTrip -v`
Expected: build error mentioning `UploadTarGz`.

- [ ] **Step 3: Implement UploadTarGz**

Append to `internal/ccbroker/workspace/s3store.go`:

```go
// UploadTarGz walks srcDir, packages it as a streaming tar.gz, and PUTs it to
// the given key. Symlinks are skipped. File modes are normalized to 0644
// (regular) / 0755 (dir). Failures during walk are logged and the offending
// file is skipped; the upload still completes with whatever was packed.
func (s *S3Store) UploadTarGz(ctx context.Context, srcDir, key string) error {
	pr, pw := io.Pipe()

	go func() {
		gw := gzip.NewWriter(pw)
		tw := tar.NewWriter(gw)
		err := writeTarball(srcDir, tw)
		_ = tw.Close()
		_ = gw.Close()
		_ = pw.CloseWithError(err)
	}()

	_, err := s.client.PutObject(ctx, s.bucket, key, pr, -1, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return fmt.Errorf("s3: put object: %w", err)
	}
	return nil
}

func writeTarball(srcDir string, tw *tar.Writer) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "s3: walk error at %s: %v (skipping)\n", path, walkErr)
			return nil
		}
		if path == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "s3: stat %s: %v (skipping)\n", path, err)
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		hdr := &tar.Header{Name: filepath.ToSlash(rel)}
		switch {
		case d.IsDir():
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0o755
			hdr.Name += "/"
		case info.Mode().IsRegular():
			hdr.Typeflag = tar.TypeReg
			hdr.Mode = 0o644
			hdr.Size = info.Size()
		default:
			return nil
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write header %s: %w", rel, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "s3: open %s: %v (skipping)\n", path, err)
				return nil
			}
			_, err = io.Copy(tw, f)
			_ = f.Close()
			if err != nil {
				return fmt.Errorf("copy %s: %w", rel, err)
			}
		}
		return nil
	})
}
```

You also need to import `os` for `os.DirEntry`. The file already imports `os`, so no change needed.

- [ ] **Step 4: Run, expect pass**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestUploadTarGz_RoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/s3store.go internal/ccbroker/workspace/s3store_test.go && git commit -m "feat(ccbroker/workspace): S3Store.UploadTarGz with round-trip test"
```

---

## Task 6: UploadTarGz skips symlinks (TDD)

**Files:**
- Modify: `internal/ccbroker/workspace/s3store_test.go`

- [ ] **Step 1: Add the failing test**

Append to `s3store_test.go`:

```go
func TestUploadTarGz_SkipsSymlinks(t *testing.T) {
	fake := newFakeS3("ccbroker")
	store, srv := newTestStore(t, fake)
	defer srv.Close()

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	key := "workspaces/ws1/claude-home.tar.gz"
	if err := store.UploadTarGz(context.Background(), src, key); err != nil {
		t.Fatalf("UploadTarGz: %v", err)
	}

	// Inspect the captured upload: walk the tar entries by name.
	uploaded := fake.uploads[key]
	gr, err := gzip.NewReader(bytes.NewReader(uploaded))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names[hdr.Name] = true
	}
	if !names["real.txt"] {
		t.Fatalf("real.txt missing from upload; names=%v", names)
	}
	if names["link"] {
		t.Fatalf("symlink entry should have been skipped; names=%v", names)
	}
}
```

- [ ] **Step 2: Run, expect pass (already implemented)**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -run TestUploadTarGz_SkipsSymlinks -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/s3store_test.go && git commit -m "test(ccbroker/workspace): UploadTarGz skips symlinks"
```

---

## Task 7: Refactor workspace.Setup / Teardown to use S3Store

**Files:**
- Modify: `internal/ccbroker/workspace/workspace.go`
- Modify: `internal/ccbroker/workspace/workspace_test.go`

This task replaces the OpenViking call sites in Setup/Teardown with S3Store, drops the `snapshot` field, and rewrites the existing test against `fakeS3`.

- [ ] **Step 1: Replace `internal/ccbroker/workspace/workspace.go` in full**

Overwrite the file with:

```go
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
// claude-home tarball. One workspace, one object.
func claudeHomeKey(workspaceID string) string {
	return fmt.Sprintf("workspaces/%s/claude-home.tar.gz", workspaceID)
}

// Setup creates the temp directory tree and downloads the workspace's
// claude-home tarball from S3. The returned Workspace must be passed to
// Teardown so the temp directory is removed and ClaudeDir is uploaded back.
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
		return nil, fmt.Errorf("download claude-home: %w", err)
	}

	return ws, nil
}

// Teardown packages ClaudeDir as a tar.gz, uploads it to S3, then removes
// the temp dir. Upload failures are logged but do not propagate — a flaky
// upload must not block the caller's turn response. TempDir is always
// removed.
func Teardown(ctx context.Context, ws *Workspace, store *S3Store) error {
	if ws == nil {
		return nil
	}
	defer func() { _ = os.RemoveAll(ws.TempDir) }()

	if err := store.UploadTarGz(ctx, ws.ClaudeDir, claudeHomeKey(ws.WorkspaceID)); err != nil {
		fmt.Fprintf(os.Stderr, "workspace.Teardown: upload claude-home: %v\n", err)
	}
	return nil
}
```

- [ ] **Step 2: Replace `internal/ccbroker/workspace/workspace_test.go` in full**

Overwrite the file with:

```go
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupAndTeardown_RoundTrip(t *testing.T) {
	old := TempDirBase
	TempDirBase = t.TempDir()
	defer func() { TempDirBase = old }()

	fake := newFakeS3("ccbroker")
	// Pre-load one file so the first Setup has something to download.
	fake.objects[claudeHomeKey("ws1")] = makeTarGz(t, map[string]string{
		"CLAUDE.md": "global-claude",
	})

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
	// mutated content back.
	fake.objects[claudeHomeKey("ws1")] = fake.uploads[claudeHomeKey("ws1")]
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
```

The `fakeS3`, `newFakeS3`, `makeTarGz`, `newTestStore`, `keysOf` helpers live in `s3store_test.go` (same package), so this file uses them directly.

- [ ] **Step 3: Run only the workspace package — expect compile failures elsewhere**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -v`
Expected: workspace package tests PASS. (The wider build is broken because callers still reference the old `*VikingClient` signature; that gets fixed in later tasks.)

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/workspace/workspace.go internal/ccbroker/workspace/workspace_test.go && git commit -m "refactor(ccbroker/workspace): Setup/Teardown use S3Store + tar.gz"
```

---

## Task 8: Delete OpenViking client and snapshot files

**Files:**
- Delete: `internal/ccbroker/workspace/viking_client.go`
- Delete: `internal/ccbroker/workspace/snapshot.go`
- Delete: `internal/ccbroker/workspace/snapshot_test.go`

- [ ] **Step 1: Verify no in-package references remain**

Run: `cd /root/agentserver && grep -n "VikingClient\|TakeSnapshot\|DiffSnapshot\|FileChange\|FileInfo" internal/ccbroker/workspace/*.go | grep -v _test.go | grep -v viking_client.go | grep -v snapshot.go`
Expected: no output. If anything appears, fix it before deleting.

- [ ] **Step 2: Delete the three files**

```bash
cd /root/agentserver && rm internal/ccbroker/workspace/viking_client.go internal/ccbroker/workspace/snapshot.go internal/ccbroker/workspace/snapshot_test.go
```

- [ ] **Step 3: Verify workspace package still builds + tests pass**

Run: `cd /root/agentserver && go test ./internal/ccbroker/workspace/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add -A internal/ccbroker/workspace/ && git commit -m "chore(ccbroker/workspace): delete OpenViking client and snapshot diffing"
```

---

## Task 9: Drop Viking field from tools.Context, update tool description

**Files:**
- Modify: `internal/ccbroker/tools/context.go`
- Modify: `internal/ccbroker/tools/workspace.go`

- [ ] **Step 1: Replace `internal/ccbroker/tools/context.go` in full**

Overwrite with:

```go
package tools

import (
	"net/http"

	"github.com/agentserver/agentserver/internal/ccbroker/workspace"
)

// Context bundles the per-turn dependencies that tool handlers close over.
// Constructed in handler_turns once per request and discarded after the turn.
type Context struct {
	SessionID           string
	WorkspaceID         string
	IMChannelID         string
	IMUserID            string
	ExecutorRegistryURL string
	AgentserverURL      string
	IMBridgeURL         string
	InternalAPISecret   string
	Workspace           *workspace.Workspace // for workspace_* tools
	HTTP                *http.Client         // shared HTTP client
}
```

The `Viking *workspace.VikingClient` field is removed; no tool reads it.

- [ ] **Step 2: Update the workspace_write tool description**

In `internal/ccbroker/tools/workspace.go`, change line 40:

Old:
```go
agentsdk.Tool[workspaceWriteInput]("workspace_write",
    "Write a file to the workspace context. Persists across sessions via OpenViking.",
```

New:
```go
agentsdk.Tool[workspaceWriteInput]("workspace_write",
    "Write a file to the workspace context. Persists across sessions.",
```

- [ ] **Step 3: Verify tools package still builds**

Run: `cd /root/agentserver && go build ./internal/ccbroker/tools/...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/tools/context.go internal/ccbroker/tools/workspace.go && git commit -m "refactor(ccbroker/tools): drop Viking field, update tool description"
```

---

## Task 10: Replace OpenViking config with S3 config

**Files:**
- Modify: `internal/ccbroker/config.go`

- [ ] **Step 1: Replace `internal/ccbroker/config.go` in full**

Overwrite with:

```go
package ccbroker

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                string
	DatabaseURL         string
	LogLevel            slog.Level
	ExecutorRegistryURL string
	AgentserverURL      string
	S3Endpoint          string
	S3Region            string
	S3Bucket            string
	S3AccessKeyID       string
	S3SecretAccessKey   string
	S3UseSSL            bool
	S3PathStyle         bool
	IMBridgeURL         string
	IMBridgeSecret      string
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Port:        envOr("CCBROKER_PORT", "8085"),
		DatabaseURL: os.Getenv("CCBROKER_DATABASE_URL"),
		LogLevel:    slog.LevelInfo,
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("CCBROKER_DATABASE_URL is required")
	}
	cfg.ExecutorRegistryURL = envOr("CCBROKER_EXECUTOR_REGISTRY_URL", "http://localhost:8084")
	cfg.AgentserverURL = envOr("CCBROKER_AGENTSERVER_URL", "http://localhost:8080")

	cfg.S3Endpoint = os.Getenv("CCBROKER_S3_ENDPOINT")
	cfg.S3Region = os.Getenv("CCBROKER_S3_REGION")
	cfg.S3Bucket = os.Getenv("CCBROKER_S3_BUCKET")
	cfg.S3AccessKeyID = os.Getenv("CCBROKER_S3_ACCESS_KEY_ID")
	cfg.S3SecretAccessKey = os.Getenv("CCBROKER_S3_SECRET_ACCESS_KEY")
	cfg.S3UseSSL = envBool("CCBROKER_S3_USE_SSL", true)
	cfg.S3PathStyle = envBool("CCBROKER_S3_PATH_STYLE", false)
	if cfg.S3Endpoint == "" {
		return cfg, fmt.Errorf("CCBROKER_S3_ENDPOINT is required")
	}
	if cfg.S3Bucket == "" {
		return cfg, fmt.Errorf("CCBROKER_S3_BUCKET is required")
	}

	cfg.IMBridgeURL = os.Getenv("CCBROKER_IMBRIDGE_URL")
	cfg.IMBridgeSecret = os.Getenv("INTERNAL_API_SECRET")
	if v := os.Getenv("CCBROKER_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		}
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
```

- [ ] **Step 2: Verify package builds (other files in this package will still error on `vc` references — that's Task 11)**

Run: `cd /root/agentserver && go build ./internal/ccbroker/`
Expected: errors in `handler_turns.go` referring to `OpenVikingURL` / `VikingClient`. That's expected — Task 11 fixes them.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/config.go && git commit -m "refactor(ccbroker/config): replace OpenViking env with S3 env"
```

---

## Task 11: Wire S3Store into Server + handler_turns

**Files:**
- Modify: `internal/ccbroker/server.go`
- Modify: `internal/ccbroker/handler_turns.go`
- Modify: `internal/ccbroker/handler_turns_test.go`

- [ ] **Step 1: Add `s3` field to Server, construct in NewServer**

Replace `internal/ccbroker/server.go` in full with:

```go
package ccbroker

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/agentserver/agentserver/internal/ccbroker/workspace"
)

type Server struct {
	config   Config
	store    *Store
	s3       *workspace.S3Store
	sse      *SSEBroker
	turnLock *TurnLock
	logger   *slog.Logger
}

func NewServer(cfg Config, store *Store) (*Server, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	s3, err := workspace.NewS3Store(workspace.S3Config{
		Endpoint:        cfg.S3Endpoint,
		Region:          cfg.S3Region,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		UseSSL:          cfg.S3UseSSL,
		PathStyle:       cfg.S3PathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("init s3 store: %w", err)
	}
	return &Server{
		config:   cfg,
		store:    store,
		s3:       s3,
		sse:      NewSSEBroker(),
		turnLock: NewTurnLock(),
		logger:   logger,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Post("/api/turns", s.handleProcessTurn)
	r.Post("/v1/sessions", s.handleCreateSession)

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

`NewServer` now returns `(*Server, error)`. Find every caller (`grep -rn "NewServer(" --include='*.go'`) and update to handle the error.

- [ ] **Step 2: Find and update `NewServer` callers**

Run: `cd /root/agentserver && grep -rn "ccbroker.NewServer(" --include="*.go"`

For each caller (typically `cmd/cc-broker/main.go`), change e.g.:
```go
srv := ccbroker.NewServer(cfg, store)
```
to:
```go
srv, err := ccbroker.NewServer(cfg, store)
if err != nil {
    log.Fatalf("init server: %v", err)
}
```

- [ ] **Step 3: Update test seam types in handler_turns.go**

In `internal/ccbroker/handler_turns.go`, change L21-22:

Old:
```go
workspaceSetup    = workspace.Setup
workspaceTeardown = workspace.Teardown
```
(unchanged — these still match the new signatures because `workspace.Setup` and `workspace.Teardown` themselves have new signatures.)

Change L107-108:

Old:
```go
// Set up the per-turn workspace (download OpenViking tree, snapshot ClaudeDir).
vc := workspace.NewVikingClient(s.config.OpenVikingURL, s.config.OpenVikingAPIKey)
ws, err := workspaceSetup(r.Context(), req.WorkspaceID, req.SessionID, vc)
```
New:
```go
// Set up the per-turn workspace (download claude-home tarball from S3).
ws, err := workspaceSetup(r.Context(), req.WorkspaceID, req.SessionID, s.s3)
```

Change L127:

Old:
```go
Workspace:           ws,
Viking:              vc,
HTTP:                http.DefaultClient,
```
New:
```go
Workspace:           ws,
HTTP:                http.DefaultClient,
```

Change L148, L156, L177 — replace every `workspaceTeardown(context.Background(), ws, vc)` with `workspaceTeardown(context.Background(), ws, s.s3)`.

- [ ] **Step 4: Update handler_turns_test.go seam signatures**

In `internal/ccbroker/handler_turns_test.go`, change L44 and L54:

Old (L44):
```go
workspaceSetup = func(_ context.Context, wid, sid string, _ *workspace.VikingClient) (*workspace.Workspace, error) {
```
New:
```go
workspaceSetup = func(_ context.Context, wid, sid string, _ *workspace.S3Store) (*workspace.Workspace, error) {
```

Old (L54):
```go
workspaceTeardown = func(_ context.Context, _ *workspace.Workspace, _ *workspace.VikingClient) error {
```
New:
```go
workspaceTeardown = func(_ context.Context, _ *workspace.Workspace, _ *workspace.S3Store) error {
```

- [ ] **Step 5: Build the whole tree, then run tests for the package**

Run: `cd /root/agentserver && go build ./...`
Expected: success.

Run: `cd /root/agentserver && go test ./internal/ccbroker/...`
Expected: PASS (the previously-skipped handler test stays skipped; everything else passes).

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && git add internal/ccbroker/ cmd/ && git commit -m "refactor(ccbroker): wire S3Store into Server and handler_turns"
```

---

## Task 12: Update docker-compose to MinIO for local dev

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Read current docker-compose.yml to find the openviking and ccbroker blocks**

Run: `cd /root/agentserver && grep -n "openviking\|cc-broker\|CCBROKER_OPENVIKING" docker-compose.yml`
Expected: line numbers for the openviking service block and the ccbroker env keys.

- [ ] **Step 2: Edit docker-compose.yml**

Remove the entire `openviking:` service block and any `volumes:` it declared.

Remove from cc-broker env:
```yaml
CCBROKER_OPENVIKING_URL: "http://openviking:1933"
CCBROKER_OPENVIKING_API_KEY: ""
```

Remove `openviking` from cc-broker `depends_on` if present.

Add a new `minio` and `minio-init` service alongside the existing services:
```yaml
  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    ports:
      - "9000:9000"
      - "9001:9001"
    volumes:
      - minio-data:/data

  minio-init:
    image: minio/mc:latest
    depends_on:
      - minio
    entrypoint:
      - sh
      - -c
      - |
        sleep 3
        mc alias set local http://minio:9000 minioadmin minioadmin
        mc mb -p local/ccbroker || true
```

Add to top-level `volumes:` block (or create one if it doesn't exist):
```yaml
volumes:
  minio-data:
```

Add to cc-broker `depends_on:` (alongside whatever else is there):
```yaml
      - minio-init
```

Add to cc-broker `environment:`:
```yaml
      CCBROKER_S3_ENDPOINT: "minio:9000"
      CCBROKER_S3_REGION: ""
      CCBROKER_S3_BUCKET: "ccbroker"
      CCBROKER_S3_USE_SSL: "false"
      CCBROKER_S3_PATH_STYLE: "true"
      CCBROKER_S3_ACCESS_KEY_ID: "minioadmin"
      CCBROKER_S3_SECRET_ACCESS_KEY: "minioadmin"
```

- [ ] **Step 3: Validate the compose file**

Run: `cd /root/agentserver && docker compose config -q`
Expected: no output (success). If errors: fix YAML indentation and re-validate.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add docker-compose.yml && git commit -m "build(compose): replace openviking with minio for local dev"
```

---

## Task 13: Update Helm chart — delete openviking subchart and rewire cc-broker env

**Files:**
- Delete: `deploy/helm/agentserver/charts/openviking/` (entire directory)
- Modify: `deploy/helm/agentserver/Chart.yaml`
- Modify: `deploy/helm/agentserver/values.yaml`
- Modify: `deploy/helm/agentserver/templates/cc-broker.yaml`

- [ ] **Step 1: Delete the openviking subchart**

Run: `cd /root/agentserver && rm -rf deploy/helm/agentserver/charts/openviking`

- [ ] **Step 2: Remove the openviking dependency from Chart.yaml**

Read `deploy/helm/agentserver/Chart.yaml`. Find the `dependencies:` block and delete the entry whose `name:` is `openviking` (and its `version`, `repository`, `condition` lines). If `dependencies:` becomes empty, delete the key.

- [ ] **Step 3: Update values.yaml**

Read `deploy/helm/agentserver/values.yaml`. Delete the entire top-level `openviking:` block. Inside the `ccbroker:` block, delete any `openviking:` sub-block.

Add inside `ccbroker:`:
```yaml
  s3:
    endpoint: ""           # required, e.g. "s3.amazonaws.com" or "minio.minio.svc:9000"
    region: ""             # required for AWS-style; "" is fine for MinIO
    bucket: ""             # required
    useSSL: true
    pathStyle: false       # true for MinIO / on-prem; false for AWS / OSS / COS
    existingSecret: ""     # k8s secret with keys: access_key_id, secret_access_key
```

- [ ] **Step 4: Update templates/cc-broker.yaml — replace the OpenViking env block**

Find the env block (currently around L93-110 — the `$ovURL` / `$ovEnabled` / `$ovAPIKey` template block + the `CCBROKER_OPENVIKING_URL` and `CCBROKER_OPENVIKING_API_KEY` env entries).

Delete that entire block.

In the same `env:` list, add:
```yaml
            - name: CCBROKER_S3_ENDPOINT
              value: {{ .Values.ccbroker.s3.endpoint | quote }}
            - name: CCBROKER_S3_REGION
              value: {{ .Values.ccbroker.s3.region | quote }}
            - name: CCBROKER_S3_BUCKET
              value: {{ .Values.ccbroker.s3.bucket | quote }}
            - name: CCBROKER_S3_USE_SSL
              value: {{ .Values.ccbroker.s3.useSSL | quote }}
            - name: CCBROKER_S3_PATH_STYLE
              value: {{ .Values.ccbroker.s3.pathStyle | quote }}
            - name: CCBROKER_S3_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: {{ .Values.ccbroker.s3.existingSecret }}
                  key: access_key_id
            - name: CCBROKER_S3_SECRET_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ .Values.ccbroker.s3.existingSecret }}
                  key: secret_access_key
```

(Match the indentation of the surrounding env entries exactly — Helm cares.)

- [ ] **Step 5: Update Chart.lock if it exists**

Run: `cd /root/agentserver/deploy/helm/agentserver && [ -f Chart.lock ] && helm dependency update . || true`
Expected: success or no-op.

- [ ] **Step 6: Render the chart with a sample values override and verify env appears**

Run:
```bash
cd /root/agentserver/deploy/helm/agentserver && helm template . \
  --set ccbroker.s3.endpoint=s3.amazonaws.com \
  --set ccbroker.s3.region=us-east-1 \
  --set ccbroker.s3.bucket=ccbroker-test \
  --set ccbroker.s3.existingSecret=ccbroker-s3 \
  --set ccbroker.s3.pathStyle=false \
  --set ccbroker.s3.useSSL=true \
  | grep -E "CCBROKER_S3_|CCBROKER_OPENVIKING"
```
Expected: only `CCBROKER_S3_*` lines, no `CCBROKER_OPENVIKING_*`.

- [ ] **Step 7: Commit**

```bash
cd /root/agentserver && git add deploy/helm/agentserver/ && git commit -m "build(helm): replace openviking subchart with S3 config on cc-broker"
```

---

## Task 14: Full verification

**Files:** none modified.

- [ ] **Step 1: Run the full test suite**

Run: `cd /root/agentserver && go test ./...`
Expected: all packages pass (the pre-existing skipped test stays skipped).

- [ ] **Step 2: Run `go vet`**

Run: `cd /root/agentserver && go vet ./...`
Expected: no output.

- [ ] **Step 3: Run `go mod tidy` and verify no diff**

Run: `cd /root/agentserver && go mod tidy && git diff --exit-code go.mod go.sum`
Expected: exit code 0 (no unrecorded deps).

- [ ] **Step 4: Confirm no OpenViking references remain in code or chart**

Run: `cd /root/agentserver && grep -rin "openviking\|VikingClient\|CCBROKER_OPENVIKING" --include="*.go" --include="*.yaml" --include="*.yml" .`
Expected: no output. (Spec files under `docs/superpowers/specs/` may legitimately reference history — this grep only checks code/config.)

- [ ] **Step 5: Manual smoke test (local dev)**

Run:
```bash
cd /root/agentserver && docker compose down -v && docker compose up -d
```

Wait ~10 seconds for services to be healthy. Send one IM message that triggers a turn (use whatever local mechanism the project provides — `curl` against `/api/turns` works).

Verify:
- Turn completes without error in `docker compose logs cc-broker`.
- `docker compose exec minio mc ls local/ccbroker/workspaces/` lists at least one workspace directory containing `claude-home.tar.gz`.
- Send a second message in the same session — Claude resumes (logs show `--resume` not `--session-id`).

- [ ] **Step 6: Stop minio mid-flight, confirm Setup fails cleanly**

Run: `cd /root/agentserver && docker compose stop minio`

Send another turn request. Expect HTTP 500 from `/api/turns` (handler returns "workspace setup failed"). No panic in cc-broker logs.

Restart: `cd /root/agentserver && docker compose start minio`. Wait ~5s. Re-send — expect 200 / SSE stream as normal.

- [ ] **Step 7: No commit (verification only). If anything failed, file follow-up tasks instead of forcing a green report.**

---

## Self-Review

### Spec coverage
- §Architecture per-turn flow → Tasks 7, 11
- §S3 layout (`workspaces/<id>/claude-home.tar.gz`) → Task 7 (`claudeHomeKey`)
- §Components: `s3store.go` API → Tasks 2, 5
- §Components: `workspace.go` signature change → Task 7
- §Removed code → Tasks 8 (workspace), 9 (Viking field), 13 (subchart)
- §Components: handler_turns wiring → Task 11
- §Config (`CCBROKER_S3_*`) → Task 10
- §Data flow Download (404 = empty, traversal protection) → Tasks 2, 3, 4
- §Data flow Upload (streaming via io.Pipe, multipart via size=-1) → Task 5
- §Concurrency (single shared `*S3Store`) → Task 11 (Server holds it)
- §Error matrix (Setup cleans up TempDir on error) → Task 7 (Setup body)
- §Testing unit + workspace + manual checklist → Tasks 2-6, 7, 14
- §Deployment Helm → Task 13
- §Deployment docker-compose → Task 12
- §Dependencies (`minio-go/v7`) → Task 1
- §Rollback → no task needed (it's a `git revert` + redeploy story, not a code change)

All spec sections covered.

### Type / signature consistency
- `S3Config` shape used identically in `s3store.go` (Task 2), `server.go` `NewServer` (Task 11), `Config` env mapping (Task 10).
- `*S3Store` carried as the third arg of both `Setup` and `Teardown` (Task 7, Task 11 seams, Task 11 handler call sites).
- `claudeHomeKey(workspaceID)` defined once in workspace.go (Task 7), used in workspace_test.go (Task 7) — no separate string spelling.

### Placeholder scan
No "TBD", "TODO", "implement later", or "appropriate error handling" instructions. Every code-changing step contains the actual code.
