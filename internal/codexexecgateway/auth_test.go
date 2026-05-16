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

func TestVerifyCapabilityToken_Workspace(t *testing.T) {
	secret := []byte("production-secret")
	now := time.Now().Unix()
	tok := mintToken(t, secret, CapPayload{
		TurnID:      "trn_prod",
		WorkspaceID: "ws_prod",
		IAT:         now,
		EXP:         now + 300,
	})
	got, err := VerifyCapabilityToken(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.TurnID != "trn_prod" || got.WorkspaceID != "ws_prod" {
		t.Fatalf("payload: %+v", got)
	}
}

func TestVerifyCapabilityToken_BadSig(t *testing.T) {
	tok := mintToken(t, []byte("k1"), CapPayload{TurnID: "t", WorkspaceID: "w", EXP: time.Now().Unix() + 60})
	if _, err := VerifyCapabilityToken(tok, []byte("k2")); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyCapabilityToken_Expired(t *testing.T) {
	tok := mintToken(t, []byte("k"), CapPayload{TurnID: "t", WorkspaceID: "w", EXP: time.Now().Unix() - 1})
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
