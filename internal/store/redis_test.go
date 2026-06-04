package store

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"tally/internal/event"
)

func TestCandidateKey(t *testing.T) {
	got := candidateKey("tenant-1", "USD", 1250)
	want := "candidates:tenant-1:USD:1250"

	if got != want {
		t.Fatalf("candidateKey() = %q, want %q", got, want)
	}
}

func TestCandidateBuckets(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		want   []int64
	}{
		{name: "middle amount", amount: 1250, want: []int64{1250, 1249, 1251}},
		{name: "zero amount skips negative bucket", amount: 0, want: []int64{0, 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := candidateBuckets(tc.amount)
			if len(got) != len(tc.want) {
				t.Fatalf("candidateBuckets(%d) = %v, want %v", tc.amount, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("candidateBuckets(%d) = %v, want %v", tc.amount, got, tc.want)
				}
			}
		})
	}
}

func TestAddCandidateUsesTenantAssetAmountKeysAndEventTimestampScore(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 123_000_000, time.UTC)
	ev := mustCanonicalEvent(t, "tenant-1", "evt-1", "ledger", "src-1", 1250, "USDC", "USD", timestamp)
	t.Cleanup(func() {
		deleteCandidateKeys(ctx, client, ev.TenantID, ev.AssetCode, candidateBuckets(ev.AmountMinor))
	})

	if err := AddCandidate(ctx, client, ev); err != nil {
		t.Fatalf("AddCandidate returned error: %v", err)
	}

	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, ev.AssetCode, amount)
		score, err := client.ZScore(ctx, key, ev.EventID).Result()
		if err != nil {
			t.Fatalf("ZScore(%q, %q): %v", key, ev.EventID, err)
		}
		if score != float64(timestamp.UnixMilli()) {
			t.Fatalf("candidate score for %q = %v, want %v", key, score, timestamp.UnixMilli())
		}
	}
}

func TestAddCandidateFallsBackToCurrencyWhenAssetCodeIsEmpty(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	ev := mustCanonicalEvent(t, "tenant-1", "evt-2", "processor", "src-2", 1250, "", "USD", timestamp)
	t.Cleanup(func() {
		deleteCandidateKeys(ctx, client, ev.TenantID, ev.Currency, candidateBuckets(ev.AmountMinor))
	})

	if err := AddCandidate(ctx, client, ev); err != nil {
		t.Fatalf("AddCandidate returned error: %v", err)
	}

	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, ev.Currency, amount)
		members, err := client.ZRange(ctx, key, 0, -1).Result()
		if err != nil {
			t.Fatalf("ZRange(%q): %v", key, err)
		}
		if len(members) != 1 || members[0] != ev.EventID {
			t.Fatalf("candidate members for %q = %v, want [%s]", key, members, ev.EventID)
		}
	}
}

func TestFindCandidatesFindsExactAndAdjacentBucketsOnce(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	candidate := mustCanonicalEvent(t, "tenant-find-1", "evt-candidate", "ledger", "src-candidate", 1250, "USD", "USD", timestamp)
	query := mustCanonicalEvent(t, "tenant-find-1", "evt-query", "processor", "src-query", 1251, "USD", "USD", timestamp.Add(time.Second))
	cleanupEventCandidateKeys(ctx, client, candidate)
	cleanupEventCandidateKeys(ctx, client, query)
	t.Cleanup(func() {
		cleanupEventCandidateKeys(ctx, client, candidate)
		cleanupEventCandidateKeys(ctx, client, query)
	})

	if err := AddCandidate(ctx, client, candidate); err != nil {
		t.Fatalf("AddCandidate returned error: %v", err)
	}

	got, err := FindCandidates(ctx, client, query, 120_000)
	if err != nil {
		t.Fatalf("FindCandidates returned error: %v", err)
	}
	if len(got) != 1 || got[0] != candidate.EventID {
		t.Fatalf("FindCandidates() = %v, want [%s]", got, candidate.EventID)
	}
}

func TestFindCandidatesScopesByTenantAndAsset(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	tenantMatch := mustCanonicalEvent(t, "tenant-find-2", "evt-tenant-match", "ledger", "src-tenant-match", 1250, "USD", "USD", timestamp)
	differentTenant := mustCanonicalEvent(t, "tenant-other", "evt-other-tenant", "ledger", "src-other-tenant", 1250, "USD", "USD", timestamp)
	differentAsset := mustCanonicalEvent(t, "tenant-find-2", "evt-other-asset", "ledger", "src-other-asset", 1250, "USDC", "USD", timestamp)
	query := mustCanonicalEvent(t, "tenant-find-2", "evt-query-2", "processor", "src-query-2", 1250, "USD", "USD", timestamp)

	for _, ev := range []*event.CanonicalEvent{tenantMatch, differentTenant, differentAsset, query} {
		cleanupEventCandidateKeys(ctx, client, ev)
	}
	t.Cleanup(func() {
		for _, ev := range []*event.CanonicalEvent{tenantMatch, differentTenant, differentAsset, query} {
			cleanupEventCandidateKeys(ctx, client, ev)
		}
	})

	for _, ev := range []*event.CanonicalEvent{tenantMatch, differentTenant, differentAsset} {
		if err := AddCandidate(ctx, client, ev); err != nil {
			t.Fatalf("AddCandidate(%s) returned error: %v", ev.EventID, err)
		}
	}

	got, err := FindCandidates(ctx, client, query, 120_000)
	if err != nil {
		t.Fatalf("FindCandidates returned error: %v", err)
	}
	if len(got) != 1 || got[0] != tenantMatch.EventID {
		t.Fatalf("FindCandidates() = %v, want [%s]", got, tenantMatch.EventID)
	}
}

