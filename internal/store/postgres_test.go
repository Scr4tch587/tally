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

	_, err = pool.Exec(ctx, "TRUNCATE match_events, matches, canonical_events")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
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

func TestConfirmMatchPersistsMatchAndUpdatesEvents(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	_, err = pool.Exec(ctx, "TRUNCATE match_events, matches, canonical_events")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	evA, err := event.NewCanonicalEvent(
		"tenant-test",
		"evt-a",
		"ledger",
		"src-a",
		1250,
		"",
		"USD",
		ts,
		"debit",
		"cash",
		"Acme Supplies",
		nil,
	)
	if err != nil {
		t.Fatalf("build event A: %v", err)
	}
	evB, err := event.NewCanonicalEvent(
		"tenant-test",
		"evt-b",
		"processor",
		"src-b",
		1250,
		"",
		"USD",
		ts.Add(time.Second),
		"debit",
		"cash",
		"ACME SUPPLIES",
		nil,
	)
	if err != nil {
		t.Fatalf("build event B: %v", err)
	}

	for _, ev := range []*event.CanonicalEvent{evA, evB} {
		inserted, err := InsertEvent(ctx, pool, ev)
		if err != nil {
			t.Fatalf("insert %s: %v", ev.EventID, err)
		}
		if !inserted {
			t.Fatalf("expected insert for %s", ev.EventID)
		}
	}

	err = ConfirmMatch(ctx, pool, evA.EventID, evB.EventID, 0.92, map[string]any{"reason": "test"})
	if err != nil {
		t.Fatalf("confirm match: %v", err)
	}

	var status string
	err = pool.QueryRow(ctx, "SELECT match_status FROM canonical_events WHERE event_id = $1", evA.EventID).Scan(&status)
	if err != nil {
		t.Fatalf("query status A: %v", err)
	}
	if status != "MATCHED" {
		t.Fatalf("event A status = %q, want MATCHED", status)
	}

	var matchScore float64
	var evidenceReason string
	err = pool.QueryRow(ctx,
		`SELECT m.match_score, m.evidence->>'reason'
		   FROM matches m
		  WHERE m.match_id = $1`,
		"evt-a:evt-b",
	).Scan(&matchScore, &evidenceReason)
	if err != nil {
		t.Fatalf("query match row: %v", err)
	}
	if matchScore != 0.92 {
		t.Fatalf("match_score = %v, want 0.92", matchScore)
	}
	if evidenceReason != "test" {
		t.Fatalf("evidence reason = %q, want test", evidenceReason)
	}

	var linkCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM match_events WHERE match_id = $1", "evt-a:evt-b").Scan(&linkCount)
	if err != nil {
		t.Fatalf("count match_events: %v", err)
	}
	if linkCount != 2 {
		t.Fatalf("match_events count = %d, want 2", linkCount)
	}

	err = ConfirmMatch(ctx, pool, evA.EventID, evB.EventID, 0.92, nil)
	if err == nil {
		t.Fatal("expected second confirm to fail")
	}
}
