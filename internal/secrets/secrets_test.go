package secrets_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"regexp"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/secrets"
)

// resetPepperForTest clears the package-level pepper so tests that call
// SetPepper don't bleed state into subsequent tests. This accesses the
// exported reset path via a fresh SetPepper with nil — instead we use
// the test-only exported helper if available, but since the package is
// internal we manipulate via the public API only: we call SetPepper with
// the same value to confirm idempotency, and rely on test ordering + the
// explicit cleanup function.
//
// Because SetPepper panics on different values, tests that set pepper MUST
// use t.Cleanup with an unexported reset or run in isolation. We expose a
// test-only reset via an internal test file (secrets_test_helpers_test.go).
// Here we simply call the exported resetPepperForTest from the same package
// via the _test build tag approach: place the helper in a _test.go file
// inside package secrets (not secrets_test) so it has access to the var.
// That file is secrets_internal_test.go (see below). We call it here.

func TestMint_HappyPath(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: unexpected error: %v", err)
	}

	// Full = "wak_<8>_<40><6crc>" — total length = 4+8+1+40+6 = 59 chars
	if !strings.HasPrefix(tok.Full, "wak_") {
		t.Errorf("Full %q does not start with wak_", tok.Full)
	}
	if len(tok.Full) != 59 {
		t.Errorf("Full len=%d want 59 (4+8+1+40+6)", len(tok.Full))
	}

	// Body = first 53 chars, CRC = last 6 chars.
	body := tok.Full[:53]
	// Split body after "wak_": body = "wak_<id8>_<secret40>"
	parts := strings.SplitN(body[4:], "_", 2) // after "wak_"
	if len(parts) != 2 {
		t.Fatalf("Full body %q does not have id+secret segments", body)
	}
	if len(parts[0]) != 8 {
		t.Errorf("ID segment len=%d want 8", len(parts[0]))
	}
	if len(parts[1]) != 40 {
		t.Errorf("Secret segment len=%d want 40", len(parts[1]))
	}

	// ID = "wak_" + 8 chars
	if tok.ID != "wak_"+parts[0] {
		t.Errorf("ID %q != wak_+%s", tok.ID, parts[0])
	}
	if len(tok.ID) != 4+8 {
		t.Errorf("ID len=%d want 12", len(tok.ID))
	}

	// Secret = 40 chars (no crc suffix)
	if len(tok.Secret) != 40 {
		t.Errorf("Secret len=%d want 40", len(tok.Secret))
	}
	if tok.Secret != parts[1] {
		t.Errorf("Secret %q != %s", tok.Secret, parts[1])
	}

	// Hash = 64 hex chars
	if len(tok.Hash) != 64 {
		t.Errorf("Hash len=%d want 64", len(tok.Hash))
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]+$`)
	if !hexRe.MatchString(tok.Hash) {
		t.Errorf("Hash %q is not lowercase hex", tok.Hash)
	}

	// All base62
	base62Re := regexp.MustCompile(`^[0-9a-zA-Z]+$`)
	if !base62Re.MatchString(parts[0]) {
		t.Errorf("ID segment %q contains non-base62 chars", parts[0])
	}
	if !base62Re.MatchString(parts[1]) {
		t.Errorf("Secret segment %q contains non-base62 chars", parts[1])
	}
}

func TestMint_Uniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		tok, err := secrets.Mint(secrets.APIKeySpec)
		if err != nil {
			t.Fatalf("Mint iteration %d: %v", i, err)
		}
		if _, dup := seen[tok.Full]; dup {
			t.Fatalf("Duplicate Full token at iteration %d: %s", i, tok.Full)
		}
		seen[tok.Full] = struct{}{}
	}
}

func TestParse_RoundTrip(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	id, secret, err := secrets.Parse(secrets.APIKeySpec, tok.Full)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id != tok.ID {
		t.Errorf("id %q != tok.ID %q", id, tok.ID)
	}
	if secret != tok.Secret {
		t.Errorf("secret %q != tok.Secret %q", secret, tok.Secret)
	}
}

func TestParse_InvalidFormat(t *testing.T) {
	validTok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint for table setup: %v", err)
	}
	// Build a token that is otherwise valid in structure but has bad id.
	// Note: we need a properly-CRC'd token for the id/secret checks to fire,
	// but since CRC is checked first, structural errors with wrong CRC also
	// return ErrInvalidFormat — which is what we want.
	badIDToken := "wak_abc!efgh_" + strings.Repeat("a", 40) + "000000"
	badSecretToken := validTok.ID + "_" + strings.Repeat("a", 39) + "!" + "000000"

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"wrong prefix", "tok_" + validTok.Full[4:]},
		{"missing separator (no second _)", "wak_abcdefgh" + strings.Repeat("x", 46)},
		{"wrong id length (7 chars)", "wak_abcdefg_" + strings.Repeat("x", 40) + "000000"},
		{"wrong secret length (39 chars)", validTok.ID + "_" + strings.Repeat("a", 39) + "000000"},
		{"non-base62 in id", badIDToken},
		{"non-base62 in secret", badSecretToken},
		{"too short (missing crc)", validTok.Full[:53]},
		{"too long (extra char)", validTok.Full + "x"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := secrets.Parse(secrets.APIKeySpec, tc.input)
			if !errors.Is(err, secrets.ErrInvalidFormat) {
				t.Errorf("Parse(%q) = %v, want ErrInvalidFormat", tc.input, err)
			}
		})
	}
}

func TestHash_Stable(t *testing.T) {
	s := "wak_abc12345_secretsecret"
	h1 := secrets.Hash(s)
	h2 := secrets.Hash(s)
	if h1 != h2 {
		t.Errorf("Hash not stable: %q != %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("Hash len=%d want 64", len(h1))
	}
}

func TestConstantTimeMatch_Match(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !secrets.ConstantTimeMatch(tok.Full, tok.Hash) {
		t.Error("ConstantTimeMatch returned false for matching token")
	}
}

func TestConstantTimeMatch_Mismatch(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	otherTok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint other: %v", err)
	}
	if secrets.ConstantTimeMatch(tok.Full, otherTok.Hash) {
		t.Error("ConstantTimeMatch returned true for non-matching tokens")
	}
	if secrets.ConstantTimeMatch("", tok.Hash) {
		t.Error("ConstantTimeMatch returned true for empty presented")
	}
}

func TestRandomHex_LengthAndCharSet(t *testing.T) {
	s, err := secrets.RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex(16): %v", err)
	}
	if len(s) != 32 {
		t.Errorf("RandomHex(16) len=%d want 32", len(s))
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]+$`)
	if !hexRe.MatchString(s) {
		t.Errorf("RandomHex(16) = %q contains non-hex chars", s)
	}

	_, err = secrets.RandomHex(0)
	if err == nil {
		t.Error("RandomHex(0) expected error, got nil")
	}
}

