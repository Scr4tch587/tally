package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tally/internal/bench"
	"tally/internal/benchrec"
	"tally/internal/loadgen"
)

type config struct {
	dataPath     string
	limit        int
	distractors  bool
	workers      int
	seed         int64
	shuffle      bool
	baseURL      string
	databaseURL  string
	pollInterval time.Duration
	maxWait      time.Duration
	output       string
	dumpMisses   int
	reset        bool
}

type postResult struct {
	EventID     string
	StatusCode  int
	Err         error
	CompletedAt time.Time
}

type stratum struct {
	Rule     string  `json:"rule"`
	Expected int     `json:"expected"`
	Matched  int     `json:"matched"`
	Missed   int     `json:"missed"`
	Rate     float64 `json:"match_rate"`
}

type report struct {
	RunID                     string               `json:"run_id"`
	GeneratedAt               string               `json:"generated_at"`
	Dataset                   string               `json:"dataset"`
	Scope                     string               `json:"scope"`
	Distractors               bool                 `json:"distractors_included"`
	Limit                     int                  `json:"pair_limit"`
	Shuffled                  bool                 `json:"shuffled_arrival"`
	Seed                      int64                `json:"seed"`
	Workers                   int                  `json:"workers"`
	Load                      benchrec.LoadStats   `json:"load_stats"`
	GroundTruthPairs          int                  `json:"ground_truth_pairs"`
	DistractorLegs            int                  `json:"distractor_legs"`
	TotalEvents               int                  `json:"total_events"`
	EventsPosted              int                  `json:"events_posted"`
	HTTPErrors                int                  `json:"http_errors"`
	DurationMillis            int64                `json:"duration_ms"`
	ThroughputEventsPerSecond float64              `json:"throughput_events_per_sec"`
	Latency                   bench.LatencyMetrics `json:"latency"`
	Correctness               bench.Correctness    `json:"correctness"`
	RelatedOutOfScope         int                  `json:"related_out_of_scope_matches"`
	Strata                    []stratum            `json:"strata"`
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "benchrec failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	var pollIntervalMS, maxWaitSec int
	flag.StringVar(&cfg.dataPath, "data", "data/benchrec/BenchRec_cash_v1.0_train.csv", "BenchRec CSV path")
	flag.IntVar(&cfg.limit, "limit", 500, "number of 1:1 ground-truth pairs to replay (0 = all)")
	flag.BoolVar(&cfg.distractors, "distractors", true, "also replay N:M and one-sided legs as ambient traffic")
	flag.IntVar(&cfg.workers, "workers", 16, "fixed number of POST workers")
	flag.Int64Var(&cfg.seed, "seed", 42, "seed for pair sampling and arrival shuffle")
	flag.BoolVar(&cfg.shuffle, "shuffle", true, "shuffle arrival order")
	flag.StringVar(&cfg.baseURL, "base-url", "http://localhost:8080", "core HTTP base URL")
	flag.StringVar(&cfg.databaseURL, "database-url", "postgres://tally:tally@localhost:5432/tally", "Postgres URL")
	flag.IntVar(&pollIntervalMS, "poll-interval-ms", 500, "match polling interval in milliseconds")
	flag.IntVar(&maxWaitSec, "max-wait-sec", 120, "maximum match polling wait after posting completes")
	flag.StringVar(&cfg.output, "output", "bench-results/benchrec-latest.json", "JSON report path")
	flag.IntVar(&cfg.dumpMisses, "dump-misses", 0, "write N missed pairs and false positives to a .misses.txt file")
	flag.BoolVar(&cfg.reset, "reset", false, "delete benchrec-* tenant data before running")
	flag.Parse()

	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	cfg.pollInterval = time.Duration(pollIntervalMS) * time.Millisecond
	cfg.maxWait = time.Duration(maxWaitSec) * time.Second
	return cfg
}

