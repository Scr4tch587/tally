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

func (e *Engine) ReconcilePendingBatch(ctx context.Context, cursor store.PendingCursor, limit int) (store.PendingCursor, int, error) {
	span := sentry.StartSpan(ctx, "reconcile.pending_batch", sentry.WithTransactionName("reconcile.pending_worker"))
	defer span.Finish()
	ctx = span.Context()

	eventIDs, next, err := store.FindPendingEventsAfter(ctx, e.Pool, cursor, limit)
	if err != nil {
		e.Log.Error().Err(err).Msg("Finding pending events failed")
		return cursor, 0, err
	}

	for _, eventID := range eventIDs {
		if err := e.ReconcilePendingEvent(ctx, eventID); err != nil {
			e.Log.Info().Err(err).Str("event_id", eventID).Msg("Reconciling pending event failed")
			continue
		}
	}

	return next, len(eventIDs), nil
}

func (e *Engine) ReconcileRecentPending(ctx context.Context, limit int) error {
	_, _, err := e.ReconcilePendingBatch(ctx, store.PendingCursor{}, limit)
	return err
}

func StartPendingWorker(ctx context.Context, engine *Engine, interval time.Duration, limit int) {
	heartbeat := observe.NewCronHeartbeat("pending-reconcile-worker")
	cursor := store.PendingCursor{}
	run := func() {
		defer observe.CapturePanic()
		heartbeat.Beat()
		next, scanned, err := engine.ReconcilePendingBatch(ctx, cursor, limit)
		if err != nil {
			engine.Log.Error().Err(err).Msg("pending reconciliation worker failed")
			return
		}
		if scanned < limit {
			cursor = store.PendingCursor{}
			return
		}
		cursor = next
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
