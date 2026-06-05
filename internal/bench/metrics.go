package bench

import (
	"math"
	"sort"
	"time"

	"tally/internal/loadgen"
)

type ObservedMatch struct {
	EventAID   string
	EventBID   string
	ObservedAt time.Time
}

type Correctness struct {
	ExpectedMatches      int
	ConfirmedMatches     int
	ConfirmedTrueMatches int
	FalsePositiveMatches int
	MissedMatches        int
	MatchRate            float64
	FalsePositiveRate    float64
}

func ComputeCorrectness(dataset loadgen.Dataset, matches []ObservedMatch) Correctness {
	correctness := Correctness{
		ExpectedMatches: len(dataset.GroundTruth.Expected),
	}

	seen := make(map[string]struct{})
	for _, match := range matches {
		key := loadgen.PairKey(match.EventAID, match.EventBID)
		seen[key] = struct{}{}
	}

	correctness.ConfirmedMatches = len(seen)
	for key := range seen {
		if _, ok := dataset.GroundTruth.Expected[key]; ok {
			correctness.ConfirmedTrueMatches++
		} else {
			correctness.FalsePositiveMatches++
		}
	}

	correctness.MissedMatches = len(dataset.GroundTruth.Expected) - correctness.ConfirmedTrueMatches
	if correctness.ExpectedMatches > 0 {
		correctness.MatchRate = float64(correctness.ConfirmedTrueMatches) / float64(correctness.ExpectedMatches)
	}
	if correctness.ConfirmedMatches > 0 {
		correctness.FalsePositiveRate = float64(correctness.FalsePositiveMatches) / float64(correctness.ConfirmedMatches)
	}

	return correctness
}

type PostResult struct {
	EventID     string
	CompletedAt time.Time
}

type LatencyMetrics struct {
	P50Millis float64
	P95Millis float64
	P99Millis float64
}

func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	copiedValues := make([]float64, len(values))
	copy(copiedValues, values)

	sort.Float64s(copiedValues)

	index := int(math.Ceil(p/100*float64(len(copiedValues)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copiedValues) {
		index = len(copiedValues) - 1
	}
	return copiedValues[index]
}

func ComputeLatencyMetrics(groundTruth loadgen.GroundTruth, observedMatches []ObservedMatch, postResults []PostResult) LatencyMetrics {
	postCompletedByEventID := make(map[string]time.Time)
	for _, postResult := range postResults {
		postCompletedByEventID[postResult.EventID] = postResult.CompletedAt
	}

	observedAtByPairKey := make(map[string]time.Time)
	for _, match := range observedMatches {
		pairKey := loadgen.PairKey(match.EventAID, match.EventBID)
		observedAtByPairKey[pairKey] = match.ObservedAt
	}

	latencyMillis := make([]float64, 0, len(groundTruth.Expected))
	for key, expected := range groundTruth.Expected {
		eventACompleted := postCompletedByEventID[expected.EventAID]
		eventBCompleted := postCompletedByEventID[expected.EventBID]
		observedAt := observedAtByPairKey[key]
		if !eventACompleted.IsZero() && !eventBCompleted.IsZero() && !observedAt.IsZero() {
			readyAt := later(eventACompleted, eventBCompleted)
			latencyMillis = append(latencyMillis, float64(observedAt.Sub(readyAt).Milliseconds()))
		}
	}

	return LatencyMetrics{
		P50Millis: Percentile(latencyMillis, 50),
		P95Millis: Percentile(latencyMillis, 95),
		P99Millis: Percentile(latencyMillis, 99),
	}
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func ThroughputEventsPerSecond(eventsPosted int, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(eventsPosted) / duration.Seconds()
}
