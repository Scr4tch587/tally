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

	truncateStoreTables(t, ctx, pool)

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

	truncateStoreTables(t, ctx, pool)

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

func TestFindPendingMatchCandidatesFiltersDurableCandidates(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateStoreTables(t, ctx, pool)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	current := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-current",
		sourceType:    "ledger",
		sourceEventID: "src-candidates-current",
		timestamp:     ts,
	})
	trueCandidate := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-true",
		sourceType:    "processor",
		sourceEventID: "src-candidates-true",
		timestamp:     ts.Add(time.Second),
	})
	adjacentAmountCandidate := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-adjacent",
		sourceType:    "bank",
		sourceEventID: "src-candidates-adjacent",
		amountMinor:   1251,
		timestamp:     ts.Add(2 * time.Second),
	})
	sameSource := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-same-source",
		sourceType:    "ledger",
		sourceEventID: "src-candidates-same-source",
		timestamp:     ts.Add(time.Second),
	})
	otherTenant := newStoreTestEvent(t, storeEventOpts{
		tenantID:      "tenant-other",
		eventID:       "evt-candidates-other-tenant",
		sourceType:    "processor",
		sourceEventID: "src-candidates-other-tenant",
		timestamp:     ts.Add(time.Second),
	})
	otherAsset := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-other-asset",
		sourceType:    "processor",
		sourceEventID: "src-candidates-other-asset",
		assetCode:     "USDC",
		timestamp:     ts.Add(time.Second),
	})
	outOfAmount := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-out-amount",
		sourceType:    "processor",
		sourceEventID: "src-candidates-out-amount",
		amountMinor:   1252,
		timestamp:     ts.Add(time.Second),
	})
	outOfTime := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-out-time",
		sourceType:    "processor",
		sourceEventID: "src-candidates-out-time",
		timestamp:     ts.Add(121 * time.Second),
	})
	matchedCandidate := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-matched",
		sourceType:    "processor",
		sourceEventID: "src-candidates-matched",
		timestamp:     ts.Add(time.Second),
	})
	matchedPeer := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-candidates-matched-peer",
		sourceType:    "bank",
		sourceEventID: "src-candidates-matched-peer",
		timestamp:     ts.Add(time.Second),
	})

	insertStoreEvents(t, ctx, pool,
		current,
		trueCandidate,
		adjacentAmountCandidate,
		sameSource,
		otherTenant,
		otherAsset,
		outOfAmount,
		outOfTime,
		matchedCandidate,
		matchedPeer,
	)

	if err := ConfirmMatch(ctx, pool, matchedCandidate.EventID, matchedPeer.EventID, 0.91, nil); err != nil {
		t.Fatalf("confirm matched candidate fixture: %v", err)
	}

	candidates, err := FindPendingMatchCandidates(ctx, pool, current, 120000)
	if err != nil {
		t.Fatalf("FindPendingMatchCandidates: %v", err)
	}

	got := candidateIDSet(candidates)
	wantPresent := []string{trueCandidate.EventID, adjacentAmountCandidate.EventID}
	for _, eventID := range wantPresent {
		if !got[eventID] {
			t.Fatalf("expected candidate %s in results; got %v", eventID, got)
		}
	}

	wantAbsent := []string{
		current.EventID,
		sameSource.EventID,
		otherTenant.EventID,
		otherAsset.EventID,
		outOfAmount.EventID,
		outOfTime.EventID,
		matchedCandidate.EventID,
		matchedPeer.EventID,
	}
	for _, eventID := range wantAbsent {
		if got[eventID] {
			t.Fatalf("did not expect candidate %s in results; got %v", eventID, got)
		}
	}
}

