// Command lookupbench measures what candidate narrowing buys.
//
// It loads the BenchRec corpus into Postgres as PENDING events (no server, no
// worker, so nothing is matched), then for a sample of events times the
// candidate lookup plus scoring two ways:
//
//   - narrowed: store.FindPendingMatchCandidates, the query the engine runs
//     (same amount within one minor unit, inside the time window)
//   - full:     the same query with the amount and time predicates removed, so
//     every pending event from the other source is fetched and scored
//
// The "full" side is a hypothetical baseline. Tally never shipped without
// narrowing; this is a comparison against scoring the whole backlog, not a
// before/after of a change.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tally/internal/bench"
	"tally/internal/benchrec"
	"tally/internal/event"
	"tally/internal/match"
	"tally/internal/store"
)

const windowMillis = 120000

type config struct {
	dataPath    string
	sample      int
	seed        int64
	databaseURL string
	output      string
	loaders     int
	keep        bool
}

type sideStats struct {
	CandidatesMean   float64 `json:"candidates_mean"`
	CandidatesMedian float64 `json:"candidates_median"`
	CandidatesP95    float64 `json:"candidates_p95"`
	CandidatesMax    float64 `json:"candidates_max"`
	MillisMean       float64 `json:"ms_mean"`
	MillisMedian     float64 `json:"ms_median"`
	MillisP95        float64 `json:"ms_p95"`
}

type report struct {
	RunID           string    `json:"run_id"`
	GeneratedAt     string    `json:"generated_at"`
	Dataset         string    `json:"dataset"`
	PendingEvents   int       `json:"pending_events"`
	Sample          int       `json:"sample"`
	Seed            int64     `json:"seed"`
	WindowMillis    int       `json:"window_ms"`
	Narrowed        sideStats `json:"narrowed"`
	Full            sideStats `json:"full"`
	WorkRatioMean   float64   `json:"candidates_scored_ratio_mean"`
	SpeedupMedian   float64   `json:"speedup_median"`
	SpeedupMean     float64   `json:"speedup_mean"`
	SameOutcome     int       `json:"same_outcome"`
	SameOutcomeRate float64   `json:"same_outcome_rate"`
	Note            string    `json:"note"`
}

type outcome struct {
	scored    int
	bestScore float64
	matched   bool
}