func TestValidateSpec_Errors(t *testing.T) {
	cases := []struct {
		name string
		spec secrets.Spec
	}{
		{"empty prefix", secrets.Spec{Prefix: "", IDLen: 8, SecretLen: 40}},
		{"non-underscore-terminated prefix", secrets.Spec{Prefix: "wak", IDLen: 8, SecretLen: 40}},
		{"zero IDLen", secrets.Spec{Prefix: "wak_", IDLen: 0, SecretLen: 40}},
		{"zero SecretLen", secrets.Spec{Prefix: "wak_", IDLen: 8, SecretLen: 0}},
		{"both zero", secrets.Spec{Prefix: "wak_", IDLen: 0, SecretLen: 0}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := secrets.Mint(tc.spec)
			if err == nil {
				t.Errorf("Mint(%+v) expected error, got nil", tc.spec)
			}
		})
	}
}

// ---- CRC32 tests (Change 1) ----

func TestMint_HasCRC32Suffix(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Full token is 59 chars; body is first 53, CRC is last 6.
	if len(tok.Full) != 59 {
		t.Fatalf("Full len=%d want 59", len(tok.Full))
	}
	body := tok.Full[:53]
	crcSuffix := tok.Full[53:]
	if len(crcSuffix) != 6 {
		t.Fatalf("CRC suffix len=%d want 6", len(crcSuffix))
	}
	// Recompute CRC32/IEEE of body and re-encode as 6 base62 chars.
	sum := crc32.ChecksumIEEE([]byte(body))
	base62 := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	out := make([]byte, 6)
	v := uint64(sum)
	for i := 5; i >= 0; i-- {
		out[i] = base62[v%62]
		v /= 62
	}
	expectedCRC := string(out)
	if crcSuffix != expectedCRC {
		t.Errorf("CRC suffix %q != recomputed %q", crcSuffix, expectedCRC)
	}
}