func TestFindPendingMatchCandidatesUsesAssetCodeBeforeCurrency(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateStoreTables(t, ctx, pool)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	current := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-asset-current",
		sourceType:    "ledger",
		sourceEventID: "src-asset-current",
		assetCode:     "USDC",
		currency:      "USD",
		timestamp:     ts,
	})
	sameAsset := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-asset-same",
		sourceType:    "processor",
		sourceEventID: "src-asset-same",
		assetCode:     "USDC",
		currency:      "USD",
		timestamp:     ts.Add(time.Second),
	})
	currencyOnly := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-asset-currency-only",
		sourceType:    "processor",
		sourceEventID: "src-asset-currency-only",
		currency:      "USD",
		timestamp:     ts.Add(time.Second),
	})

	insertStoreEvents(t, ctx, pool, current, sameAsset, currencyOnly)

	candidates, err := FindPendingMatchCandidates(ctx, pool, current, 120000)
	if err != nil {
		t.Fatalf("FindPendingMatchCandidates: %v", err)
	}

	got := candidateIDSet(candidates)
	if !got[sameAsset.EventID] {
		t.Fatalf("expected same asset candidate in results; got %v", got)
	}
	if got[currencyOnly.EventID] {
		t.Fatalf("did not expect currency fallback candidate when event has asset code; got %v", got)
	}
}

func TestFindRecentPendingEventsReturnsOnlyPendingWithLimit(t *testing.T) {
	ctx := context.Background()

	pool, err := Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateStoreTables(t, ctx, pool)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	pendingA := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-recent-pending-a",
		sourceType:    "ledger",
		sourceEventID: "src-recent-pending-a",
		timestamp:     ts,
	})
	pendingB := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-recent-pending-b",
		sourceType:    "processor",
		sourceEventID: "src-recent-pending-b",
		timestamp:     ts.Add(time.Second),
	})
	matchedA := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-recent-matched-a",
		sourceType:    "ledger",
		sourceEventID: "src-recent-matched-a",
		timestamp:     ts.Add(2 * time.Second),
	})
	matchedB := newStoreTestEvent(t, storeEventOpts{
		eventID:       "evt-recent-matched-b",
		sourceType:    "processor",
		sourceEventID: "src-recent-matched-b",
		timestamp:     ts.Add(3 * time.Second),
	})

	insertStoreEvents(t, ctx, pool, pendingA, pendingB, matchedA, matchedB)

	if err := ConfirmMatch(ctx, pool, matchedA.EventID, matchedB.EventID, 0.91, nil); err != nil {
		t.Fatalf("confirm matched fixture: %v", err)
	}

	eventIDs, err := FindRecentPendingEvents(ctx, pool, 10)
	if err != nil {
		t.Fatalf("FindRecentPendingEvents: %v", err)
	}

	got := stringSet(eventIDs)
	for _, eventID := range []string{pendingA.EventID, pendingB.EventID} {
		if !got[eventID] {
			t.Fatalf("expected pending event %s in results; got %v", eventID, got)
		}
	}
	for _, eventID := range []string{matchedA.EventID, matchedB.EventID} {
		if got[eventID] {
			t.Fatalf("did not expect matched event %s in results; got %v", eventID, got)
		}
	}

	limitedEventIDs, err := FindRecentPendingEvents(ctx, pool, 1)
	if err != nil {
		t.Fatalf("FindRecentPendingEvents limit 1: %v", err)
	}
	if len(limitedEventIDs) != 1 {
		t.Fatalf("limited pending event count = %d, want 1", len(limitedEventIDs))
	}
}

type storeEventOpts struct {
	eventID       string
	tenantID      string
	sourceType    string
	sourceEventID string
	amountMinor   int64
	assetCode     string
	currency      string
	timestamp     time.Time
}

func truncateStoreTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	acquireStoreTestLock(t, ctx, pool)
	_, err := pool.Exec(ctx, "TRUNCATE match_events, matches, canonical_events")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

func acquireStoreTestLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire postgres test lock connection: %v", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(8675309)); err != nil {
		conn.Release()
		t.Fatalf("acquire postgres test lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(8675309))
		conn.Release()
	})
}

func newStoreTestEvent(t *testing.T, opts storeEventOpts) *event.CanonicalEvent {
	t.Helper()

	if opts.timestamp.IsZero() {
		opts.timestamp = time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	}
	if opts.tenantID == "" {
		opts.tenantID = "tenant-test"
	}
	if opts.amountMinor == 0 {
		opts.amountMinor = 1250
	}
	if opts.currency == "" {
		opts.currency = "USD"
	}

	ev, err := event.NewCanonicalEvent(
		opts.tenantID,
		opts.eventID,
		opts.sourceType,
		opts.sourceEventID,
		opts.amountMinor,
		opts.assetCode,
		opts.currency,
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

func candidateIDSet(candidates []*event.CanonicalEvent) map[string]bool {
	ids := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ids[candidate.EventID] = true
	}
	return ids
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