func main() {
	var cfg config
	flag.StringVar(&cfg.dataPath, "data", "data/benchrec/BenchRec_cash_v1.0_train.csv", "BenchRec CSV path")
	flag.IntVar(&cfg.sample, "sample", 1000, "number of events to time")
	flag.Int64Var(&cfg.seed, "seed", 42, "seed for the sample")
	flag.StringVar(&cfg.databaseURL, "database-url", "postgres://tally:tally@localhost:5432/tally", "Postgres URL")
	flag.StringVar(&cfg.output, "output", "bench-results/lookup-narrowing.json", "JSON report path")
	flag.IntVar(&cfg.loaders, "loaders", 16, "concurrent insert workers for the load step")
	flag.BoolVar(&cfg.keep, "keep", false, "leave the loaded events in Postgres afterwards")
	flag.Parse()

	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lookupbench failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config) error {
	runID := fmt.Sprintf("lookupbench-%d", time.Now().UnixMilli())

	fmt.Printf("loading %s\n", cfg.dataPath)
	dataset, err := benchrec.Load(cfg.dataPath, 0, cfg.seed)
	if err != nil {
		return err
	}
	raw := dataset.Events(runID, true)
	events := make([]*event.CanonicalEvent, 0, len(raw))
	for _, r := range raw {
		ev, err := event.NewCanonicalEvent(r.TenantID, r.EventID, r.SourceType, r.SourceEventID, r.AmountMinor,
			r.AssetCode, r.Currency, r.Timestamp, r.Direction, r.AccountRef, r.CounterpartyRef, r.Metadata)
		if err != nil {
			return fmt.Errorf("event %s: %w", r.EventID, err)
		}
		events = append(events, ev)
	}

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := reset(ctx, pool); err != nil {
		return err
	}
	if !cfg.keep {
		defer func() {
			if err := reset(context.Background(), pool); err != nil {
				fmt.Fprintf(os.Stderr, "cleanup failed: %v\n", err)
			}
		}()
	}

	fmt.Printf("run_id=%s inserting %d pending events\n", runID, len(events))
	loadStart := time.Now()
	if err := load(ctx, pool, events, cfg.loaders); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "ANALYZE canonical_events"); err != nil {
		return err
	}
	fmt.Printf("loaded in %s\n", time.Since(loadStart).Round(time.Millisecond))

	rng := rand.New(rand.NewSource(cfg.seed))
	order := rng.Perm(len(events))
	const warmup = 20
	if cfg.sample+warmup > len(order) {
		cfg.sample = len(order) - warmup
	}

	for _, idx := range order[:warmup] {
		if _, err := narrowed(ctx, pool, events[idx]); err != nil {
			return err
		}
		if _, err := full(ctx, pool, events[idx]); err != nil {
			return err
		}
	}

	var nCands, fCands, nMillis, fMillis []float64
	same := 0
	for i, idx := range order[warmup : warmup+cfg.sample] {
		ev := events[idx]

		start := time.Now()
		n, err := narrowed(ctx, pool, ev)
		if err != nil {
			return err
		}
		nElapsed := time.Since(start)

		start = time.Now()
		f, err := full(ctx, pool, ev)
		if err != nil {
			return err
		}
		fElapsed := time.Since(start)

		nCands = append(nCands, float64(n.scored))
		fCands = append(fCands, float64(f.scored))
		nMillis = append(nMillis, float64(nElapsed.Microseconds())/1000)
		fMillis = append(fMillis, float64(fElapsed.Microseconds())/1000)
		if n.matched == f.matched && (!n.matched || n.bestScore == f.bestScore) {
			same++
		}

		if (i+1)%100 == 0 {
			fmt.Printf("  %d/%d timed\n", i+1, cfg.sample)
		}
	}

	rep := report{
		RunID:         runID,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Dataset:       "BenchRec_cash_v1.0_train.csv",
		PendingEvents: len(events),
		Sample:        cfg.sample,
		Seed:          cfg.seed,
		WindowMillis:  windowMillis,
		Narrowed:      summarize(nCands, nMillis),
		Full:          summarize(fCands, fMillis),
		SameOutcome:   same,
		Note:          "full = hypothetical baseline that fetches and scores every pending event from the other source; never shipped",
	}
	if rep.Narrowed.CandidatesMean > 0 {
		rep.WorkRatioMean = rep.Full.CandidatesMean / rep.Narrowed.CandidatesMean
	}
	if rep.Narrowed.MillisMedian > 0 {
		rep.SpeedupMedian = rep.Full.MillisMedian / rep.Narrowed.MillisMedian
	}
	if rep.Narrowed.MillisMean > 0 {
		rep.SpeedupMean = rep.Full.MillisMean / rep.Narrowed.MillisMean
	}
	if cfg.sample > 0 {
		rep.SameOutcomeRate = float64(same) / float64(cfg.sample)
	}

	fmt.Printf("\npending=%d sample=%d\n", rep.PendingEvents, rep.Sample)
	fmt.Printf("narrowed: candidates mean=%.2f median=%.0f p95=%.0f max=%.0f | ms median=%.3f mean=%.3f p95=%.3f\n",
		rep.Narrowed.CandidatesMean, rep.Narrowed.CandidatesMedian, rep.Narrowed.CandidatesP95, rep.Narrowed.CandidatesMax,
		rep.Narrowed.MillisMedian, rep.Narrowed.MillisMean, rep.Narrowed.MillisP95)
	fmt.Printf("full:     candidates mean=%.2f median=%.0f p95=%.0f max=%.0f | ms median=%.3f mean=%.3f p95=%.3f\n",
		rep.Full.CandidatesMean, rep.Full.CandidatesMedian, rep.Full.CandidatesP95, rep.Full.CandidatesMax,
		rep.Full.MillisMedian, rep.Full.MillisMean, rep.Full.MillisP95)
	fmt.Printf("candidates scored ratio (mean)=%.0fx speedup median=%.0fx mean=%.0fx same outcome=%d/%d\n",
		rep.WorkRatioMean, rep.SpeedupMedian, rep.SpeedupMean, same, cfg.sample)

	if err := bench.WriteJSONReport(cfg.output, rep); err != nil {
		return err
	}
	fmt.Printf("report written to %s\n", cfg.output)
	return nil
}