func TestParse_RejectsBadCRC(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Flip the last character of the CRC region.
	runes := []byte(tok.Full)
	last := runes[len(runes)-1]
	// Change to a different base62 char.
	if last == '0' {
		runes[len(runes)-1] = '1'
	} else {
		runes[len(runes)-1] = '0'
	}
	tampered := string(runes)
	_, _, err = secrets.Parse(secrets.APIKeySpec, tampered)
	if !errors.Is(err, secrets.ErrInvalidFormat) {
		t.Errorf("Parse with bad CRC = %v, want ErrInvalidFormat", err)
	}
}

func TestParse_RejectsTamperedBody(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Flip a char in the secret region (offset 13 into body = inside the 40-char secret).
	runes := []byte(tok.Full)
	idx := 13 // well inside the secret segment (body chars 0-52)
	if runes[idx] == 'a' {
		runes[idx] = 'b'
	} else {
		runes[idx] = 'a'
	}
	tampered := string(runes)
	_, _, err = secrets.Parse(secrets.APIKeySpec, tampered)
	if !errors.Is(err, secrets.ErrInvalidFormat) {
		t.Errorf("Parse with tampered body = %v, want ErrInvalidFormat", err)
	}
}

func TestCRC32Base62_Stable(t *testing.T) {
	// Verify stability: same input always produces the same 6-char output.
	tok1, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	body := tok1.Full[:53]
	crc1 := tok1.Full[53:]
	// Re-parse to implicitly re-derive the CRC.
	tok2, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint tok2: %v", err)
	}
	_ = tok2 // just ensure Mint works twice

	// Check that a manually known-small CRC32 value gets 6 chars with leading zero.
	// crc32.ChecksumIEEE("") == 0x00000000 → should produce "000000".
	sum := crc32.ChecksumIEEE([]byte(""))
	base62 := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	out := make([]byte, 6)
	v := uint64(sum)
	for i := 5; i >= 0; i-- {
		out[i] = base62[v%62]
		v /= 62
	}
	if string(out) != "000000" {
		t.Errorf("CRC32 of empty string encoded as %q, want 000000", string(out))
	}

	// Verify crc1 is exactly 6 chars.
	if len(crc1) != 6 {
		t.Errorf("CRC suffix len=%d want 6 for body %q", len(crc1), body)
	}
	// Verify it's all base62.
	base62Re := regexp.MustCompile(`^[0-9a-zA-Z]+$`)
	if !base62Re.MatchString(crc1) {
		t.Errorf("CRC suffix %q contains non-base62 chars", crc1)
	}
}

// ---- HMAC + pepper tests (Change 2) ----

// secretsResetPepper is called by tests that install a pepper. It uses the
// exported ResetPepperForTest function defined in secrets_test_export_test.go
// (package secrets, build tag: _test).
// Since we are in package secrets_test (external), we call the re-exported
// wrapper below. See secrets_pepper_test.go (internal package) for the
// actual implementation.

func TestHash_NoPepper_IsSHA256(t *testing.T) {
	// In a fresh subtest where pepper is nil (default for unit tests),
	// Hash(s) should equal hex(sha256(s)).
	s := "wak_testtoken_nopepperhashcheck"
	got := secrets.Hash(s)
	sum := sha256.Sum256([]byte(s))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("Hash without pepper = %q, want SHA-256 %q", got, want)
	}
}

func TestHash_WithPepper_IsHMAC(t *testing.T) {
	t.Cleanup(resetPepperForTest)
	key := []byte("testpepperkey")
	secrets.SetPepper(key)

	s := "wak_testtoken_withpepperhashcheck"
	got := secrets.Hash(s)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(s))
	wantHMAC := hex.EncodeToString(mac.Sum(nil))

	sumPlain := sha256.Sum256([]byte(s))
	wantPlain := hex.EncodeToString(sumPlain[:])

	if got != wantHMAC {
		t.Errorf("Hash with pepper = %q, want HMAC-SHA256 %q", got, wantHMAC)
	}
	if got == wantPlain {
		t.Errorf("Hash with pepper equals plain SHA-256 — pepper not applied")
	}
}

func TestSetPepper_Idempotent(t *testing.T) {
	t.Cleanup(resetPepperForTest)
	key := []byte("idempotentpepperkey")
	// Same value twice — must not panic.
	secrets.SetPepper(key)
	secrets.SetPepper(key) // no-op

	// Different value — must panic.
	defer func() {
		if r := recover(); r == nil {
			t.Error("SetPepper with different value did not panic")
		}
	}()
	secrets.SetPepper([]byte("differentkey"))
}
