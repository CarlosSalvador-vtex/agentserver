package codexappgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// MintCapToken produces a capability token consumed by codex-exec-gateway's
// VerifyCapabilityToken. Format and HMAC are kept identical (HS256 over
// "headerB64.payloadB64", base64url-no-pad) — see
// internal/codexexecgateway/auth.go for the verifier.
//
// Per the 2026-05-10 refinement, each minted token authorises exactly one
// exe_id (one bridge connection per executor per turn). Verifier still
// accepts multi-id payloads for forward compat.
func MintCapToken(secret []byte, turnID, workspaceID, exeID string, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("captoken: empty secret")
	}
	if turnID == "" || workspaceID == "" || exeID == "" {
		return "", fmt.Errorf("captoken: turnID/workspaceID/exeID required")
	}
	now := time.Now().UTC().Unix()
	payload := struct {
		TurnID      string   `json:"turn_id"`
		WorkspaceID string   `json:"workspace_id"`
		ExeIDs      []string `json:"exe_ids"`
		IAT         int64    `json:"iat"`
		EXP         int64    `json:"exp"`
	}{
		TurnID:      turnID,
		WorkspaceID: workspaceID,
		ExeIDs:      []string{exeID},
		IAT:         now,
		EXP:         now + int64(ttl.Seconds()),
	}
	pj, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("captoken: marshal payload: %w", err)
	}
	enc := base64.RawURLEncoding
	headerB64 := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"CXG"}`))
	payloadB64 := enc.EncodeToString(pj)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	return headerB64 + "." + payloadB64 + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