func load(ctx context.Context, pool *pgxpool.Pool, events []*event.CanonicalEvent, workers int) error {
	if workers <= 0 {
		workers = 1
	}
	jobs := make(chan *event.CanonicalEvent)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ev := range jobs {
				if _, err := store.InsertEvent(ctx, pool, ev); err != nil {
					select {
					case errs <- err:
					default:
					}
				}
			}
		}()
	}
	for _, ev := range events {
		jobs <- ev
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

// narrowed runs the engine's real candidate query and scores what it returns.
func narrowed(ctx context.Context, pool *pgxpool.Pool, ev *event.CanonicalEvent) (outcome, error) {
	candidates, err := store.FindPendingMatchCandidates(ctx, pool, ev, windowMillis)
	if err != nil {
		return outcome{}, err
	}
	return scoreAll(ev, candidates), nil
}

// full fetches every pending event from the other source, with the same
// columns and the same tenant, status, source and asset predicates as the
// engine's query, and scores all of them.
func full(ctx context.Context, pool *pgxpool.Pool, ev *event.CanonicalEvent) (outcome, error) {
	asset := ev.AssetCode
	if asset == "" {
		asset = ev.Currency
	}
	rows, err := pool.Query(ctx,
		`SELECT tenant_id, event_id, source_type, source_event_id, amount_minor, currency, asset_code,
		        event_timestamp, ingested_at, direction, account_ref, counterparty_ref, metadata, idempotency_key
		   FROM canonical_events
		  WHERE tenant_id = $1
		    AND event_id <> $2
		    AND match_status = 'PENDING'
		    AND source_type <> $3
		    AND COALESCE(NULLIF(asset_code, ''), currency) = $4`,
		ev.TenantID, ev.EventID, ev.SourceType, asset)
	if err != nil {
		return outcome{}, err
	}
	defer rows.Close()

	candidates := make([]*event.CanonicalEvent, 0, 1024)
	for rows.Next() {
		var c event.CanonicalEvent
		if err := rows.Scan(&c.TenantID, &c.EventID, &c.SourceType, &c.SourceEventID, &c.AmountMinor, &c.Currency,
			&c.AssetCode, &c.Timestamp, &c.IngestedAt, &c.Direction, &c.AccountRef, &c.CounterpartyRef,
			&c.Metadata, &c.IdempotencyKey); err != nil {
			return outcome{}, err
		}
		candidates = append(candidates, &c)
	}
	if err := rows.Err(); err != nil {
		return outcome{}, err
	}
	return scoreAll(ev, candidates), nil
}

func scoreAll(ev *event.CanonicalEvent, candidates []*event.CanonicalEvent) outcome {
	var out outcome
	for _, c := range candidates {
		if c.SourceType == ev.SourceType {
			continue
		}
		out.scored++
		score, _, ok := match.Score(ev, c)
		if ok && (!out.matched || score > out.bestScore) {
			out.matched = true
			out.bestScore = score
		}
	}
	return out
}

func summarize(candidates, millis []float64) sideStats {
	return sideStats{
		CandidatesMean:   mean(candidates),
		CandidatesMedian: percentile(candidates, 0.50),
		CandidatesP95:    percentile(candidates, 0.95),
		CandidatesMax:    percentile(candidates, 1.0),
		MillisMean:       mean(millis),
		MillisMedian:     percentile(millis, 0.50),
		MillisP95:        percentile(millis, 0.95),
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(p*float64(len(sorted)-1) + 0.5)
	return sorted[idx]
}

func reset(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `DELETE FROM canonical_events WHERE tenant_id LIKE 'lookupbench-%'`)
	return err
}
