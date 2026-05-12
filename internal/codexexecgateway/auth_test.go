package codexexecgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func mintToken(t *testing.T, secret []byte, payload CapPayload) string {
	t.Helper()
	header := []byte(`{"alg":"HS256","typ":"CXG"}`)
	pj, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(pj)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + enc.EncodeToString(sig)
}

func TestVerifyCapabilityToken_HappyPath(t *testing.T) {
	secret := []byte("k")
	now := time.Now().Unix()
	// multi-id token — forward-compat case
	tok := mintToken(t, secret, CapPayload{
		TurnID: "trn_1", WorkspaceID: "ws_1",
		ExeIDs: []string{"exe_a", "exe_b"}, IAT: now, EXP: now + 60,
	})
	got, err := VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TurnID != "trn_1" || len(got.ExeIDs) != 2 || got.ExeIDs[1] != "exe_b" {
		t.Fatalf("payload: %+v", got)
	}
}

// TestVerifyCapabilityToken_SingleExeID exercises the production case: each
// cap token minted by codex-app-gateway contains exactly one exe_id.
func TestVerifyCapabilityToken_SingleExeID(t *testing.T) {
	secret := []byte("production-secret")
	now := time.Now().Unix()
	tok := mintToken(t, secret, CapPayload{
		TurnID:      "trn_prod",
		WorkspaceID: "ws_prod",
		ExeIDs:      []string{"exe_only"},
		IAT:         now,
		EXP:         now + 300,
	})
	got, err := VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify single-id: %v", err)
	}
	if len(got.ExeIDs) != 1 || got.ExeIDs[0] != "exe_only" {
		t.Fatalf("single-id payload: %+v", got)
	}
	if !got.AllowsExeID("exe_only") {
		t.Fatal("AllowsExeID should return true for the sole exe_id")
	}
	if got.AllowsExeID("exe_other") {
		t.Fatal("AllowsExeID should return false for unknown exe_id")
	}
}

func TestVerifyCapabilityToken_BadSig(t *testing.T) {
	tok := mintToken(t, []byte("k1"), CapPayload{TurnID: "t", EXP: time.Now().Unix() + 60})
	if _, err := VerifyCapabilityToken(tok, []byte("k2")); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyCapabilityToken_Expired(t *testing.T) {
	tok := mintToken(t, []byte("k"), CapPayload{TurnID: "t", EXP: time.Now().Unix() - 1})
	if _, err := VerifyCapabilityToken(tok, []byte("k")); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerifyCapabilityToken_Malformed(t *testing.T) {
	cases := []string{"", "a.b", "a.b.c.d", "!.!.!"}
	for _, c := range cases {
		if _, err := VerifyCapabilityToken(c, []byte("k")); err != ErrMalformed {
			t.Fatalf("%q: want ErrMalformed, got %v", c, err)
		}
	}
}

func TestCapPayload_AllowsExeID(t *testing.T) {
	p := CapPayload{ExeIDs: []string{"exe_a", "exe_b"}}
	if !p.AllowsExeID("exe_b") {
		t.Fatal("want true")
	}
	if p.AllowsExeID("exe_z") {
		t.Fatal("want false")
	}
}
