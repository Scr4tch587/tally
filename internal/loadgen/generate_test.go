package loadgen

import (
	"reflect"
	"testing"
	"time"
)

func TestPairKeyIsOrderIndependent(t *testing.T) {
	gotAB := PairKey("evt-b", "evt-a")
	gotBA := PairKey("evt-a", "evt-b")

	if gotAB != gotBA {
		t.Fatalf("expected same key, got %q and %q", gotAB, gotBA)
	}

	if gotAB != "evt-a:evt-b" {
		t.Fatalf("expected sorted key, got %q", gotAB)
	}
}

func TestGroundTruthStoresExpectedMatchByPairKey(t *testing.T) {
	eventAID := "evt-ledger-truth-000001"
	eventBID := "evt-processor-truth-000001"
	key := PairKey(eventAID, eventBID)

	truth := GroundTruth{
		Expected: map[string]ExpectedMatch{
			key: {
				TruthID:  "truth-000001",
				EventAID: eventAID,
				EventBID: eventBID,
				PairKey:  key,
			},
		},
	}

	got, ok := truth.Expected[PairKey(eventBID, eventAID)]
	if !ok {
		t.Fatalf("expected match to be lookupable by reverse-order pair key")
	}

	if got.TruthID != "truth-000001" {
		t.Fatalf("expected truth ID %q, got %q", "truth-000001", got.TruthID)
	}

	if got.PairKey != key {
		t.Fatalf("expected stored pair key %q, got %q", key, got.PairKey)
	}
}

func TestGeneratedEventCanCarryTruthMetadata(t *testing.T) {
	event := GeneratedEvent{
		TenantID:      "tenant-1",
		EventID:       "evt-ledger-truth-000001",
		SourceType:    "ledger",
		SourceEventID: "src-ledger-truth-000001",
		TruthID:       "truth-000001",
		Kind:          KindTruePair,
	}

	if event.TruthID != "truth-000001" {
		t.Fatalf("expected truth ID %q, got %q", "truth-000001", event.TruthID)
	}

	if event.Kind != KindTruePair {
		t.Fatalf("expected kind %q, got %q", KindTruePair, event.Kind)
	}
}

func TestGenerateCreatesPairedTrueEventsAndGroundTruth(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:  "tenant-1",
		TruePairs: 2,
		Arrival:   ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	if len(dataset.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(dataset.Events))
	}

	if len(dataset.GroundTruth.Expected) != 2 {
		t.Fatalf("expected 2 expected matches, got %d", len(dataset.GroundTruth.Expected))
	}

	firstLedger := dataset.Events[0]
	firstProcessor := dataset.Events[1]

	if firstLedger.SourceType != "ledger" {
		t.Fatalf("expected first event to be ledger, got %q", firstLedger.SourceType)
	}

	if firstProcessor.SourceType != "processor" {
		t.Fatalf("expected second event to be processor, got %q", firstProcessor.SourceType)
	}

	if firstLedger.TruthID != firstProcessor.TruthID {
		t.Fatalf("expected first pair to share truth ID, got %q and %q", firstLedger.TruthID, firstProcessor.TruthID)
	}

	key := PairKey(firstLedger.EventID, firstProcessor.EventID)
	match, ok := dataset.GroundTruth.Expected[key]
	if !ok {
		t.Fatalf("expected ground truth match for key %q", key)
	}

	if match.TruthID != firstLedger.TruthID {
		t.Fatalf("expected match truth ID %q, got %q", firstLedger.TruthID, match.TruthID)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	config := Config{
		TenantID:  "tenant-1",
		Seed:      42,
		TruePairs: 3,
		Arrival:   ArrivalModePaired,
	}

	first, err := Generate(config)
	if err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}

	second, err := Generate(config)
	if err != nil {
		t.Fatalf("second generate returned error: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected same config to generate identical datasets")
	}
}

func TestGenerateRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "empty tenant",
			config: Config{
				TruePairs: 1,
				Arrival:   ArrivalModePaired,
			},
		},
		{
			name: "zero true pairs",
			config: Config{
				TenantID:  "tenant-1",
				TruePairs: 0,
				Arrival:   ArrivalModePaired,
			},
		},
		{
			name: "negative true pairs",
			config: Config{
				TenantID:  "tenant-1",
				TruePairs: -1,
				Arrival:   ArrivalModePaired,
			},
		},
		{
			name: "unknown arrival",
			config: Config{
				TenantID:  "tenant-1",
				TruePairs: 1,
				Arrival:   ArrivalMode("uniform"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Generate(tt.config); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestGenerateDefaultsEmptyArrivalToPaired(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:  "tenant-1",
		TruePairs: 1,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	if len(dataset.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(dataset.Events))
	}

	if dataset.Events[0].SourceType != "ledger" || dataset.Events[1].SourceType != "processor" {
		t.Fatalf("expected empty arrival to default to paired ledger/processor order")
	}
}

func TestGenerateAcceptsShuffledArrival(t *testing.T) {
	_, err := Generate(Config{
		TenantID:  "tenant-1",
		Seed:      42,
		TruePairs: 3,
		Arrival:   ArrivalModeShuffled,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}
}

func TestGenerateShuffledArrivalIsDeterministic(t *testing.T) {
	config := Config{
		TenantID:   "tenant-1",
		Seed:       42,
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModeShuffled,
	}

	first, err := Generate(config)
	if err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}

	second, err := Generate(config)
	if err != nil {
		t.Fatalf("second generate returned error: %v", err)
	}

	if !reflect.DeepEqual(first.Events, second.Events) {
		t.Fatalf("expected same seed to produce same shuffled event order")
	}

	if !reflect.DeepEqual(first.GroundTruth, second.GroundTruth) {
		t.Fatalf("expected same seed to produce same ground truth")
	}
}

func TestGenerateShuffledArrivalChangesEventOrder(t *testing.T) {
	config := Config{
		TenantID:   "tenant-1",
		Seed:       42,
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	}

	paired, err := Generate(config)
	if err != nil {
		t.Fatalf("paired generate returned error: %v", err)
	}

	config.Arrival = ArrivalModeShuffled
	shuffled, err := Generate(config)
	if err != nil {
		t.Fatalf("shuffled generate returned error: %v", err)
	}

	if reflect.DeepEqual(paired.Events, shuffled.Events) {
		t.Fatalf("expected shuffled arrival to change event order")
	}

	if !reflect.DeepEqual(paired.GroundTruth, shuffled.GroundTruth) {
		t.Fatalf("expected shuffled arrival to preserve ground truth")
	}
}

func TestGenerateCreatesUniqueEventIDs(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:  "tenant-1",
		TruePairs: 3,
		Arrival:   ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	seen := make(map[string]struct{})
	for _, event := range dataset.Events {
		if _, ok := seen[event.EventID]; ok {
			t.Fatalf("duplicate event ID %q", event.EventID)
		}
		seen[event.EventID] = struct{}{}
	}
}

func TestGenerateDoesNotCreateBankEvents(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:  "tenant-1",
		TruePairs: 3,
		Arrival:   ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	for _, event := range dataset.Events {
		if event.SourceType == "bank" {
			t.Fatalf("expected no bank events, got event %q", event.EventID)
		}
	}
}

func TestGenerateCreatesSameSourceDecoys(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	var decoys []GeneratedEvent
	for _, event := range dataset.Events {
		if event.Kind == KindSameSourceDecoy {
			decoys = append(decoys, event)
		}
	}

	if len(decoys) != 4 {
		t.Fatalf("expected 4 same-source decoy events, got %d", len(decoys))
	}

	for _, event := range decoys {
		if event.SourceType != "ledger" {
			t.Fatalf("expected same-source decoy to use ledger source, got %q", event.SourceType)
		}
		if event.AccountRef == "account-0" || event.AccountRef == "account-1" {
			t.Fatalf("expected same-source decoy account %q to be isolated from true-pair accounts", event.AccountRef)
		}
	}
}

func TestGenerateExcludesDecoysFromGroundTruth(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	if len(dataset.GroundTruth.Expected) != 4 {
		t.Fatalf("expected ground truth to include only true pairs, got %d", len(dataset.GroundTruth.Expected))
	}

	decoyIDs := make(map[string]struct{})
	for _, event := range dataset.Events {
		if event.Kind != KindTruePair {
			decoyIDs[event.EventID] = struct{}{}
		}
	}

	for _, match := range dataset.GroundTruth.Expected {
		if _, ok := decoyIDs[match.EventAID]; ok {
			t.Fatalf("expected decoy event %q to be excluded from ground truth", match.EventAID)
		}
		if _, ok := decoyIDs[match.EventBID]; ok {
			t.Fatalf("expected decoy event %q to be excluded from ground truth", match.EventBID)
		}
	}
}

func TestGenerateCreatesAmountDecoys(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	byTruthID := make(map[string][]GeneratedEvent)
	for _, event := range dataset.Events {
		if event.Kind == KindAmountDecoy {
			byTruthID[event.TruthID] = append(byTruthID[event.TruthID], event)
		}
	}

	if len(byTruthID) != 2 {
		t.Fatalf("expected 2 amount decoy pairs, got %d", len(byTruthID))
	}

	for truthID, events := range byTruthID {
		if len(events) != 2 {
			t.Fatalf("expected amount decoy %q to have 2 events, got %d", truthID, len(events))
		}

		first := events[0]
		second := events[1]
		if first.SourceType == second.SourceType {
			t.Fatalf("expected amount decoy %q to use different sources, got %q and %q", truthID, first.SourceType, second.SourceType)
		}
		if first.AmountMinor == second.AmountMinor {
			t.Fatalf("expected amount decoy %q to use mismatched amounts", truthID)
		}
		if first.AccountRef != second.AccountRef {
			t.Fatalf("expected amount decoy %q to share account ref", truthID)
		}
	}
}

func TestGenerateCreatesAccountDecoys(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	byTruthID := make(map[string][]GeneratedEvent)
	for _, event := range dataset.Events {
		if event.Kind == KindAccountDecoy {
			byTruthID[event.TruthID] = append(byTruthID[event.TruthID], event)
		}
	}

	if len(byTruthID) != 2 {
		t.Fatalf("expected 2 account decoy pairs, got %d", len(byTruthID))
	}

	for truthID, events := range byTruthID {
		if len(events) != 2 {
			t.Fatalf("expected account decoy %q to have 2 events, got %d", truthID, len(events))
		}

		first := events[0]
		second := events[1]
		if first.SourceType == second.SourceType {
			t.Fatalf("expected account decoy %q to use different sources, got %q and %q", truthID, first.SourceType, second.SourceType)
		}
		if first.AmountMinor != second.AmountMinor {
			t.Fatalf("expected account decoy %q to use matching amounts", truthID)
		}
		if first.AccountRef == second.AccountRef {
			t.Fatalf("expected account decoy %q to use mismatched account refs", truthID)
		}
	}
}

func TestGenerateCreatesTimeDecoys(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  4,
		DecoyRatio: 0.5,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	byTruthID := make(map[string][]GeneratedEvent)
	for _, event := range dataset.Events {
		if event.Kind == KindTimeDecoy {
			byTruthID[event.TruthID] = append(byTruthID[event.TruthID], event)
		}
	}

	if len(byTruthID) != 2 {
		t.Fatalf("expected 2 time decoy pairs, got %d", len(byTruthID))
	}

	for truthID, events := range byTruthID {
		if len(events) != 2 {
			t.Fatalf("expected time decoy %q to have 2 events, got %d", truthID, len(events))
		}

		first := events[0]
		second := events[1]
		if first.SourceType == second.SourceType {
			t.Fatalf("expected time decoy %q to use different sources, got %q and %q", truthID, first.SourceType, second.SourceType)
		}
		if first.AmountMinor != second.AmountMinor {
			t.Fatalf("expected time decoy %q to use matching amounts", truthID)
		}
		if first.AccountRef != second.AccountRef {
			t.Fatalf("expected time decoy %q to share account ref", truthID)
		}
		if second.Timestamp.Sub(first.Timestamp) < 10*time.Minute {
			t.Fatalf("expected time decoy %q to have at least 10 minute gap", truthID)
		}
	}
}

func TestGenerateEventCountIncludesAllDecoyKinds(t *testing.T) {
	dataset, err := Generate(Config{
		TenantID:   "tenant-1",
		TruePairs:  10,
		DecoyRatio: 0.4,
		Arrival:    ArrivalModePaired,
	})
	if err != nil {
		t.Fatalf("generate returned error: %v", err)
	}

	trueEvents := 10 * 2
	decoyPairsPerKind := 4
	decoyKinds := 4
	decoyEvents := decoyPairsPerKind * decoyKinds * 2
	want := trueEvents + decoyEvents

	if len(dataset.Events) != want {
		t.Fatalf("expected %d total events, got %d", want, len(dataset.Events))
	}

	if len(dataset.GroundTruth.Expected) != 10 {
		t.Fatalf("expected 10 expected matches, got %d", len(dataset.GroundTruth.Expected))
	}
}
