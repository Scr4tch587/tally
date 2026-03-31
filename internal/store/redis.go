package store

import (
	"github.com/redis/go-redis/v9"
	"time"
	"context"
	"fmt"
	"tally/internal/event"
)

func NewRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:6379"}) 
}

func AddCandidate(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent) error {
	_, err := client.ZAdd(ctx, fmt.Sprintf("candidates:%s:%d", ev.Currency, ev.AmountMinor), redis.Z{Score: float64(time.Now().Unix()), Member: ev.EventID}).Result()
	return err
}

func FindCandidates(ctx context.Context, client *redis.Client, currency string, amount int64, maxAgeSec int64) ([]string, error) {
	eIDs, err :=   client.ZRangeArgs(ctx, redis.ZRangeArgs{                                                                                                     
      Key:     fmt.Sprintf("candidates:%s:%d", currency, amount),                                                                                
      Start:   fmt.Sprintf("%f", float64(time.Now().Unix()-maxAgeSec)),                                                                          
      Stop:    fmt.Sprintf("%f", float64(time.Now().Unix())),                                                                                    
      ByScore: true,                                                                                                                             
  	}).Result() 
	if err != nil {
		return nil, err
	}
	return eIDs, nil
}

func RemoveCandidate(ctx context.Context, client *redis.Client, ev *event.CanonicalEvent) error {
	_, err := client.ZRem(ctx, fmt.Sprintf("candidates:%s:%d", ev.Currency, ev.AmountMinor), ev.EventID).Result()
	return err
}