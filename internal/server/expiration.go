package server

import (
	"errors"
	"time"
)

const (
	expirationDefaultDuration = 90 * 24 * time.Hour
	expirationMaxDuration     = 365 * 24 * time.Hour
	expirationClockSkew       = 1 * time.Minute // tolerance for "in the past"
)

// resolveExpiresAt parses a client-supplied RFC3339 timestamp into a UTC
// time.Time. When raw is empty, returns NOW + 90 days. Returns an error
// string suitable for an HTTP 422 response when:
//   - the string is not RFC3339-parseable
//   - the parsed time is in the past (beyond clock-skew tolerance)
//   - the parsed time is more than 365 days in the future
//
// All returned times are in UTC.
func resolveExpiresAt(raw string) (time.Time, error) {
	now := time.Now().UTC()
	if raw == "" {
		return now.Add(expirationDefaultDuration), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("expires_at must be an RFC3339 timestamp")
	}
	t = t.UTC()
	if t.Before(now.Add(-expirationClockSkew)) {
		return time.Time{}, errors.New("expires_at is in the past")
	}
	if t.After(now.Add(expirationMaxDuration)) {
		return time.Time{}, errors.New("expires_at is more than 365 days in the future")
	}
	return t, nil
}
