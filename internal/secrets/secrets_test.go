package secrets_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/secrets"
)

func TestMint_HappyPath(t *testing.T) {
	tok, err := secrets.Mint(secrets.APIKeySpec)
	if err != nil {
		t.Fatalf("Mint: unexpected error: %v", err)
	}

	// Full = "wak_<8>_<40>" — 3 segments total, split by _ gives 4 parts
	// but since prefix already has _, we check structural shape:
	// Full starts with "wak_", then 8 chars, then "_", then 40 chars.
	if !strings.HasPrefix(tok.Full, "wak_") {
		t.Errorf("Full %q does not start with wak_", tok.Full)
	}
	// Split by _ should yield exactly 3 parts: "wak", id8chars, secret40chars
	parts := strings.SplitN(tok.Full[4:], "_", 2) // after "wak_"
	if len(parts) != 2 {
		t.Fatalf("Full %q does not have 3 underscore-separated parts", tok.Full)
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

	// Secret = 40 chars
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
	// Build a token with a non-base62 char in id: replace char 5 with '!'
	badIDToken := "wak_abc!efgh_" + validTok.Secret
	// Build a token with non-base62 char in secret
	badSecretToken := validTok.ID + "_" + strings.Repeat("a", 39) + "!"

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"wrong prefix", "tok_" + validTok.Full[4:]},
		{"missing separator (no second _)", "wak_abcdefgh"},
		{"wrong id length (7 chars)", "wak_abcdefg_" + strings.Repeat("x", 40)},
		{"wrong secret length (39 chars)", validTok.ID + "_" + strings.Repeat("a", 39)},
		{"non-base62 in id", badIDToken},
		{"non-base62 in secret", badSecretToken},
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
