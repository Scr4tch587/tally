package bench

import (
	"reflect"
	"testing"
	"time"

	"tally/internal/loadgen"
)

func TestComputeCorrectnessAllExpectedObserved(t *testing.T) {
	dataset := correctnessDataset("a", "b", "c", "d")

	got := ComputeCorrectness(dataset, []ObservedMatch{
		{EventAID: "a", EventBID: "b"},
		{EventAID: "c", EventBID: "d"},
	})

	if got.ExpectedMatches != 2 {
		t.Fatalf("expected 2 expected matches, got %d", got.ExpectedMatches)
	}
	if got.ConfirmedMatches != 2 {
		t.Fatalf("expected 2 confirmed matches, got %d", got.ConfirmedMatches)
	}
	if got.ConfirmedTrueMatches != 2 {
		t.Fatalf("expected 2 confirmed true matches, got %d", got.ConfirmedTrueMatches)
	}
	if got.FalsePositiveMatches != 0 {
		t.Fatalf("expected 0 false positives, got %d", got.FalsePositiveMatches)
	}
	if got.MissedMatches != 0 {
		t.Fatalf("expected 0 missed matches, got %d", got.MissedMatches)
	}
	if got.MatchRate != 1.0 {
		t.Fatalf("expected match rate 1.0, got %f", got.MatchRate)
	}
	if got.FalsePositiveRate != 0 {
		t.Fatalf("expected false positive rate 0, got %f", got.FalsePositiveRate)
	}
}

func TestComputeCorrectnessCountsMissedMatches(t *testing.T) {
	dataset := correctnessDataset("a", "b", "c", "d")

	got := ComputeCorrectness(dataset, []ObservedMatch{
		{EventAID: "a", EventBID: "b"},
	})

	if got.ConfirmedTrueMatches != 1 {
		t.Fatalf("expected 1 confirmed true match, got %d", got.ConfirmedTrueMatches)
	}
	if got.MissedMatches != 1 {
		t.Fatalf("expected 1 missed match, got %d", got.MissedMatches)
	}
	if got.MatchRate != 0.5 {
		t.Fatalf("expected match rate 0.5, got %f", got.MatchRate)
	}
}

func TestComputeCorrectnessCountsFalsePositiveMatches(t *testing.T) {
	dataset := correctnessDataset("a", "b")

	got := ComputeCorrectness(dataset, []ObservedMatch{
		{EventAID: "a", EventBID: "b"},
		{EventAID: "x", EventBID: "y"},
	})

	if got.ConfirmedMatches != 2 {
		t.Fatalf("expected 2 confirmed matches, got %d", got.ConfirmedMatches)
	}
	if got.ConfirmedTrueMatches != 1 {
		t.Fatalf("expected 1 confirmed true match, got %d", got.ConfirmedTrueMatches)
	}
	if got.FalsePositiveMatches != 1 {
		t.Fatalf("expected 1 false positive, got %d", got.FalsePositiveMatches)
	}
	if got.FalsePositiveRate != 0.5 {
		t.Fatalf("expected false positive rate 0.5, got %f", got.FalsePositiveRate)
	}
}

func TestComputeCorrectnessUsesOrderIndependentPairKeys(t *testing.T) {
	dataset := correctnessDataset("a", "b")

	got := ComputeCorrectness(dataset, []ObservedMatch{
		{EventAID: "b", EventBID: "a"},
	})

	if got.ConfirmedTrueMatches != 1 {
		t.Fatalf("expected reversed observed pair to count as true match, got %d", got.ConfirmedTrueMatches)
	}
	if got.FalsePositiveMatches != 0 {
		t.Fatalf("expected 0 false positives, got %d", got.FalsePositiveMatches)
	}
}

