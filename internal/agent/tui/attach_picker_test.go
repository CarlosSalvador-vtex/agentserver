// internal/agent/tui/attach_picker_test.go
package tui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachFromPath_ReadsAndBase64s(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := AttachFromPath(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Filename != "test.txt" {
		t.Errorf("filename=%q", a.Filename)
	}
	if a.Size != 5 {
		t.Errorf("size=%d", a.Size)
	}
	decoded, _ := base64.StdEncoding.DecodeString(a.ContentB64)
	if string(decoded) != "hello" {
		t.Errorf("decoded=%q", string(decoded))
	}
	if a.Kind != "file" {
		t.Errorf("kind=%q", a.Kind)
	}
}

func TestAttachFromPath_DetectsImageByExt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, []byte("\x89PNGfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := AttachFromPath(p)
	if a.Kind != "image" {
		t.Errorf("kind=%q want image", a.Kind)
	}
}

func TestAttachFromPath_RejectsTooBig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(p, make([]byte, 9<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AttachFromPath(p)
	if err == nil {
		t.Error("expected size cap error")
	}
}
