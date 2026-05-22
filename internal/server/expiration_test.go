package server

import (
	"testing"
	"time"
)

func TestResolveExpiresAt_Empty_DefaultsTo90Days(t *testing.T) {
	before := time.Now().UTC()
	got, err := resolveExpiresAt("")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lo := before.Add(expirationDefaultDuration)
	hi := after.Add(expirationDefaultDuration)
	if got.Before(lo) || got.After(hi) {
		t.Fatalf("got %v, want in [%v, %v]", got, lo, hi)
	}
}

func TestResolveExpiresAt_ValidTimestamp(t *testing.T) {
	ts := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	raw := ts.Format(time.RFC3339)
	got, err := resolveExpiresAt(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("got %v, want %v", got, ts)
	}
}

func TestResolveExpiresAt_RejectsInvalidFormat(t *testing.T) {
	_, err := resolveExpiresAt("2026-08-20")
	if err == nil {
		t.Fatal("expected error for non-RFC3339 string")
	}
}

func TestResolveExpiresAt_RejectsPastTimestamp(t *testing.T) {
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	_, err := resolveExpiresAt(past)
	if err == nil {
		t.Fatal("expected error for past timestamp")
	}
}

func TestResolveExpiresAt_RejectsMoreThan365Days(t *testing.T) {
	future := time.Now().UTC().Add(400 * 24 * time.Hour).Format(time.RFC3339)
	_, err := resolveExpiresAt(future)
	if err == nil {
		t.Fatal("expected error for >365d future timestamp")
	}
}

func TestResolveExpiresAt_AcceptsClockSkewBoundary(t *testing.T) {
	// 30 seconds in the past is within the 1-minute clock skew tolerance.
	nearPast := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	_, err := resolveExpiresAt(nearPast)
	if err != nil {
		t.Fatalf("expected no error within clock skew, got: %v", err)
	}
}

func TestResolveExpiresAt_RejectsJustBeyondClockSkew(t *testing.T) {
	// 2 minutes in the past exceeds the 1-minute clock skew tolerance.
	beyondSkew := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	_, err := resolveExpiresAt(beyondSkew)
	if err == nil {
		t.Fatal("expected error for timestamp beyond clock skew")
	}
}

func TestResolveExpiresAt_ResultIsUTC(t *testing.T) {
	// Supply a timestamp with timezone offset — result must be UTC.
	ts := time.Now().Add(10 * 24 * time.Hour)
	// Use a +05:30 offset string by adding 5.5h and formatting with a fixed zone
	loc := time.FixedZone("IST", 5*3600+30*60)
	raw := ts.In(loc).Format(time.RFC3339)
	got, err := resolveExpiresAt(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC, got %v", got.Location())
	}
}
