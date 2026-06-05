package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tally/internal/bench"
	"tally/internal/loadgen"
)

type config struct {
	mode           string
	pairs          int
	workers        int
	seed           int64
	arrival        string
	decoyRatio     float64
	baseURL        string
	databaseURL    string
	pollInterval   time.Duration
	maxWait        time.Duration
	output         string
	resumeP99MS    float64
	steps          string
	resetBenchData bool
}

type postResult struct {
	EventID     string
	StatusCode  int
	Err         error
	StartedAt   time.Time
	CompletedAt time.Time
}

type pollResult struct {
	matches []bench.ObservedMatch
	err     error
}

func main() {
	cfg := parseFlags()
	ctx := context.Background()

	if cfg.mode != "correctness" {
		if cfg.mode != "load" {
			fmt.Fprintf(os.Stderr, "unsupported mode %q; expected correctness or load\n", cfg.mode)
			os.Exit(2)
		}
	}

	var err error
	if cfg.mode == "load" {
		err = runLoad(ctx, cfg)
	} else {
		err = runCorrectness(ctx, cfg)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	var pollIntervalMS int
	var maxWaitSec int
	flag.StringVar(&cfg.mode, "mode", "correctness", "correctness|load")
	flag.IntVar(&cfg.pairs, "pairs", 1000, "number of true ledger/processor pairs")
	flag.IntVar(&cfg.workers, "workers", 16, "fixed number of POST workers")
	flag.Int64Var(&cfg.seed, "seed", 42, "deterministic generation seed")
	flag.StringVar(&cfg.arrival, "arrival", string(loadgen.ArrivalModePaired), "paired|shuffled")
	flag.Float64Var(&cfg.decoyRatio, "decoy-ratio", 0.4, "decoy pairs per kind as ratio of true pairs")
	flag.StringVar(&cfg.baseURL, "base-url", "http://localhost:8080", "core HTTP base URL")
	flag.StringVar(&cfg.databaseURL, "database-url", "postgres://tally:tally@localhost:5432/tally", "Postgres URL")
	flag.IntVar(&pollIntervalMS, "poll-interval-ms", 10, "match polling interval in milliseconds")
	flag.IntVar(&maxWaitSec, "max-wait-sec", 30, "maximum match polling wait in seconds")
	flag.Float64Var(&cfg.resumeP99MS, "resume-p99-ms", 250, "resume-quality p99 latency threshold")
	flag.StringVar(&cfg.output, "output", "bench-results/latest.json", "JSON report path")
	flag.StringVar(&cfg.steps, "steps", "100,1000,5000,10000,25000", "load mode pair steps")
	flag.BoolVar(&cfg.resetBenchData, "reset", false, "delete bench-* tenant data before running")
	flag.Parse()

	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	cfg.pollInterval = time.Duration(pollIntervalMS) * time.Millisecond
	cfg.maxWait = time.Duration(maxWaitSec) * time.Second
	return cfg
}

func runCorrectness(ctx context.Context, cfg config) error {
	report, err := runBenchmark(ctx, cfg, cfg.resetBenchData)
	if err != nil {
		return err
	}

	printSummary(report)
	if err := bench.WriteJSONReport(cfg.output, report); err != nil {
		return err
	}

	if !report.Clean {
		return fmt.Errorf("correctness gate failed: match_rate=%.4f false_positives=%d missed=%d http_errors=%d", report.Correctness.MatchRate, report.Correctness.FalsePositiveMatches, report.Correctness.MissedMatches, report.HTTPErrors)
	}
	return nil
}

func runLoad(ctx context.Context, cfg config) error {
	steps, err := parseSteps(cfg.steps)
	if err != nil {
		return err
	}

	loadReport := bench.LoadReport{
		RunID:       fmt.Sprintf("load-%d-%d", cfg.seed, time.Now().UnixMilli()),
		Seed:        cfg.seed,
		Mode:        cfg.mode,
		Workers:     cfg.workers,
		ResumeP99Ms: cfg.resumeP99MS,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	var best *bench.Report
	resetNext := cfg.resetBenchData
	for _, step := range steps {
		stepCfg := cfg
		stepCfg.mode = "load"
		stepCfg.pairs = step

		report, err := runBenchmark(ctx, stepCfg, resetNext)
		resetNext = false
		if err != nil {
			return err
		}
		loadReport.Steps = append(loadReport.Steps, report)
		printSummary(report)

		if !isResumeUsable(report, cfg.resumeP99MS) {
			loadReport.StopReason = fmt.Sprintf("step %d was not resume-usable", step)
			break
		}
		if best == nil || isResumeBetter(*best, report, cfg.resumeP99MS) {
			reportCopy := report
			best = &reportCopy
			loadReport.BestRun = best
			continue
		}
		loadReport.StopReason = fmt.Sprintf("step %d was not resume-better than %d events", step, best.TotalEvents)
		break
	}

	if loadReport.StopReason == "" {
		loadReport.StopReason = "completed all configured steps"
	}

	if err := bench.WriteJSONReport(cfg.output, loadReport); err != nil {
		return err
	}
	if loadReport.BestRun == nil {
		return fmt.Errorf("no resume-usable run found")
	}
	fmt.Printf("best_resume_run events=%d throughput_events_sec=%.2f p99_ms=%.2f\n", loadReport.BestRun.TotalEvents, loadReport.BestRun.ThroughputEventsPerSecond, loadReport.BestRun.Latency.P99Millis)
	fmt.Printf("stop_reason=%s\n", loadReport.StopReason)
	return nil
}

func runBenchmark(ctx context.Context, cfg config, reset bool) (bench.Report, error) {
	runID := fmt.Sprintf("bench-%d-%d", cfg.seed, time.Now().UnixMilli())
	dataset, err := loadgen.Generate(loadgen.Config{
		TenantID:   runID,
		Seed:       cfg.seed,
		TruePairs:  cfg.pairs,
		DecoyRatio: cfg.decoyRatio,
		Arrival:    loadgen.ArrivalMode(cfg.arrival),
	})
	if err != nil {
		return bench.Report{}, err
	}

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return bench.Report{}, err
	}
	defer pool.Close()

	if reset {
		if err := resetBenchData(ctx, pool); err != nil {
			return bench.Report{}, err
		}
	}

	postDone := make(chan struct{})
	pollResults := make(chan pollResult, 1)
	go func() {
		matches, err := pollMatchesDuringRun(ctx, pool, runID, dataset.GroundTruth, cfg.pollInterval, cfg.maxWait, postDone)
		pollResults <- pollResult{matches: matches, err: err}
	}()

	startedAt := time.Now()
	postResults := postEvents(ctx, cfg.baseURL, dataset.Events, cfg.workers)
	duration := time.Since(startedAt)
	close(postDone)

	httpErrors := countPostErrors(postResults)
	polled := <-pollResults
	if polled.err != nil {
		return bench.Report{}, polled.err
	}
	observedMatches := polled.matches

	observedMatches = mergeObservedMatches(observedMatches, queryMatchesBestEffort(ctx, pool, runID))
	benchPostResults := make([]bench.PostResult, 0, len(postResults))
	for _, result := range postResults {
		if result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
			benchPostResults = append(benchPostResults, bench.PostResult{
				EventID:     result.EventID,
				CompletedAt: result.CompletedAt,
			})
		}
	}

	correctness := bench.ComputeCorrectness(dataset, observedMatches)
	latency := bench.ComputeLatencyMetrics(dataset.GroundTruth, observedMatches, benchPostResults)
	report := bench.NewReport(
		runID,
		cfg.seed,
		cfg.mode,
		cfg.arrival,
		cfg.workers,
		cfg.pairs,
		cfg.decoyRatio,
		len(dataset.Events),
		len(benchPostResults),
		httpErrors,
		duration,
		latency,
		correctness,
	)

	return report, nil
}

