package codexappgateway

import (
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway"
)

func TestMintCapToken_VerifiesAtExecGateway(t *testing.T) {
	secret := []byte("shared-cap-secret")
	tok, err := MintCapToken(secret, "trn_42", "ws_a", "exe_alpha", time.Minute)
	if err != nil {
		t.Fatalf("MintCapToken: %v", err)
	}
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TurnID != "trn_42" || got.WorkspaceID != "ws_a" {
		t.Errorf("payload = %+v", got)
	}
	if len(got.ExeIDs) != 1 || got.ExeIDs[0] != "exe_alpha" {
		t.Errorf("exe_ids = %v (want one element [\"exe_alpha\"])", got.ExeIDs)
	}
	if !got.AllowsExeID("exe_alpha") {
		t.Error("AllowsExeID(exe_alpha) = false")
	}
	if got.AllowsExeID("exe_other") {
		t.Error("AllowsExeID(exe_other) = true (cross-executor leak)")
	}
}

func TestMintCapToken_ExpRespectsTTL(t *testing.T) {
	tok, err := MintCapToken([]byte("k"), "trn_1", "ws_a", "exe_a", -time.Second)
	if err != nil {
		t.Fatalf("MintCapToken: %v", err)
	}
	_, err = codexexecgateway.VerifyCapabilityToken(tok, []byte("k"))
	if err != codexexecgateway.ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestMintCapToken_RejectsEmptySecret(t *testing.T) {
	if _, err := MintCapToken(nil, "trn_1", "ws_a", "exe_a", time.Minute); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestMintCapToken_RejectsEmptyFields(t *testing.T) {
	cases := []struct {
		turn, ws, exe string
	}{
		{"", "ws", "exe"},
		{"trn", "", "exe"},
		{"trn", "ws", ""},
	}
	for _, tc := range cases {
		if _, err := MintCapToken([]byte("k"), tc.turn, tc.ws, tc.exe, time.Minute); err == nil {
			t.Errorf("MintCapToken(%q,%q,%q): want error, got nil", tc.turn, tc.ws, tc.exe)
		}
	}
}
