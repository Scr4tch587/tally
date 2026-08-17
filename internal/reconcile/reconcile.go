package reconcile

import (
	"context"
	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"sort"
	"strings"
	"tally/internal/event"
	"tally/internal/match"
	"tally/internal/observe"
	"tally/internal/store"
	"time"
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
	span := sentry.StartSpan(ctx, "reconcile.pending_event")
	defer span.Finish()
	ctx = span.Context()

	getSpan := sentry.StartSpan(ctx, "db.get_event")
	ev, err := store.GetEvent(ctx, e.Pool, eventID)
	getSpan.Finish()
	if err != nil {
		e.Log.Error().Err(err).Msg("Fetching event failed")
		return err
	}

	candidateSpan := sentry.StartSpan(ctx, "db.find_pending_candidates")
	postgresCandidates, err := store.FindPendingMatchCandidates(ctx, e.Pool, ev, 120000)
	candidateSpan.Finish()
	if err != nil {
		e.Log.Error().Err(err).Msg("Finding postgres match candidates failed")
		return err
	}

	seenIDs := map[string]bool{}
	e.Log.Info().Int("postgres_candidate_count", len(postgresCandidates)).Msg("postgres candidates found")
	rankedCandidates := make([]rankedCandidate, 0, len(postgresCandidates))

	scoreSpan := sentry.StartSpan(ctx, "reconcile.score_candidates")
	for _, candidateEv := range postgresCandidates {
		if seenIDs[candidateEv.EventID] {
			continue
		}
		seenIDs[candidateEv.EventID] = true

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
	scoreSpan.Finish()

	matched := false
	for _, candidate := range rankedCandidates {
		confirmSpan := sentry.StartSpan(ctx, "db.confirm_match")
		err = store.ConfirmMatch(ctx, e.Pool, ev.EventID, candidate.Event.EventID, candidate.Score, candidate.Evidence)
		confirmSpan.Finish()
		if err != nil {
			e.Log.Info().Err(err).Str("candidate_id", candidate.Event.EventID).Msg("confirming ranked candidate failed")
			if strings.Contains(err.Error(), "is not pending") {
				continue
			}
			return err
		}

		matched = true
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

	if !matched {
		err = store.AddCandidate(ctx, e.Client, ev)
		if err != nil {
			e.Log.Error().Err(err).Msg("Adding unmatched pending event failed")
			return err
		}
	}

	return nil
}

func (e *Engine) ReconcileRecentPending(ctx context.Context, limit int) error {
	span := sentry.StartSpan(ctx, "reconcile.recent_pending", sentry.WithTransactionName("reconcile.pending_worker"))
	defer span.Finish()
	ctx = span.Context()

	eventIDs, err := store.FindRecentPendingEvents(ctx, e.Pool, limit)
	if err != nil {
		e.Log.Error().Err(err).Msg("Finding recent pending events failed")
		return err
	}

	for _, eventID := range eventIDs {
		err = e.ReconcilePendingEvent(ctx, eventID)
		if err != nil {
			e.Log.Info().Err(err).Str("event_id", eventID).Msg("Reconciling pending event failed")
			continue
		}
	}

	return nil
}

func StartPendingWorker(ctx context.Context, engine *Engine, interval time.Duration, limit int) {
	heartbeat := observe.NewCronHeartbeat("pending-reconcile-worker")
	run := func() {
		defer observe.CapturePanic()
		heartbeat.Beat()
		if err := engine.ReconcileRecentPending(ctx, limit); err != nil {
			engine.Log.Error().Err(err).Msg("pending reconciliation worker failed")
		}
	}

	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
