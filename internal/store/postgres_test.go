package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

func TestInsertEventIsIdempotent(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateStoreTables(t, ctx, pool)

	ev := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-idempotent",
		sourceType:    "ledger",
		sourceEventID: "src-idempotent",
	})

	inserted, err := InsertEvent(ctx, pool, ev)
	if err != nil {
		t.Fatalf("first InsertEvent: %v", err)
	}
	if !inserted {
		t.Fatal("expected first insert to create a row")
	}

	inserted, err = InsertEvent(ctx, pool, ev)
	if err != nil {
		t.Fatalf("second InsertEvent: %v", err)
	}
	if inserted {
		t.Fatal("expected replay insert to be ignored")
	}

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM canonical_events WHERE idempotency_key = $1", ev.IdempotencyKey).Scan(&count)
	if err != nil {
		t.Fatalf("count canonical_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("canonical_events count = %d, want 1", count)
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

func TestConfirmMatchPreventsConcurrentDoubleMatch(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateStoreTables(t, ctx, pool)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	evA := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-race-a",
		sourceType:    "ledger",
		sourceEventID: "src-race-a",
		timestamp:     ts,
	})
	evB := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-race-b",
		sourceType:    "processor",
		sourceEventID: "src-race-b",
		timestamp:     ts.Add(time.Second),
	})
	evC := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-race-c",
		sourceType:    "bank",
		sourceEventID: "src-race-c",
		timestamp:     ts.Add(2 * time.Second),
	})

	insertStoreEvents(t, ctx, pool, evA, evB, evC)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup

	for _, pair := range [][2]string{
		{evA.EventID, evB.EventID},
		{evA.EventID, evC.EventID},
	} {
		wg.Add(1)
		go func(eventA, eventB string) {
			defer wg.Done()
			<-start
			errs <- ConfirmMatch(ctx, pool, eventA, eventB, 0.91, map[string]any{"test": "race"})
		}(pair[0], pair[1])
	}

	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	failures := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("ConfirmMatch race successes=%d failures=%d, want 1/1", successes, failures)
	}

	var matchCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM matches").Scan(&matchCount)
	if err != nil {
		t.Fatalf("count matches: %v", err)
	}
	if matchCount != 1 {
		t.Fatalf("matches count = %d, want 1", matchCount)
	}

	var linkCount int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM match_events").Scan(&linkCount)
	if err != nil {
		t.Fatalf("count match_events: %v", err)
	}
	if linkCount != 2 {
		t.Fatalf("match_events count = %d, want 2", linkCount)
	}

	statuses := map[string]string{}
	rows, err := pool.Query(ctx, "SELECT event_id, match_status FROM canonical_events WHERE event_id IN ($1, $2, $3)", evA.EventID, evB.EventID, evC.EventID)
	if err != nil {
		t.Fatalf("query statuses: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventID, status string
		if err := rows.Scan(&eventID, &status); err != nil {
			t.Fatalf("scan status: %v", err)
		}
		statuses[eventID] = status
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate statuses: %v", err)
	}

	if statuses[evA.EventID] != "MATCHED" {
		t.Fatalf("shared event status = %q, want MATCHED", statuses[evA.EventID])
	}

	matchedCounterparts := 0
	for _, eventID := range []string{evB.EventID, evC.EventID} {
		if statuses[eventID] == "MATCHED" {
			matchedCounterparts++
		}
	}
	if matchedCounterparts != 1 {
		t.Fatalf("matched counterparts = %d, want 1; statuses=%v", matchedCounterparts, statuses)
	}
}

type storeEventOpts struct {
	eventID       string
	sourceType    string
	sourceEventID string
	timestamp     time.Time
}

func truncateStoreTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	_, err := pool.Exec(ctx, "TRUNCATE match_events, matches, canonical_events")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

func newStoreTestEvent(t *testing.T, opts storeEventOpts) *event.CanonicalEvent {
	t.Helper()

	if opts.timestamp.IsZero() {
		opts.timestamp = time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	}

	ev, err := event.NewCanonicalEvent(
		"tenant-test",
		opts.eventID,
		opts.sourceType,
		opts.sourceEventID,
		1250,
		"",
		"USD",
		opts.timestamp,
		"debit",
		"cash",
		"Acme Supplies",
		nil,
	)
	if err != nil {
		t.Fatalf("build canonical event: %v", err)
	}

	return ev
}

func insertStoreEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, events ...*event.CanonicalEvent) {
	t.Helper()

	for _, ev := range events {
		inserted, err := InsertEvent(ctx, pool, ev)
		if err != nil {
			t.Fatalf("insert %s: %v", ev.EventID, err)
		}
		if !inserted {
			t.Fatalf("expected insert for %s", ev.EventID)
		}
	}
}
