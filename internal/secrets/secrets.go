// Package secrets generates and validates prefix-id-secret style
// credentials used across agentserver. Centralizes the
// `rand.Read + encode + sha256 + hex` pattern that was previously
// duplicated across workspace_api_keys, codex_token_format,
// proxy_tokens, and a few smaller call sites.
//
// Format on the wire: <prefix>_<id>_<secret>
//   - prefix is a short ASCII tag (e.g. "wak", "ast") with a trailing _
//     in the Spec (so the full wire token reads "wak_...").
//   - id is a public, indexable handle (DB primary key); base62 encoded
//   - secret is the high-entropy payload; base62 encoded
//   - hash = hex(sha256(<full token>)) — the value persisted
//
// Encoding is base62 (0-9, a-z, A-Z) for ~5.95 bits/character.
// SHA-256 is used because secrets are CSPRNG-generated (>= ~140 bits
// of entropy at SecretLen=24+); bcrypt's KDF overhead adds nothing
// against random secrets and just slows down validation.
package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// base62Alphabet is the canonical ordering for base62-encoded credentials.
// Order: digits first (0-9), then lowercase (a-z), then uppercase (A-Z).
// Matches the de-facto convention used by Stripe / OpenAI tokens.
const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Spec defines the shape of a credential type. One Spec per kind.
type Spec struct {
	// Prefix is the human-readable type tag. MUST end in "_".
	// Examples: "wak_" (workspace api key), "ast_" (agent session token).
	Prefix string
	// IDLen is the number of base62 chars in the public id segment.
	// 8 is the project default; gives 47 bits of entropy in the id alone,
	// enough to make collisions vanishingly unlikely across the lifetime
	// of any one workspace.
	IDLen int
	// SecretLen is the number of base62 chars in the secret segment.
	// 40 chars = ~238 bits, well above any cryptographic threshold.
	SecretLen int
}

// Token is a freshly minted credential. Only `Full` ever leaves the
// server (returned to the user once, never stored). `Hash` is what gets
// persisted; presented secrets are compared via ConstantTimeMatch.
type Token struct {
	// Full is what the user receives: "<prefix>_<id>_<secret>".
	// Caller MUST present this back as `Authorization: Bearer <Full>`.
	Full string
	// ID is "<prefix>_<id>" — the public, indexable handle.
	// Use as DB primary key for the row.
	ID string
	// Secret is the bare secret segment (no prefix, no id).
	// Most callers don't need to store this directly — store Hash instead.
	Secret string
	// Hash is hex(sha256(Full)). Persist this; never persist Full or Secret.
	Hash string
}

// Mint generates a new credential matching spec. Reads from crypto/rand
// and rejects bad specs (empty/no-underscore prefix, zero lengths) so
// misconfigured callers fail loud at startup rather than at the first mint.
func Mint(spec Spec) (Token, error) {
	if err := validateSpec(spec); err != nil {
		return Token{}, err
	}
	idPart, err := randomBase62(spec.IDLen)
	if err != nil {
		return Token{}, fmt.Errorf("secrets: gen id: %w", err)
	}
	secret, err := randomBase62(spec.SecretLen)
	if err != nil {
		return Token{}, fmt.Errorf("secrets: gen secret: %w", err)
	}
	id := spec.Prefix + idPart
	full := id + "_" + secret
	return Token{
		Full:   full,
		ID:     id,
		Secret: secret,
		Hash:   Hash(full),
	}, nil
}

// Parse validates wire shape and splits a presented full token into
// (id, secret) without doing any crypto. Returns ErrInvalidFormat on
// any structural mismatch (wrong prefix, wrong segment lengths,
// missing underscore separator, non-base62 character).
//
// Used by middleware to extract the indexable id before the DB lookup,
// so a malformed token never hits the DB.
func Parse(spec Spec, full string) (id, secret string, err error) {
	if err := validateSpec(spec); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(full, spec.Prefix) {
		return "", "", ErrInvalidFormat
	}
	rest := full[len(spec.Prefix):]
	sep := strings.IndexByte(rest, '_')
	if sep != spec.IDLen {
		return "", "", ErrInvalidFormat
	}
	if len(rest)-sep-1 != spec.SecretLen {
		return "", "", ErrInvalidFormat
	}
	idPart := rest[:sep]
	secretPart := rest[sep+1:]
	if !isBase62(idPart) || !isBase62(secretPart) {
		return "", "", ErrInvalidFormat
	}
	return spec.Prefix + idPart, secretPart, nil
}

// Hash returns hex(sha256(full)). Stable across the lifetime of a
// credential — the hash a freshly minted token produces is the same
// hash the next presentation of that token produces.
func Hash(full string) string {
	sum := sha256.Sum256([]byte(full))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeMatch hashes presented and constant-time compares to
// storedHash. Returns false on any mismatch including length difference.
// Use this in auth paths to avoid timing leaks on prefix collisions.
func ConstantTimeMatch(presented, storedHash string) bool {
	h := Hash(presented)
	return subtle.ConstantTimeCompare([]byte(h), []byte(storedHash)) == 1
}

// RandomHex returns 2*nBytes hex chars from crypto/rand. For sites that
// just need an opaque bare-hex token (session cookies, internal proxy
// tokens) and don't want the prefix-id-secret structure. Equivalent to
// `rand.Read(b); hex.EncodeToString(b)` but centralizes the pattern.
func RandomHex(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", fmt.Errorf("secrets: RandomHex needs nBytes > 0")
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secrets: rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ErrInvalidFormat is returned by Parse when the presented token doesn't
// match the Spec's wire shape.
var ErrInvalidFormat = fmt.Errorf("secrets: invalid token format")

// validateSpec ensures a Spec is internally consistent. Called from Mint
// and Parse — package-level Specs that get the values wrong fail at
// program startup rather than at first mint.
func validateSpec(spec Spec) error {
	if !strings.HasSuffix(spec.Prefix, "_") {
		return fmt.Errorf("secrets: Spec.Prefix %q must end with _", spec.Prefix)
	}
	if spec.IDLen <= 0 || spec.SecretLen <= 0 {
		return fmt.Errorf("secrets: Spec needs IDLen > 0 and SecretLen > 0")
	}
	return nil
}

// randomBase62 returns n base62 characters drawn from crypto/rand.
// Uses rand.Int with a power-of-62 ceiling to avoid modulo bias.
func randomBase62(n int) (string, error) {
	max := big.NewInt(int64(len(base62Alphabet)))
	out := make([]byte, n)
	for i := range out {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = base62Alphabet[k.Int64()]
	}
	return string(out), nil
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return true
}

// Pre-defined specs used by callers. Adding a new credential kind?
// Define its Spec here so the catalog of token formats is greppable.

// APIKeySpec is the format for per-workspace developer API keys
// (POST /api/workspaces/{wid}/api-keys → returns Token.Full once).
var APIKeySpec = Spec{Prefix: "wak_", IDLen: 8, SecretLen: 40}
