package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sha256OfFile returns the hex-encoded SHA-256 of the named file's contents.
func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sha256OfNormalizedFile reads the file, normalizes it, and returns its hex SHA-256.
func sha256OfNormalizedFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	norm := normalize(b)
	sum := sha256.Sum256(norm)
	return hex.EncodeToString(sum[:])
}

// writePin marshals pin to JSON in a temp dir and returns the path.
func writePin(t *testing.T, pin Pin) string {
	t.Helper()
	b, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "codex-pin.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fixtureDir is the testdata root relative to this file.
const fixtureDir = "testdata"

// upstreamOK is the "everything matches" upstream fixture tree.
const upstreamOK = fixtureDir + "/upstream-ok"

// upstreamDrift has a modified item.rs.
const upstreamDrift = fixtureDir + "/upstream-drift"

// makeRepoRoot writes a fake repo with internal/relaypb/relay.proto set to
// content and returns the root dir.
func makeRepoRoot(t *testing.T, protoContent string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "relaypb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "relay.proto")
	if err := os.WriteFile(path, []byte(protoContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestVerify_OK: fixture tree matches pin → no mismatches.
func TestVerify_OK(t *testing.T) {
	protoUpstreamPath := upstreamOK + "/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"

	// The normalized sha of the upstream proto == sha of our proto after normalize.
	// We verify both are the same by computing them from the fixture.
	normalizedSha := sha256OfNormalizedFile(t, protoUpstreamPath)

	// Build the pin with shas derived from the OK fixtures.
	pin := Pin{
		UpstreamRepo: "openai/codex",
		Tag:          "rust-v0.131.0-alpha.22",
		Sha:          "d2c823dc87c863bedeb3752e997facdf7c1b5aad",
		TrackedFiles: map[string]string{
			"codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto":   sha256OfFile(t, upstreamOK+"/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"),
			"codex-rs/app-server-protocol/src/protocol/v1.rs":                    sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v1.rs"),
			"codex-rs/app-server-protocol/src/protocol/v2/item.rs":               sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/item.rs"),
			"codex-rs/app-server-protocol/src/protocol/v2/mcp.rs":                sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/mcp.rs"),
		},
		NormalizedEquivalentFiles: map[string]NormalizedEntry{
			"internal/relaypb/relay.proto": {
				UpstreamPath:     "codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto",
				NormalizedSha256: normalizedSha,
				Comment:          "test",
			},
		},
	}
	pinPath := writePin(t, pin)

	// Our local proto is our real relay.proto (which has the go_package line).
	// After normalization it must equal the upstream fixture.
	// Read the real proto and write it into a fake repo root.
	realProto, err := os.ReadFile("../../internal/relaypb/relay.proto")
	if err != nil {
		t.Fatalf("read real relay.proto: %v", err)
	}
	repoRoot := makeRepoRoot(t, string(realProto))

	report, err := Verify(pinPath, repoRoot, upstreamOK)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if len(report.Mismatches) != 0 {
		t.Errorf("expected 0 mismatches, got %d:", len(report.Mismatches))
		for _, m := range report.Mismatches {
			t.Errorf("  file=%s reason=%s want=%s got=%s", m.File, m.Reason, m.Want, m.Got)
		}
	}
}

// TestVerify_TrackedSha_DriftDetected: upstream item.rs changed → 1 mismatch.
func TestVerify_TrackedSha_DriftDetected(t *testing.T) {
	protoUpstreamPath := upstreamOK + "/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"
	normalizedSha := sha256OfNormalizedFile(t, protoUpstreamPath)

	// Use OK shas for tracked_files — but the drift fixture has a different item.rs.
	// So the sha in the pin still points to the OK content, but upstream-drift has changed content.
	pin := Pin{
		UpstreamRepo: "openai/codex",
		Tag:          "rust-v0.131.0-alpha.22",
		Sha:          "d2c823dc87c863bedeb3752e997facdf7c1b5aad",
		TrackedFiles: map[string]string{
			"codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto":   sha256OfFile(t, upstreamOK+"/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"),
			"codex-rs/app-server-protocol/src/protocol/v1.rs":                    sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v1.rs"),
			// Pin has the OK sha, but the drift fixture has a different file.
			"codex-rs/app-server-protocol/src/protocol/v2/item.rs": sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/item.rs"),
			"codex-rs/app-server-protocol/src/protocol/v2/mcp.rs":  sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/mcp.rs"),
		},
		NormalizedEquivalentFiles: map[string]NormalizedEntry{
			"internal/relaypb/relay.proto": {
				UpstreamPath:     "codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto",
				NormalizedSha256: normalizedSha,
				Comment:          "test",
			},
		},
	}
	pinPath := writePin(t, pin)

	realProto, err := os.ReadFile("../../internal/relaypb/relay.proto")
	if err != nil {
		t.Fatalf("read real relay.proto: %v", err)
	}
	repoRoot := makeRepoRoot(t, string(realProto))

	// Use the DRIFT upstream tree — item.rs content has changed.
	report, err := Verify(pinPath, repoRoot, upstreamDrift)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	const wantFile = "codex-rs/app-server-protocol/src/protocol/v2/item.rs"
	const wantReason = "tracked-sha"

	if len(report.Mismatches) != 1 {
		t.Fatalf("expected exactly 1 mismatch, got %d: %+v", len(report.Mismatches), report.Mismatches)
	}
	m := report.Mismatches[0]
	if m.File != wantFile {
		t.Errorf("mismatch.File = %q, want %q", m.File, wantFile)
	}
	if m.Reason != wantReason {
		t.Errorf("mismatch.Reason = %q, want %q", m.Reason, wantReason)
	}
}

// TestVerify_NormalizedEquivalent_Mismatch: our proto has a different schema → mismatch.
func TestVerify_NormalizedEquivalent_Mismatch(t *testing.T) {
	protoUpstreamPath := upstreamOK + "/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"
	normalizedSha := sha256OfNormalizedFile(t, protoUpstreamPath)

	pin := Pin{
		UpstreamRepo: "openai/codex",
		Tag:          "rust-v0.131.0-alpha.22",
		Sha:          "d2c823dc87c863bedeb3752e997facdf7c1b5aad",
		TrackedFiles: map[string]string{
			"codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto":   sha256OfFile(t, upstreamOK+"/codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto"),
			"codex-rs/app-server-protocol/src/protocol/v1.rs":                    sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v1.rs"),
			"codex-rs/app-server-protocol/src/protocol/v2/item.rs":               sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/item.rs"),
			"codex-rs/app-server-protocol/src/protocol/v2/mcp.rs":                sha256OfFile(t, upstreamOK+"/codex-rs/app-server-protocol/src/protocol/v2/mcp.rs"),
		},
		NormalizedEquivalentFiles: map[string]NormalizedEntry{
			"internal/relaypb/relay.proto": {
				UpstreamPath:     "codex-rs/exec-server/src/proto/codex.exec_server.relay.v1.proto",
				NormalizedSha256: normalizedSha,
				Comment:          "test",
			},
		},
	}
	pinPath := writePin(t, pin)

	// Write a proto that differs in schema (not just go_package), so normalization
	// will still produce a different sha.
	brokenProto := `syntax = "proto3";

package codex.exec_server.relay.v1;

option go_package = "github.com/agentserver/agentserver/internal/relaypb;relaypb";

// This file has a completely different schema — extra field added.
message RelayMessageFrame {
  uint32 version = 1;
  string stream_id = 2;
  uint32 ack = 3;
  uint32 ack_bits = 4;
  string extra_field = 99;
}
`
	repoRoot := makeRepoRoot(t, brokenProto)

	report, err := Verify(pinPath, repoRoot, upstreamOK)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	const wantFile = "internal/relaypb/relay.proto"
	const wantReason = "normalized-equivalent"

	var found *Mismatch
	for i := range report.Mismatches {
		if report.Mismatches[i].File == wantFile && report.Mismatches[i].Reason == wantReason {
			found = &report.Mismatches[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a mismatch with file=%q reason=%q, got: %+v", wantFile, wantReason, report.Mismatches)
	}
}