func run(ctx context.Context, cfg config) error {
	runID := fmt.Sprintf("benchrec-%d", time.Now().UnixMilli())

	fmt.Printf("loading %s\n", cfg.dataPath)
	dataset, err := benchrec.Load(cfg.dataPath, cfg.limit, cfg.seed)
	if err != nil {
		return err
	}

	events := dataset.Events(runID, cfg.distractors)
	if cfg.shuffle {
		rng := rand.New(rand.NewSource(cfg.seed))
		rng.Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })
	}

	truth := loadgen.GroundTruth{Expected: make(map[string]loadgen.ExpectedMatch, len(dataset.Pairs))}
	ruleByPairKey := make(map[string]string, len(dataset.Pairs))
	matchIDByEventID := make(map[string]string, len(events))
	legByEventID := make(map[string]benchrec.Leg, len(events))

	for _, pair := range dataset.Pairs {
		aID := pair.A.EventID(runID)
		bID := pair.B.EventID(runID)
		key := loadgen.PairKey(aID, bID)
		truth.Expected[key] = loadgen.ExpectedMatch{
			TruthID:  pair.MatchID,
			EventAID: aID,
			EventBID: bID,
			PairKey:  key,
		}
		ruleByPairKey[key] = pair.Rule()
	}
	for _, pair := range dataset.Pairs {
		legByEventID[pair.A.EventID(runID)] = pair.A
		legByEventID[pair.B.EventID(runID)] = pair.B
		matchIDByEventID[pair.A.EventID(runID)] = pair.MatchID
		matchIDByEventID[pair.B.EventID(runID)] = pair.MatchID
	}
	if cfg.distractors {
		for _, leg := range dataset.Distractors {
			legByEventID[leg.EventID(runID)] = leg
			matchIDByEventID[leg.EventID(runID)] = leg.MatchID
		}
	}

	fmt.Printf("run_id=%s pairs=%d distractor_legs=%d events=%d\n",
		runID, len(dataset.Pairs), len(dataset.Distractors), len(events))
	if dataset.Stats.SignDirectionConfl > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: %d legs disagree between amount sign and debitOrCredit\n", dataset.Stats.SignDirectionConfl)
	}
	if dataset.Stats.AmountParseFailures > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: %d legs dropped on amount parse failure\n", dataset.Stats.AmountParseFailures)
	}

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.reset {
		if err := resetData(ctx, pool); err != nil {
			return err
		}
	}

	postDone := make(chan struct{})
	polled := make(chan []bench.ObservedMatch, 1)
	go func() {
		polled <- pollMatches(ctx, pool, runID, truth, cfg.pollInterval, cfg.maxWait, postDone)
	}()

	startedAt := time.Now()
	postResults := postEvents(ctx, cfg.baseURL, events, cfg.workers)
	duration := time.Since(startedAt)
	close(postDone)

	observed := <-polled

	scored := make([]bench.ObservedMatch, 0, len(observed))
	related := 0
	for _, match := range observed {
		key := loadgen.PairKey(match.EventAID, match.EventBID)
		if _, ok := truth.Expected[key]; ok {
			scored = append(scored, match)
			continue
		}
		aMatchID := matchIDByEventID[match.EventAID]
		bMatchID := matchIDByEventID[match.EventBID]
		if aMatchID != "" && aMatchID == bMatchID {
			related++
			continue
		}
		scored = append(scored, match)
	}

	benchPosts := make([]bench.PostResult, 0, len(postResults))
	httpErrors := 0
	for _, result := range postResults {
		if result.Err != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
			httpErrors++
			continue
		}
		benchPosts = append(benchPosts, bench.PostResult{EventID: result.EventID, CompletedAt: result.CompletedAt})
	}

	correctness := bench.ComputeCorrectness(loadgen.Dataset{GroundTruth: truth}, scored)
	latency := bench.ComputeLatencyMetrics(truth, observed, benchPosts)

	scope := "1:1 subset only"
	if cfg.distractors {
		scope = "1:1 subset + N:M and one-sided legs as ambient traffic"
	}

	rep := report{
		RunID:                     runID,
		GeneratedAt:               time.Now().UTC().Format(time.RFC3339),
		Dataset:                   filepath.Base(cfg.dataPath),
		Scope:                     scope,
		Distractors:               cfg.distractors,
		Limit:                     cfg.limit,
		Shuffled:                  cfg.shuffle,
		Seed:                      cfg.seed,
		Workers:                   cfg.workers,
		Load:                      dataset.Stats,
		GroundTruthPairs:          len(dataset.Pairs),
		DistractorLegs:            len(dataset.Distractors),
		TotalEvents:               len(events),
		EventsPosted:              len(benchPosts),
		HTTPErrors:                httpErrors,
		DurationMillis:            duration.Milliseconds(),
		ThroughputEventsPerSecond: bench.ThroughputEventsPerSecond(len(benchPosts), duration),
		Latency:                   latency,
		Correctness:               correctness,
		RelatedOutOfScope:         related,
		Strata:                    computeStrata(truth, scored, ruleByPairKey),
	}

	printSummary(rep)
	if err := bench.WriteJSONReport(cfg.output, rep); err != nil {
		return err
	}
	fmt.Printf("report written to %s\n", cfg.output)

	if cfg.dumpMisses > 0 {
		path := strings.TrimSuffix(cfg.output, filepath.Ext(cfg.output)) + ".misses.txt"
		if err := dumpMisses(path, cfg.dumpMisses, runID, truth, scored, legByEventID); err != nil {
			return err
		}
		fmt.Printf("misses written to %s\n", path)
	}

	return nil
}

