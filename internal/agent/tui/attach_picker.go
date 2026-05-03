// internal/agent/tui/attach_picker.go
package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const attachPerFileMaxBytes = 8 << 20

// AttachFromPath reads a file off disk and returns it as an InboundAttachment
// ready to drop into the next prompt. Image files (recognised by extension)
// get kind="image"; everything else is "file". The 8 MiB cap is per-file
// (the cumulative cap is enforced server-side).
func AttachFromPath(path string) (InboundAttachment, error) {
	f, err := os.Open(path)
	if err != nil {
		return InboundAttachment{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return InboundAttachment{}, err
	}
	if info.Size() > attachPerFileMaxBytes {
		return InboundAttachment{}, fmt.Errorf("attachment exceeds 8 MiB cap (%d bytes)", info.Size())
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return InboundAttachment{}, err
	}
	kind := "file"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		kind = "image"
	}
	return InboundAttachment{
		Kind:       kind,
		Filename:   filepath.Base(path),
		Size:       int(info.Size()),
		ContentB64: base64.StdEncoding.EncodeToString(raw),
	}, nil
}
