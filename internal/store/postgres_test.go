package store

import (
	"context"
	"testing"
	"time"

	"tally/internal/event"
)

func TestInsertEventPersistsMetadata(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, "TRUNCATE canonical_events")
	if err != nil {
		t.Fatalf("truncate canonical_events: %v", err)
	}

	ev, err := event.NewCanonicalEvent(
		"tenant-test",
		"evt-1",
		"ledger",
		"src-1",
		1250,
		"",
		"USD",
		time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
		"debit",
		"cash",
		"Acme Supplies",
		map[string]string{
			"merchant": "acme",
			"channel":  "card",
		},
	)
	if err != nil {
		t.Fatalf("build canonical event: %v", err)
	}

	inserted, err := InsertEvent(ctx, pool, ev)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if !inserted {
		t.Fatalf("expected first insert to create a row")
	}

	var merchant, channel string
	err = pool.QueryRow(
		ctx,
		`SELECT metadata->>'merchant', metadata->>'channel'
		 FROM canonical_events
		 WHERE event_id = $1`,
		ev.EventID,
	).Scan(&merchant, &channel)
	if err != nil {
		t.Fatalf("query inserted metadata: %v", err)
	}

	if merchant != "acme" {
		t.Fatalf("merchant metadata = %q, want %q", merchant, "acme")
	}
	if channel != "card" {
		t.Fatalf("channel metadata = %q, want %q", channel, "card")
	}
}