func computeStrata(truth loadgen.GroundTruth, scored []bench.ObservedMatch, ruleByPairKey map[string]string) []stratum {
	confirmed := make(map[string]bool, len(scored))
	for _, match := range scored {
		confirmed[loadgen.PairKey(match.EventAID, match.EventBID)] = true
	}

	expected := make(map[string]int)
	matched := make(map[string]int)
	for key := range truth.Expected {
		rule := ruleByPairKey[key]
		expected[rule]++
		if confirmed[key] {
			matched[rule]++
		}
	}

	strata := make([]stratum, 0, len(expected))
	for rule, total := range expected {
		s := stratum{Rule: rule, Expected: total, Matched: matched[rule], Missed: total - matched[rule]}
		if total > 0 {
			s.Rate = float64(matched[rule]) / float64(total)
		}
		strata = append(strata, s)
	}
	sort.Slice(strata, func(i, j int) bool { return strata[i].Expected > strata[j].Expected })
	return strata
}

func dumpMisses(path string, limit int, runID string, truth loadgen.GroundTruth, scored []bench.ObservedMatch, legByEventID map[string]benchrec.Leg) error {
	confirmed := make(map[string]bool, len(scored))
	for _, match := range scored {
		confirmed[loadgen.PairKey(match.EventAID, match.EventBID)] = true
	}

	var builder strings.Builder
	written := 0
	builder.WriteString("=== MISSED (true pair never confirmed) ===\n")
	for key, expected := range truth.Expected {
		if confirmed[key] || written >= limit {
			continue
		}
		written++
		builder.WriteString(describePair(legByEventID[expected.EventAID], legByEventID[expected.EventBID]))
	}

	written = 0
	builder.WriteString("\n=== FALSE POSITIVES (confirmed, not ground truth, not same matchId) ===\n")
	for _, match := range scored {
		key := loadgen.PairKey(match.EventAID, match.EventBID)
		if _, ok := truth.Expected[key]; ok || written >= limit {
			continue
		}
		written++
		builder.WriteString(describePair(legByEventID[match.EventAID], legByEventID[match.EventBID]))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func describePair(a, b benchrec.Leg) string {
	return fmt.Sprintf(
		"\nmatchId %s / %s  rule=%s\n  %s %s  amount=%d %s  date=%s\n     ref=%q\n     att=%q\n  %s %s  amount=%d %s  date=%s\n     ref=%q\n     att=%q\n",
		a.MatchID, b.MatchID, a.MatchRule,
		a.Side, a.SourceID, a.AmountMinor, a.Direction, a.ValueDate.Format("2006-01-02"), a.References, a.Attributes,
		b.Side, b.SourceID, b.AmountMinor, b.Direction, b.ValueDate.Format("2006-01-02"), b.References, b.Attributes,
	)
}

func postEvents(ctx context.Context, baseURL string, events []benchrec.Event, workers int) []postResult {
	if workers <= 0 {
		workers = 1
	}

	jobs := make(chan benchrec.Event)
	results := make(chan postResult, len(events))
	client := &http.Client{Timeout: 30 * time.Second}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ev := range jobs {
				results <- postEvent(ctx, client, baseURL, ev)
			}
		}()
	}

	go func() {
		for _, ev := range events {
			jobs <- ev
		}
		close(jobs)
	}()

	wg.Wait()
	close(results)

	out := make([]postResult, 0, len(events))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func postEvent(ctx context.Context, client *http.Client, baseURL string, ev benchrec.Event) postResult {
	result := postResult{EventID: ev.EventID}

	body, err := json.Marshal(ev)
	if err != nil {
		result.Err = err
		result.CompletedAt = time.Now()
		return result
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/events", bytes.NewReader(body))
	if err != nil {
		result.Err = err
		result.CompletedAt = time.Now()
		return result
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	result.CompletedAt = time.Now()
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Err = fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return result
}

func pollMatches(ctx context.Context, pool *pgxpool.Pool, tenantID string, truth loadgen.GroundTruth, interval, maxWait time.Duration, postDone <-chan struct{}) []bench.ObservedMatch {
	observed := make(map[string]bench.ObservedMatch)
	seenMatchID := make(map[string]bool)
	cursor := time.Time{}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var deadline time.Time
	postingDone := false

	for {
		next, err := queryMatchesSince(ctx, pool, tenantID, cursor, seenMatchID, observed)
		if err == nil && !next.IsZero() {
			cursor = next
		}

		if countExpected(observed, truth) == len(truth.Expected) && len(truth.Expected) > 0 {
			return collect(observed)
		}
		if postingDone && time.Now().After(deadline) {
			return collect(observed)
		}

		select {
		case <-ctx.Done():
			return collect(observed)
		case <-postDone:
			if !postingDone {
				postingDone = true
				deadline = time.Now().Add(maxWait)
				postDone = nil
			}
		case <-ticker.C:
		}
	}
}

func queryMatchesSince(ctx context.Context, pool *pgxpool.Pool, tenantID string, cursor time.Time, seen map[string]bool, observed map[string]bench.ObservedMatch) (time.Time, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.match_id, m.created_at, me.event_id
		  FROM matches m
		  JOIN match_events me ON me.match_id = m.match_id
		 WHERE m.tenant_id = $1 AND m.created_at >= $2
		 ORDER BY m.created_at, m.match_id`,
		tenantID, cursor)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()

	byMatch := make(map[string][]string)
	createdAt := make(map[string]time.Time)
	var maxCreated time.Time
	for rows.Next() {
		var matchID, eventID string
		var created time.Time
		if err := rows.Scan(&matchID, &created, &eventID); err != nil {
			return time.Time{}, err
		}
		byMatch[matchID] = append(byMatch[matchID], eventID)
		createdAt[matchID] = created
		if created.After(maxCreated) {
			maxCreated = created
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, err
	}

	now := time.Now()
	for matchID, ids := range byMatch {
		if seen[matchID] || len(ids) != 2 {
			continue
		}
		seen[matchID] = true
		key := loadgen.PairKey(ids[0], ids[1])
		if _, ok := observed[key]; !ok {
			observed[key] = bench.ObservedMatch{EventAID: ids[0], EventBID: ids[1], ObservedAt: now}
		}
	}

	return maxCreated, nil
}

func countExpected(observed map[string]bench.ObservedMatch, truth loadgen.GroundTruth) int {
	count := 0
	for key := range observed {
		if _, ok := truth.Expected[key]; ok {
			count++
		}
	}
	return count
}

func collect(observed map[string]bench.ObservedMatch) []bench.ObservedMatch {
	out := make([]bench.ObservedMatch, 0, len(observed))
	for _, match := range observed {
		out = append(out, match)
	}
	return out
}

func resetData(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM match_events
		 WHERE match_id IN (SELECT match_id FROM matches WHERE tenant_id LIKE 'benchrec-%');
		DELETE FROM matches WHERE tenant_id LIKE 'benchrec-%';
		DELETE FROM canonical_events WHERE tenant_id LIKE 'benchrec-%';
	`)
	return err
}

func printSummary(rep report) {
	fmt.Printf("\nrun_id=%s scope=%q\n", rep.RunID, rep.Scope)
	fmt.Printf("events_posted=%d/%d http_errors=%d duration_ms=%d throughput_events_sec=%.2f\n",
		rep.EventsPosted, rep.TotalEvents, rep.HTTPErrors, rep.DurationMillis, rep.ThroughputEventsPerSecond)
	fmt.Printf("matches expected=%d confirmed=%d true=%d missed=%d false_positive=%d related_out_of_scope=%d\n",
		rep.Correctness.ExpectedMatches, rep.Correctness.ConfirmedMatches, rep.Correctness.ConfirmedTrueMatches,
		rep.Correctness.MissedMatches, rep.Correctness.FalsePositiveMatches, rep.RelatedOutOfScope)
	fmt.Printf("match_rate=%.4f false_positive_rate=%.4f p50_ms=%.2f p95_ms=%.2f p99_ms=%.2f\n",
		rep.Correctness.MatchRate, rep.Correctness.FalsePositiveRate,
		rep.Latency.P50Millis, rep.Latency.P95Millis, rep.Latency.P99Millis)
	fmt.Println("per-stratum match rate:")
	for _, s := range rep.Strata {
		fmt.Printf("  %-12s %6d/%-6d  %.4f\n", s.Rule, s.Matched, s.Expected, s.Rate)
	}
}
