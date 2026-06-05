package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewReportComputesDerivedFields(t *testing.T) {
	report := NewReport(
		"run-1",
		42,
		"correctness",
		"paired",
		16,
		100,
		0.4,
		280,
		280,
		0,
		2*time.Second,
		LatencyMetrics{P50Millis: 10, P95Millis: 20, P99Millis: 30},
		Correctness{MatchRate: 1, FalsePositiveMatches: 0, MissedMatches: 0},
	)

	if report.DurationMillis != 2000 {
		t.Fatalf("expected duration 2000ms, got %d", report.DurationMillis)
	}
	if report.ThroughputEventsPerSecond != 140 {
		t.Fatalf("expected throughput 140, got %f", report.ThroughputEventsPerSecond)
	}
	if !report.Clean {
		t.Fatalf("expected report to be clean")
	}
}

func TestNewReportMarksUncleanRuns(t *testing.T) {
	report := NewReport(
		"run-1",
		42,
		"correctness",
		"paired",
		16,
		100,
		0.4,
		280,
		280,
		1,
		time.Second,
		LatencyMetrics{},
		Correctness{MatchRate: 1, FalsePositiveMatches: 0, MissedMatches: 0},
	)

	if report.Clean {
		t.Fatalf("expected HTTP errors to make report unclean")
	}
}

func TestWriteJSONReportCreatesParentAndWritesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench-results", "latest.json")
	report := NewReport(
		"run-1",
		42,
		"correctness",
		"paired",
		16,
		100,
		0.4,
		280,
		280,
		0,
		time.Second,
		LatencyMetrics{P99Millis: 30},
		Correctness{MatchRate: 1},
	)

	if err := WriteJSONReport(path, report); err != nil {
		t.Fatalf("write JSON report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JSON report: %v", err)
	}

	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal JSON report: %v", err)
	}

	if got.RunID != "run-1" {
		t.Fatalf("expected run ID run-1, got %q", got.RunID)
	}
	if got.Latency.P99Millis != 30 {
		t.Fatalf("expected p99 30, got %f", got.Latency.P99Millis)
	}
}
