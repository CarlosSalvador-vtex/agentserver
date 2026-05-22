// NOTE: This file is intentionally NOT migrated to internal/secrets.
// ast_ codex tokens use bcrypt hashing and base36 encoding. Migrating
// would invalidate all existing tokens stored in the DB (bcrypt hashes
// are not forward-compatible with sha256 hashes). For NEW credential
// kinds, use internal/secrets instead of duplicating this pattern.
package server

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
)

const (
	codexTokenPrefix    = "ast_"
	codexTokenIDLen     = 8
	codexTokenSecretLen = 40
	codexTokenAlphabet  = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var errBadCodexToken = errors.New("bad codex token format")

// generateCodexToken returns (full_token, id, secret, err) where full_token
// is what we hand the user and id/secret are the parts we persist (id as PK,
// bcrypt(secret) as token_hash).
func generateCodexToken() (full, id, secret string, err error) {
	id, err = randomBase36(codexTokenIDLen)
	if err != nil {
		return
	}
	secret, err = randomBase36(codexTokenSecretLen)
	if err != nil {
		return
	}
	full = codexTokenPrefix + id + "_" + secret
	return
}

// parseCodexToken validates shape and splits a token into (id, secret).
func parseCodexToken(tok string) (id, secret string, err error) {
	if !strings.HasPrefix(tok, codexTokenPrefix) {
		return "", "", errBadCodexToken
	}
	rest := tok[len(codexTokenPrefix):]
	sep := strings.IndexByte(rest, '_')
	// sep must be exactly at codexTokenIDLen (8 chars for id), and not at the end
	if sep != codexTokenIDLen || sep == len(rest)-1 {
		return "", "", errBadCodexToken
	}
	return rest[:sep], rest[sep+1:], nil
}

func randomBase36(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(codexTokenAlphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = codexTokenAlphabet[k.Int64()]
	}
	return string(b), nil
}