func TestFindCandidatesRespectsEventTimeWindow(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	insideWindow := mustCanonicalEvent(t, "tenant-find-3", "evt-inside-window", "ledger", "src-inside-window", 1250, "USD", "USD", timestamp.Add(-119*time.Second))
	outsideWindow := mustCanonicalEvent(t, "tenant-find-3", "evt-outside-window", "ledger", "src-outside-window", 1250, "USD", "USD", timestamp.Add(-121*time.Second))
	query := mustCanonicalEvent(t, "tenant-find-3", "evt-query-3", "processor", "src-query-3", 1250, "USD", "USD", timestamp)

	for _, ev := range []*event.CanonicalEvent{insideWindow, outsideWindow, query} {
		cleanupEventCandidateKeys(ctx, client, ev)
	}
	t.Cleanup(func() {
		for _, ev := range []*event.CanonicalEvent{insideWindow, outsideWindow, query} {
			cleanupEventCandidateKeys(ctx, client, ev)
		}
	})

	for _, ev := range []*event.CanonicalEvent{insideWindow, outsideWindow} {
		if err := AddCandidate(ctx, client, ev); err != nil {
			t.Fatalf("AddCandidate(%s) returned error: %v", ev.EventID, err)
		}
	}

	got, err := FindCandidates(ctx, client, query, 120_000)
	if err != nil {
		t.Fatalf("FindCandidates returned error: %v", err)
	}
	if len(got) != 1 || got[0] != insideWindow.EventID {
		t.Fatalf("FindCandidates() = %v, want [%s]", got, insideWindow.EventID)
	}
}

func TestRemoveCandidateRemovesFromAllBuckets(t *testing.T) {
	ctx := context.Background()
	client := redisClientOrSkip(t, ctx)

	timestamp := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	ev := mustCanonicalEvent(t, "tenant-remove-1", "evt-remove", "ledger", "src-remove", 1250, "USD", "USD", timestamp)
	cleanupEventCandidateKeys(ctx, client, ev)
	t.Cleanup(func() {
		cleanupEventCandidateKeys(ctx, client, ev)
	})

	if err := AddCandidate(ctx, client, ev); err != nil {
		t.Fatalf("AddCandidate returned error: %v", err)
	}
	if err := RemoveCandidate(ctx, client, ev); err != nil {
		t.Fatalf("RemoveCandidate returned error: %v", err)
	}

	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, ev.AssetCode, amount)
		count, err := client.ZCount(ctx, key, "-inf", "+inf").Result()
		if err != nil {
			t.Fatalf("ZCount(%q): %v", key, err)
		}
		if count != 0 {
			t.Fatalf("candidate count for %q = %d, want 0", key, count)
		}
	}
}

func deleteCandidateKeys(ctx context.Context, client *redis.Client, tenantID, asset string, amounts []int64) {
	for _, amount := range amounts {
		client.Del(ctx, candidateKey(tenantID, asset, amount))
	}
}

func cleanupEventCandidateKeys(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent) {
	asset := ev.AssetCode
	if asset == "" {
		asset = ev.Currency
	}
	deleteCandidateKeys(ctx, client, ev.TenantID, asset, candidateBuckets(ev.AmountMinor))
}

func redisClientOrSkip(t *testing.T, ctx context.Context) *redis.Client {
	t.Helper()

	client := NewRedisClient()
	t.Cleanup(func() {
		client.Close()
	})

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis is not running locally: %v", err)
	}

	return client
}

func mustCanonicalEvent(t *testing.T, tenantID, eventID, sourceType, sourceEventID string, amountMinor int64, assetCode, currency string, timestamp time.Time) *event.CanonicalEvent {
	t.Helper()

	ev, err := event.NewCanonicalEvent(
		tenantID,
		eventID,
		sourceType,
		sourceEventID,
		amountMinor,
		assetCode,
		currency,
		timestamp,
		"debit",
		"cash",
		"Acme Supplies",
		nil,
	)
	if err != nil {
		t.Fatalf("NewCanonicalEvent: %v", err)
	}

	return ev
}
