package codexexecgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// CapPayload is the parsed JSON payload from a CODEX_EXEC_GATEWAY_TOKEN.
// Under the 2026-05-10 refinement, tokens minted by codex-app-gateway
// contain exactly one exe_id. The verification logic accepts any-length
// exe_ids for forward-compatibility.
type CapPayload struct {
	TurnID      string   `json:"turn_id"`
	WorkspaceID string   `json:"workspace_id"`
	ExeIDs      []string `json:"exe_ids"`
	IAT         int64    `json:"iat"`
	EXP         int64    `json:"exp"`
}

// AllowsExeID reports whether the named exe_id is in the token's allow set.
func (p CapPayload) AllowsExeID(exeID string) bool {
	for _, id := range p.ExeIDs {
		if id == exeID {
			return true
		}
	}
	return false
}

var (
	ErrMalformed    = errors.New("malformed capability token")
	ErrBadSignature = errors.New("bad capability token signature")
	ErrExpired      = errors.New("capability token expired")
)

// VerifyCapabilityToken parses and verifies a 3-part HMAC capability token.
//
// Token format (spec § Capability token):
//
//	token   = base64url(header) "." base64url(payload) "." base64url(sig)
//	header  = '{"alg":"HS256","typ":"CXG"}'
//	payload = '{"turn_id":"...","workspace_id":"...","exe_ids":[...],"iat":...,"exp":...}'
//	sig     = HMAC-SHA256(secret, base64url(header) "." base64url(payload))
//
// base64url encoding uses no padding (RFC 7515 / JWT convention).
// HMAC comparison is constant-time via hmac.Equal to prevent timing attacks.
// Expiry is checked in UTC.
func VerifyCapabilityToken(token string, secret []byte) (CapPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return CapPayload{}, ErrMalformed
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]
	if headerB64 == "" || payloadB64 == "" || sigB64 == "" {
		return CapPayload{}, ErrMalformed
	}

	enc := base64.RawURLEncoding

	// Decode and validate header.
	headerBytes, err := enc.DecodeString(headerB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return CapPayload{}, ErrMalformed
	}
	if hdr.Alg != "HS256" || hdr.Typ != "CXG" {
		return CapPayload{}, ErrMalformed
	}

	// Decode claimed signature.
	gotSig, err := enc.DecodeString(sigB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}

	// Recompute HMAC over "headerB64.payloadB64" and compare constant-time.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	wantSig := mac.Sum(nil)
	if !hmac.Equal(gotSig, wantSig) {
		return CapPayload{}, ErrBadSignature
	}

	// Decode and unmarshal payload (after signature check so we don't act on
	// attacker-supplied JSON before verifying integrity).
	payloadBytes, err := enc.DecodeString(payloadB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}
	var payload CapPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return CapPayload{}, ErrMalformed
	}

	// Check expiry in UTC.
	if time.Now().UTC().Unix() > payload.EXP {
		return CapPayload{}, ErrExpired
	}

	return payload, nil
}
