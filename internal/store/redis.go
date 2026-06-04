package store

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"tally/internal/event"
)

func NewRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:6379"})
}

func candidateKey(tenantID string, asset string, amountBucket int64) string {
	return fmt.Sprintf("candidates:%s:%s:%d", tenantID, asset, amountBucket)
}

func candidateBuckets(amount int64) []int64 {
	output := make([]int64, 0)
	output = append(output, amount)
	if amount >= 1 {
		output = append(output, amount-1)
	}
	output = append(output, amount+1)
	return output
}

func AddCandidate(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent) error {
	asset := ev.AssetCode
	if asset == "" {
		asset = ev.Currency
	}

	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, asset, amount)
		_, err := client.ZAdd(ctx, key, redis.Z{
			Score:  float64(ev.Timestamp.UnixMilli()),
			Member: ev.EventID,
		}).Result()
		if err != nil {
			return err
		}
	}
	return nil
}

func FindCandidates(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent, maxAgeMillis int64) ([]string, error) {
	asset := ev.AssetCode
	if asset == "" {
		asset = ev.Currency
	}

	bucketIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, asset, amount)
		eIDs, err := client.ZRangeArgs(ctx, redis.ZRangeArgs{
			Key:     key,
			Start:   fmt.Sprintf("%d", ev.Timestamp.UnixMilli()-maxAgeMillis),
			Stop:    fmt.Sprintf("%d", ev.Timestamp.UnixMilli()+maxAgeMillis),
			ByScore: true,
		}).Result()
		if err != nil {
			return nil, err
		}
		for _, eID := range eIDs {
			if !seen[eID] {
				seen[eID] = true
				bucketIDs = append(bucketIDs, eID)
			}
		}
	}
	return bucketIDs, nil
}

func RemoveCandidate(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent) error {
	asset := ev.AssetCode
	if asset == "" {
		asset = ev.Currency
	}

	for _, amount := range candidateBuckets(ev.AmountMinor) {
		key := candidateKey(ev.TenantID, asset, amount)
		_, err := client.ZRem(ctx, key, ev.EventID).Result()
		if err != nil {
			return err
		}
	}
	return nil
}
