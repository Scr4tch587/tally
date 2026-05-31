package event

import (
	"testing"
	"time"
)

func newTestCanonicalEvent(t *testing.T, sourceType string, timestamp time.Time) *CanonicalEvent {
	t.Helper()

	ev, err := NewCanonicalEvent(
		"tenant-dev",
		"evt-1",
		sourceType,
		"source-evt-1",
		100,
		"USD",
		"USD",
		timestamp,
		"credit",
		"account-1",
		"counterparty-1",
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("NewCanonicalEvent returned error: %v", err)
	}

	return ev
}

func TestNewCanonicalEventRejectsInvalidSourceType(t *testing.T) {
	_, err := NewCanonicalEvent(
		"tenant-dev",
		"evt-1",
		"payments",
		"source-evt-1",
		100,
		"USD",
		"USD",
		time.Now(),
		"credit",
		"account-1",
		"counterparty-1",
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected invalid source type to return error")
	}
}

func TestNewCanonicalEventAcceptsValidSourceType(t *testing.T) {
	ev := newTestCanonicalEvent(t, "ledger", time.Now())

	if ev.SourceType != "ledger" {
		t.Fatalf("expected source type ledger, got %s", ev.SourceType)
	}
}

func TestNewCanonicalEventNormalizesTimestampToUTC(t *testing.T) {
	location := time.FixedZone("EST", -5*60*60)
	timestamp := time.Date(2026, 5, 31, 12, 0, 0, 0, location)

	ev := newTestCanonicalEvent(t, "ledger", timestamp)

	if ev.Timestamp.Location() != time.UTC {
		t.Fatalf("expected timestamp location UTC, got %v", ev.Timestamp.Location())
	}
	if !ev.Timestamp.Equal(timestamp) {
		t.Fatalf("expected timestamp instant to be preserved, got %v want %v", ev.Timestamp, timestamp)
	}
}

func TestNewCanonicalEventBuildsIdempotencyKey(t *testing.T) {
	ev := newTestCanonicalEvent(t, "processor", time.Now())

	expected := "tenant-dev:processor:source-evt-1"
	if ev.IdempotencyKey != expected {
		t.Fatalf("expected idempotency key %s, got %s", expected, ev.IdempotencyKey)
	}
}
