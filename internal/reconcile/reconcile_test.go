package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"tally/internal/event"
	"tally/internal/store"
)

func TestReconcilePendingEventMatchesViaPostgresWithoutRedisCandidates(t *testing.T) {
	ctx := context.Background()
	pool, client := setupReconcileTest(t, ctx)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ledger := newReconcileTestEvent(t, "evt-reconcile-ledger", "ledger", "src-reconcile-ledger", ts)
	processor := newReconcileTestEvent(t, "evt-reconcile-processor", "processor", "src-reconcile-processor", ts.Add(time.Second))
	insertReconcileEvents(t, ctx, pool, ledger, processor)

	engine := NewEngine(pool, zerolog.Nop(), client)
	if err := engine.ReconcilePendingEvent(ctx, ledger.EventID); err != nil {
		t.Fatalf("ReconcilePendingEvent: %v", err)
	}

	statuses := reconcileEventStatuses(t, ctx, pool, ledger.EventID, processor.EventID)
	if statuses[ledger.EventID] != "MATCHED" {
		t.Fatalf("ledger status = %q, want MATCHED", statuses[ledger.EventID])
	}
	if statuses[processor.EventID] != "MATCHED" {
		t.Fatalf("processor status = %q, want MATCHED", statuses[processor.EventID])
	}

	var matchCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM matches").Scan(&matchCount); err != nil {
		t.Fatalf("count matches: %v", err)
	}
	if matchCount != 1 {
		t.Fatalf("matches count = %d, want 1", matchCount)
	}
}

func TestReconcileRecentPendingMatchesPendingPair(t *testing.T) {
	ctx := context.Background()
	pool, client := setupReconcileTest(t, ctx)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ledger := newReconcileTestEvent(t, "evt-reconcile-recent-ledger", "ledger", "src-reconcile-recent-ledger", ts)
	processor := newReconcileTestEvent(t, "evt-reconcile-recent-processor", "processor", "src-reconcile-recent-processor", ts.Add(time.Second))
	insertReconcileEvents(t, ctx, pool, ledger, processor)

	engine := NewEngine(pool, zerolog.Nop(), client)
	if err := engine.ReconcileRecentPending(ctx, 10); err != nil {
		t.Fatalf("ReconcileRecentPending: %v", err)
	}

	statuses := reconcileEventStatuses(t, ctx, pool, ledger.EventID, processor.EventID)
	if statuses[ledger.EventID] != "MATCHED" {
		t.Fatalf("ledger status = %q, want MATCHED", statuses[ledger.EventID])
	}
	if statuses[processor.EventID] != "MATCHED" {
		t.Fatalf("processor status = %q, want MATCHED", statuses[processor.EventID])
	}
}

func TestReconcilePendingEventRestoresUnmatchedEventToRedis(t *testing.T) {
	ctx := context.Background()
	pool, client := setupReconcileTest(t, ctx)

	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	ledger := newReconcileTestEvent(t, "evt-reconcile-unmatched", "ledger", "src-reconcile-unmatched", ts)
	insertReconcileEvents(t, ctx, pool, ledger)

	engine := NewEngine(pool, zerolog.Nop(), client)
	if err := engine.ReconcilePendingEvent(ctx, ledger.EventID); err != nil {
		t.Fatalf("ReconcilePendingEvent: %v", err)
	}

	candidates, err := store.FindCandidates(ctx, client, ledger, 120000)
	if err != nil {
		t.Fatalf("FindCandidates: %v", err)
	}
	for _, candidateID := range candidates {
		if candidateID == ledger.EventID {
			return
		}
	}
	t.Fatalf("expected unmatched event %s to be restored to Redis candidates; got %v", ledger.EventID, candidates)
}

func setupReconcileTest(t *testing.T, ctx context.Context) (*pgxpool.Pool, *redis.Client) {
	t.Helper()

	pool, err := store.Connect(ctx)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	acquireReconcileTestLock(t, ctx, pool)
	if _, err := pool.Exec(ctx, "TRUNCATE match_events, matches, canonical_events"); err != nil {
		t.Fatalf("truncate postgres tables: %v", err)
	}

	client := store.NewRedisClient()
	t.Cleanup(func() {
		_ = client.Close()
	})
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis: %v", err)
	}

	return pool, client
}

func acquireReconcileTestLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
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

func newReconcileTestEvent(t *testing.T, eventID, sourceType, sourceEventID string, ts time.Time) *event.CanonicalEvent {
	t.Helper()

	ev, err := event.NewCanonicalEvent(
		"tenant-reconcile-test",
		eventID,
		sourceType,
		sourceEventID,
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
		t.Fatalf("build canonical event: %v", err)
	}
	return ev
}

func insertReconcileEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, events ...*event.CanonicalEvent) {
	t.Helper()

	for _, ev := range events {
		inserted, err := store.InsertEvent(ctx, pool, ev)
		if err != nil {
			t.Fatalf("insert %s: %v", ev.EventID, err)
		}
		if !inserted {
			t.Fatalf("expected insert for %s", ev.EventID)
		}
	}
}

func reconcileEventStatuses(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventIDs ...string) map[string]string {
	t.Helper()

	statuses := map[string]string{}
	rows, err := pool.Query(ctx, "SELECT event_id, match_status FROM canonical_events WHERE event_id = ANY($1)", eventIDs)
	if err != nil {
		t.Fatalf("query event statuses: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventID, status string
		if err := rows.Scan(&eventID, &status); err != nil {
			t.Fatalf("scan event status: %v", err)
		}
		statuses[eventID] = status
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event statuses: %v", err)
	}
	return statuses
}
