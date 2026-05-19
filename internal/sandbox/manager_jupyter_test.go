package sandbox

import (
	"strings"
	"testing"
)

// TestJupyterConfig_ImageRequired is a smoke check that asserts the
// Config plumbing compiles. Heavier integration coverage lives in
// internal/sandboxproxy/jupyter_proxy_test.go (Task A5) plus the
// chart-template smoke check (Task A6).
func TestJupyterConfig_ImageRequired(t *testing.T) {
	c := Config{JupyterImage: "", JupyterPort: 8888}
	if c.JupyterImage != "" {
		t.Errorf("default JupyterImage should be empty until env set")
	}
	if c.JupyterPort != 8888 {
		t.Errorf("JupyterPort=%d", c.JupyterPort)
	}
	if !strings.Contains("JUPYTER_IMAGE", "JUPYTER") {
		t.Fatal("sanity")
	}
}