func postEvents(ctx context.Context, baseURL string, events []loadgen.GeneratedEvent, workers int) []postResult {
	if workers <= 0 {
		workers = 1
	}

	jobs := make(chan loadgen.GeneratedEvent)
	results := make(chan postResult, len(events))
	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range jobs {
				results <- postEvent(ctx, client, baseURL, event)
			}
		}()
	}

	for _, event := range events {
		jobs <- event
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]postResult, 0, len(events))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func postEvent(ctx context.Context, client *http.Client, baseURL string, event loadgen.GeneratedEvent) postResult {
	result := postResult{
		EventID:   event.EventID,
		StartedAt: time.Now(),
	}

	body, err := json.Marshal(event)
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

func pollMatches(ctx context.Context, pool *pgxpool.Pool, tenantID string, truth loadgen.GroundTruth, interval time.Duration, maxWait time.Duration) ([]bench.ObservedMatch, error) {
	postDone := make(chan struct{})
	close(postDone)
	return pollMatchesDuringRun(ctx, pool, tenantID, truth, interval, maxWait, postDone)
}

func pollMatchesDuringRun(ctx context.Context, pool *pgxpool.Pool, tenantID string, truth loadgen.GroundTruth, interval time.Duration, maxWait time.Duration, postDone <-chan struct{}) ([]bench.ObservedMatch, error) {
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}

	observed := make(map[string]bench.ObservedMatch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var deadline time.Time
	postingDone := false
	for {
		matches, err := queryMatches(ctx, pool, tenantID, time.Now())
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			key := loadgen.PairKey(match.EventAID, match.EventBID)
			if _, ok := observed[key]; !ok {
				observed[key] = match
			}
		}
		if observedExpectedCount(observed, truth) == len(truth.Expected) {
			return observedSlice(observed), nil
		}
		if postingDone && time.Now().After(deadline) {
			return observedSlice(observed), nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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

func queryMatchesBestEffort(ctx context.Context, pool *pgxpool.Pool, tenantID string) []bench.ObservedMatch {
	matches, err := queryMatches(ctx, pool, tenantID, time.Now())
	if err != nil {
		return nil
	}
	return matches
}

func queryMatches(ctx context.Context, pool *pgxpool.Pool, tenantID string, observedAt time.Time) ([]bench.ObservedMatch, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.match_id, me.event_id
		  FROM matches m
		  JOIN match_events me ON me.match_id = m.match_id
		 WHERE m.tenant_id = $1
		 ORDER BY m.match_id, me.event_id`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byMatch := make(map[string][]string)
	for rows.Next() {
		var matchID string
		var eventID string
		if err := rows.Scan(&matchID, &eventID); err != nil {
			return nil, err
		}
		byMatch[matchID] = append(byMatch[matchID], eventID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	matches := make([]bench.ObservedMatch, 0, len(byMatch))
	for _, ids := range byMatch {
		if len(ids) != 2 {
			continue
		}
		matches = append(matches, bench.ObservedMatch{
			EventAID:   ids[0],
			EventBID:   ids[1],
			ObservedAt: observedAt,
		})
	}
	return matches, nil
}

func mergeObservedMatches(primary []bench.ObservedMatch, secondary []bench.ObservedMatch) []bench.ObservedMatch {
	merged := make(map[string]bench.ObservedMatch)
	for _, match := range secondary {
		merged[loadgen.PairKey(match.EventAID, match.EventBID)] = match
	}
	for _, match := range primary {
		merged[loadgen.PairKey(match.EventAID, match.EventBID)] = match
	}
	return observedSlice(merged)
}

func observedExpectedCount(observed map[string]bench.ObservedMatch, truth loadgen.GroundTruth) int {
	count := 0
	for key := range observed {
		if _, ok := truth.Expected[key]; ok {
			count++
		}
	}
	return count
}

func observedSlice(matches map[string]bench.ObservedMatch) []bench.ObservedMatch {
	out := make([]bench.ObservedMatch, 0, len(matches))
	for _, match := range matches {
		out = append(out, match)
	}
	return out
}

func countPostErrors(results []postResult) int {
	count := 0
	for _, result := range results {
		if result.Err != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
			count++
		}
	}
	return count
}

func resetBenchData(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM match_events
		 WHERE match_id IN (SELECT match_id FROM matches WHERE tenant_id LIKE 'bench-%');
		DELETE FROM matches WHERE tenant_id LIKE 'bench-%';
		DELETE FROM canonical_events WHERE tenant_id LIKE 'bench-%';
	`)
	return err
}

func parseSteps(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	steps := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var step int
		if _, err := fmt.Sscanf(part, "%d", &step); err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", part, err)
		}
		if step <= 0 {
			return nil, fmt.Errorf("step must be positive: %d", step)
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one load step is required")
	}
	return steps, nil
}

func isResumeUsable(report bench.Report, resumeP99MS float64) bool {
	return report.Clean && report.Latency.P99Millis <= resumeP99MS
}

func isResumeBetter(best bench.Report, candidate bench.Report, resumeP99MS float64) bool {
	if !isResumeUsable(candidate, resumeP99MS) {
		return false
	}
	if candidate.TotalEvents <= best.TotalEvents {
		return false
	}
	if best.ThroughputEventsPerSecond == 0 {
		return true
	}
	return candidate.ThroughputEventsPerSecond >= best.ThroughputEventsPerSecond*0.75
}

func printSummary(report bench.Report) {
	fmt.Printf("run_id=%s\n", report.RunID)
	fmt.Printf("mode=%s arrival=%s workers=%d\n", report.Mode, report.Arrival, report.Workers)
	fmt.Printf("events_posted=%d/%d http_errors=%d duration_ms=%d throughput_events_sec=%.2f\n", report.EventsPosted, report.TotalEvents, report.HTTPErrors, report.DurationMillis, report.ThroughputEventsPerSecond)
	fmt.Printf("matches expected=%d confirmed=%d true=%d missed=%d false_positive=%d\n", report.Correctness.ExpectedMatches, report.Correctness.ConfirmedMatches, report.Correctness.ConfirmedTrueMatches, report.Correctness.MissedMatches, report.Correctness.FalsePositiveMatches)
	fmt.Printf("match_rate=%.4f false_positive_rate=%.4f latency_p50_ms=%.2f latency_p95_ms=%.2f latency_p99_ms=%.2f clean=%t\n", report.Correctness.MatchRate, report.Correctness.FalsePositiveRate, report.Latency.P50Millis, report.Latency.P95Millis, report.Latency.P99Millis, report.Clean)
}