func TestComputeCorrectnessDeduplicatesObservedMatches(t *testing.T) {
	dataset := correctnessDataset("a", "b")

	got := ComputeCorrectness(dataset, []ObservedMatch{
		{EventAID: "a", EventBID: "b"},
		{EventAID: "b", EventBID: "a"},
	})

	if got.ConfirmedMatches != 1 {
		t.Fatalf("expected duplicate observed pair to count once, got %d", got.ConfirmedMatches)
	}
	if got.ConfirmedTrueMatches != 1 {
		t.Fatalf("expected 1 confirmed true match, got %d", got.ConfirmedTrueMatches)
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	values := []float64{50, 10, 40, 20, 30}

	if got := Percentile(values, 50); got != 30 {
		t.Fatalf("expected p50 30, got %f", got)
	}
	if got := Percentile(values, 95); got != 50 {
		t.Fatalf("expected p95 50, got %f", got)
	}
	if got := Percentile(values, 99); got != 50 {
		t.Fatalf("expected p99 50, got %f", got)
	}
}

func TestPercentileHandlesEmptyAndDoesNotMutateInput(t *testing.T) {
	if got := Percentile(nil, 50); got != 0 {
		t.Fatalf("expected empty percentile 0, got %f", got)
	}

	values := []float64{3, 1, 2}
	original := append([]float64(nil), values...)
	_ = Percentile(values, 50)

	if !reflect.DeepEqual(values, original) {
		t.Fatalf("expected percentile not to mutate input, got %v", values)
	}
}

func TestComputeLatencyMetricsUsesPairReadyTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dataset := correctnessDataset("a", "b", "c", "d", "e", "f")

	got := ComputeLatencyMetrics(dataset.GroundTruth, []ObservedMatch{
		{EventAID: "a", EventBID: "b", ObservedAt: base.Add(110 * time.Millisecond)},
		{EventAID: "c", EventBID: "d", ObservedAt: base.Add(250 * time.Millisecond)},
		{EventAID: "e", EventBID: "f", ObservedAt: base.Add(390 * time.Millisecond)},
	}, []PostResult{
		{EventID: "a", CompletedAt: base},
		{EventID: "b", CompletedAt: base.Add(10 * time.Millisecond)},
		{EventID: "c", CompletedAt: base.Add(20 * time.Millisecond)},
		{EventID: "d", CompletedAt: base.Add(50 * time.Millisecond)},
		{EventID: "e", CompletedAt: base.Add(60 * time.Millisecond)},
		{EventID: "f", CompletedAt: base.Add(90 * time.Millisecond)},
	})

	if got.P50Millis != 200 {
		t.Fatalf("expected p50 200ms, got %f", got.P50Millis)
	}
	if got.P95Millis != 300 {
		t.Fatalf("expected p95 300ms, got %f", got.P95Millis)
	}
	if got.P99Millis != 300 {
		t.Fatalf("expected p99 300ms, got %f", got.P99Millis)
	}
}

func TestComputeLatencyMetricsSkipsIncompleteInputs(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dataset := correctnessDataset("a", "b", "c", "d")

	got := ComputeLatencyMetrics(dataset.GroundTruth, []ObservedMatch{
		{EventAID: "a", EventBID: "b", ObservedAt: base.Add(150 * time.Millisecond)},
	}, []PostResult{
		{EventID: "a", CompletedAt: base},
		{EventID: "b", CompletedAt: base.Add(50 * time.Millisecond)},
		{EventID: "c", CompletedAt: base},
		{EventID: "d", CompletedAt: base.Add(50 * time.Millisecond)},
	})

	if got.P50Millis != 100 || got.P95Millis != 100 || got.P99Millis != 100 {
		t.Fatalf("expected only complete observed match latency to be measured, got %+v", got)
	}
}

func correctnessDataset(eventIDs ...string) loadgen.Dataset {
	expected := make(map[string]loadgen.ExpectedMatch)
	for i := 0; i < len(eventIDs); i += 2 {
		eventAID := eventIDs[i]
		eventBID := eventIDs[i+1]
		key := loadgen.PairKey(eventAID, eventBID)
		expected[key] = loadgen.ExpectedMatch{
			TruthID:  key,
			EventAID: eventAID,
			EventBID: eventBID,
			PairKey:  key,
		}
	}

	return loadgen.Dataset{
		GroundTruth: loadgen.GroundTruth{
			Expected: expected,
		},
	}
}
