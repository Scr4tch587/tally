package main

import (
	"tally/internal/store"
	"context"
	"tally/internal/logger"
	"tally/internal/api"
	"net/http"
	"time"
	"tally/internal/event"
)

func main() {
	log := logger.New()
	log.Info().Msg("Tally Ready")
	ctx := context.Background()
	pool, err := store.Connect(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres connection failed")
		return
	} 
	defer pool.Close()

	err = pool.Ping(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres ping failed")
		return
	}

	log.Info().Msg("postgres ok")
	h := &api.Handler{Pool: pool, Log: log}
	r := api.NewRouter(h)
	log.Info().Msg("server listening on :8080")

	client := store.NewRedisClient()
	ev1, _ := event.NewCanonicalEvent("abc123", "stripe", 100, "USD", time.Now())
	ev2, _ := event.NewCanonicalEvent("abc124", "stripe", 100, "USD", time.Now())
	ev3, _ := event.NewCanonicalEvent("abc125", "stripe", 100, "USD", time.Now())
	err = store.AddCandidate(ctx, client, ev1)
	if err != nil {
		log.Fatal().Err(err).Msg("adding event failed")
		return
	}
	_ = store.AddCandidate(ctx, client, ev2)
	_ = store.AddCandidate(ctx, client, ev3)

	candidates, _ := store.FindCandidates(ctx, client, "USD", 100, 10000000)
	for _, id := range candidates {
		log.Info().Str("EventID", id).Msg("Candidate found")
	}

	_ = store.RemoveCandidate(ctx, client, ev1)

	candidates, _ = store.FindCandidates(ctx, client, "USD", 100, 10000000)
	for _, id := range candidates {
		log.Info().Str("EventID", id).Msg("Candidate found")
	}

	http.ListenAndServe(":8080", r)


}