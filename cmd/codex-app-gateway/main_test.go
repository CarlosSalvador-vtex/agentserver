package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseEnvMcpArgs_HappyPath(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--exe-id", "exe_alpha",
		"--bridge-url", "ws://exec-gateway:6060/bridge/exe_alpha",
		"--token-env", "CXG_BRIDGE_TOKEN_EXE_ALPHA",
		"--exe-desc", "Daisy's MacBook",
		"--turn-id", "trn_xxx",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.ExeID != "exe_alpha" {
		t.Errorf("ExeID = %q", args.ExeID)
	}
	if args.BridgeURL != "ws://exec-gateway:6060/bridge/exe_alpha" {
		t.Errorf("BridgeURL = %q", args.BridgeURL)
	}
	if args.TokenEnv != "CXG_BRIDGE_TOKEN_EXE_ALPHA" {
		t.Errorf("TokenEnv = %q", args.TokenEnv)
	}
	if args.ExeDesc != "Daisy's MacBook" {
		t.Errorf("ExeDesc = %q", args.ExeDesc)
	}
	if args.TurnID != "trn_xxx" {
		t.Errorf("TurnID = %q", args.TurnID)
	}
}

func TestParseEnvMcpArgs_RequiresExeID(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--bridge-url", "ws://x/bridge/y",
		"--token-env", "T",
	})
	if err == nil || !strings.Contains(err.Error(), "--exe-id") {
		t.Fatalf("want --exe-id required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_RequiresBridgeURL(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--exe-id", "x", "--token-env", "T",
	})
	if err == nil || !strings.Contains(err.Error(), "--bridge-url") {
		t.Fatalf("want --bridge-url required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_RequiresTokenEnv(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--exe-id", "x", "--bridge-url", "ws://x/bridge/y",
	})
	if err == nil || !strings.Contains(err.Error(), "--token-env") {
		t.Fatalf("want --token-env required error, got %v", err)
	}
}

func TestParseEnvMcpArgs_DescDefaultsToExeID(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--exe-id", "exe_x",
		"--bridge-url", "ws://x/bridge/y",
		"--token-env", "T",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.ExeDesc != "exe_x" {
		t.Errorf("ExeDesc default = %q, want exe_x", args.ExeDesc)
	}
}

func TestParseEnvMcpArgs_HelpFlag_ReturnsErrHelp(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("want flag.ErrHelp, got %v", err)
	}
}

func TestParseEnvMcpArgs_RejectsTrailingPositional(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--exe-id", "x",
		"--bridge-url", "ws://x/bridge/y",
		"--token-env", "T",
		"unexpected",
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional") {
		t.Fatalf("want unexpected-positional error, got %v", err)
	}
}

func TestParseEnvMcpArgs_UnknownFlag_NoStderrLeak(t *testing.T) {
	// Smoke test: parse should error without panicking and the error
	// should be the FlagSet's own message (i.e., we didn't suppress it
	// to the point of losing the diagnostic).
	_, err := parseEnvMcpArgs([]string{"--bogus", "x"})
	if err == nil {
		t.Fatal("want error on unknown flag")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the offending flag: %v", err)
	}
}
