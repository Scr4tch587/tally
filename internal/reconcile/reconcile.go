package reconcile

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"sort"
	"strings"
	"tally/internal/event"
	"tally/internal/match"
	"tally/internal/store"
)

type rankedCandidate struct {
	Event    *event.CanonicalEvent
	Score    float64
	Evidence map[string]any
}

type Engine struct {
	Pool   *pgxpool.Pool
	Log    zerolog.Logger
	Client *redis.Client
}

func NewEngine(pool *pgxpool.Pool, log zerolog.Logger, client *redis.Client) *Engine {
	return &Engine{pool, log, client}
}

func (e *Engine) ReconcilePendingEvent(ctx context.Context, eventID string) error {
	ev, err := store.GetEvent(ctx, e.Pool, eventID)
	if err != nil {
		e.Log.Error().Err(err).Msg("Fetching event failed")
		return err
	}

	candidates, err := store.FindCandidates(ctx, e.Client, ev, 120000)
	if err != nil {
		e.Log.Error().Err(err).Msg("Finding candidates failed")
		return err
	}
	e.Log.Info().Int("candidate_count", len(candidates)).Msg("candidates found")

	rankedCandidates := make([]rankedCandidate, 0, len(candidates))

	for _, id := range candidates {
		e.Log.Info().Str("candidate_id", id).Str("current_event", ev.EventID).Msg("candidate found")
		if id == ev.EventID {
			continue
		}

		candidateEv, err := store.GetEvent(ctx, e.Pool, id)
		if err != nil {
			e.Log.Error().Err(err).Msg("Fetching event failed")
			return err
		}

		if candidateEv.SourceType == ev.SourceType {
			continue
		}

		score, evidence, ok := match.Score(ev, candidateEv)
		if !ok {
			continue
		}

		rankedCandidates = append(rankedCandidates, rankedCandidate{
			Event:    candidateEv,
			Score:    score,
			Evidence: evidence,
		})
	}

	sort.Slice(rankedCandidates, func(i, j int) bool {
		return rankedCandidates[i].Score > rankedCandidates[j].Score
	})

	for _, candidate := range rankedCandidates {
		err = store.ConfirmMatch(ctx, e.Pool, ev.EventID, candidate.Event.EventID, candidate.Score, candidate.Evidence)
		if err != nil {
			e.Log.Info().Err(err).Str("candidate_id", candidate.Event.EventID).Msg("confirming ranked candidate failed")
			if strings.Contains(err.Error(), "is not pending") {
				continue
			}
			return err
		}
		err = store.RemoveCandidate(ctx, e.Client, ev)
		if err != nil {
			e.Log.Error().Err(err).Msg("Removing candidate failed")
			return err
		}
		err = store.RemoveCandidate(ctx, e.Client, candidate.Event)
		if err != nil {
			e.Log.Error().Err(err).Msg("Removing candidate failed")
			return err
		}
		break
	}

	return nil
}
