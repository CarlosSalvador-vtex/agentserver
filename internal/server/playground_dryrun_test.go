package server

import "testing"

func TestResolveDryRunModel(t *testing.T) {
	t.Setenv("PLAYGROUND_DRYRUN_MODEL", "env-model")
	if got := resolveDryRunModel("req-model"); got != "req-model" {
		t.Errorf("request model: got %q", got)
	}
	if got := resolveDryRunModel(""); got != "env-model" {
		t.Errorf("env model: got %q", got)
	}
	t.Setenv("PLAYGROUND_DRYRUN_MODEL", "")
	if got := resolveDryRunModel(""); got != playgroundDryRunModelDefault {
		t.Errorf("default model: got %q", got)
	}
}
