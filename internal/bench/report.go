package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Report struct {
	RunID                     string         `json:"run_id"`
	Seed                      int64          `json:"seed"`
	Mode                      string         `json:"mode"`
	Arrival                   string         `json:"arrival"`
	Workers                   int            `json:"workers"`
	TruePairs                 int            `json:"true_pairs"`
	DecoyRatio                float64        `json:"decoy_ratio"`
	TotalEvents               int            `json:"total_events"`
	EventsPosted              int            `json:"events_posted"`
	HTTPErrors                int            `json:"http_errors"`
	DurationMillis            int64          `json:"duration_ms"`
	ThroughputEventsPerSecond float64        `json:"throughput_events_per_sec"`
	Latency                   LatencyMetrics `json:"latency"`
	Correctness               Correctness    `json:"correctness"`
	Clean                     bool           `json:"clean"`
}

type LoadReport struct {
	RunID       string   `json:"run_id"`
	Seed        int64    `json:"seed"`
	Mode        string   `json:"mode"`
	Workers     int      `json:"workers"`
	Steps       []Report `json:"steps"`
	BestRun     *Report  `json:"best_run,omitempty"`
	StopReason  string   `json:"stop_reason"`
	ResumeP99Ms float64  `json:"resume_p99_ms"`
	GeneratedAt string   `json:"generated_at"`
}

func NewReport(
	runID string,
	seed int64,
	mode string,
	arrival string,
	workers int,
	truePairs int,
	decoyRatio float64,
	totalEvents int,
	eventsPosted int,
	httpErrors int,
	duration time.Duration,
	latency LatencyMetrics,
	correctness Correctness,
) Report {
	return Report{
		RunID:                     runID,
		Seed:                      seed,
		Mode:                      mode,
		Arrival:                   arrival,
		Workers:                   workers,
		TruePairs:                 truePairs,
		DecoyRatio:                decoyRatio,
		TotalEvents:               totalEvents,
		EventsPosted:              eventsPosted,
		HTTPErrors:                httpErrors,
		DurationMillis:            duration.Milliseconds(),
		ThroughputEventsPerSecond: ThroughputEventsPerSecond(eventsPosted, duration),
		Latency:                   latency,
		Correctness:               correctness,
		Clean:                     httpErrors == 0 && correctness.MatchRate == 1 && correctness.FalsePositiveMatches == 0 && correctness.MissedMatches == 0,
	}
}

func WriteJSONReport(path string, report any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return os.WriteFile(path, data, 0o644)
}
