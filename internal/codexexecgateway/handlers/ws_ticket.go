package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// wsTicketTTL bounds the window between the cloud-register response and
// the immediately-following ws upgrade. Codex re-registers on every
// reconnect, so we don't need long-lived tickets.
const wsTicketTTL = 5 * time.Minute

// MintWSTicket returns a short-lived bearer that authorizes the
// `/codex-exec/{exe_id}?token=...` ws upgrade. Format:
//
//	<exe_id>.<expiry_unix>.<base64url(HMAC-SHA256(secret, "<exe_id>.<expiry>"))>
//
// The inbound handler recomputes the HMAC with its own secret and
// confirms the exe_id matches and the expiry is in the future. No DB
// round-trip; no bcrypt; no JWT verification.
func MintWSTicket(exeID, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("internal secret not configured")
	}
	expiry := time.Now().Add(wsTicketTTL).Unix()
	payload := exeID + "." + strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// VerifyWSTicket returns nil iff the ticket is well-formed, signed with
// `secret`, names the expected exe_id, and has not yet expired.
func VerifyWSTicket(ticket, expectedExeID, secret string) error {
	if secret == "" {
		return fmt.Errorf("internal secret not configured")
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed ticket")
	}
	exeID, expStr, sigB64 := parts[0], parts[1], parts[2]
	if exeID != expectedExeID {
		return fmt.Errorf("ticket exe_id mismatch")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return fmt.Errorf("bad expiry: %w", err)
	}
	if time.Now().Unix() >= exp {
		return fmt.Errorf("ticket expired")
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write([]byte(exeID + "." + expStr))
	wantSig := want.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("bad signature encoding: %w", err)
	}
	if !hmac.Equal(wantSig, gotSig) {
		return fmt.Errorf("bad signature")
	}
	return nil
}
