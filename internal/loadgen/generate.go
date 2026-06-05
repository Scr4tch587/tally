package loadgen

import (
	"fmt"
	"math/rand"
	"time"
)

type ArrivalMode string

const (
	ArrivalModePaired   ArrivalMode = "paired"
	ArrivalModeShuffled ArrivalMode = "shuffled"
)

type EventKind string

const (
	KindTruePair        EventKind = "true_pair"
	KindSameSourceDecoy EventKind = "same_source_decoy"
	KindAmountDecoy     EventKind = "amount_decoy"
	KindTimeDecoy       EventKind = "time_decoy"
	KindAccountDecoy    EventKind = "account_decoy"
)

type Config struct {
	TenantID   string
	Seed       int64
	TruePairs  int
	DecoyRatio float64
	Arrival    ArrivalMode
}

type GeneratedEvent struct {
	TenantID        string
	EventID         string
	SourceType      string
	SourceEventID   string
	AmountMinor     int64
	Currency        string
	AssetCode       string
	Timestamp       time.Time
	Direction       string
	AccountRef      string
	CounterpartyRef string

	TruthID string
	Kind    EventKind
}

type ExpectedMatch struct {
	TruthID  string
	EventAID string
	EventBID string
	PairKey  string
}

type GroundTruth struct {
	Expected map[string]ExpectedMatch
}

type Dataset struct {
	Events      []GeneratedEvent
	GroundTruth GroundTruth
}

func shuffleEvents(events []GeneratedEvent, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(events), func(i, j int) {
		events[i], events[j] = events[j], events[i]
	})
}

func Generate(config Config) (Dataset, error) {
	if config.TenantID == "" {
		return Dataset{}, fmt.Errorf("tenant ID is required")
	}

	if config.TruePairs <= 0 {
		return Dataset{}, fmt.Errorf("true pairs must be positive")
	}

	if config.Arrival == "" {
		config.Arrival = ArrivalModePaired
	}

	if config.Arrival != ArrivalModePaired && config.Arrival != ArrivalModeShuffled {
		return Dataset{}, fmt.Errorf("arrival must be paired, shuffled, or empty")
	}

	dataset := Dataset{}
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dataset.GroundTruth.Expected = make(map[string]ExpectedMatch)

	for i := 0; i < config.TruePairs; i++ {
		appendTruePair(&dataset, config.TenantID, baseTime, i)
	}

	decoyCount := int(float64(config.TruePairs) * config.DecoyRatio)
	for i := 0; i < decoyCount; i++ {
		appendSameSourceDecoy(&dataset, config.TenantID, baseTime, i)
	}

	for i := 0; i < decoyCount; i++ {
		appendAmountDecoy(&dataset, config.TenantID, baseTime, i)
	}

	for i := 0; i < decoyCount; i++ {
		appendAccountDecoy(&dataset, config.TenantID, baseTime, i)
	}

	for i := 0; i < decoyCount; i++ {
		appendTimeDecoy(&dataset, config.TenantID, baseTime, i)
	}

	if config.Arrival == ArrivalModeShuffled {
		shuffleEvents(dataset.Events, config.Seed)
	}
	return dataset, nil
}

func appendTruePair(dataset *Dataset, tenantID string, baseTime time.Time, i int) {
	truthID := fmt.Sprintf("truth-%06d", i)
	ledgerEvent := eventID(tenantID, "ledger", truthID)
	processorEvent := eventID(tenantID, "processor", truthID)
	ledgerTimestamp := baseTime.Add(time.Duration(i) * 10 * time.Second)
	amountMinor := int64(1000 + i*10)
	accountRef := fmt.Sprintf("account-%d", i)
	counterpartyRef := fmt.Sprintf("counterparty-%d", i)

	dataset.Events = append(dataset.Events,
		generatedEvent(tenantID, ledgerEvent, "ledger", sourceEventID(tenantID, "ledger", truthID), amountMinor, ledgerTimestamp, accountRef, counterpartyRef, truthID, KindTruePair),
		generatedEvent(tenantID, processorEvent, "processor", sourceEventID(tenantID, "processor", truthID), amountMinor, ledgerTimestamp.Add(time.Second), accountRef, counterpartyRef, truthID, KindTruePair),
	)

	key := PairKey(ledgerEvent, processorEvent)
	dataset.GroundTruth.Expected[key] = ExpectedMatch{
		TruthID:  truthID,
		EventAID: ledgerEvent,
		EventBID: processorEvent,
		PairKey:  key,
	}
}

func appendSameSourceDecoy(dataset *Dataset, tenantID string, baseTime time.Time, i int) {
	truthID := fmt.Sprintf("decoy-same-source-%06d", i)
	ledgerTimestamp := baseTime.Add(time.Duration(i) * 10 * time.Second)
	accountRef := fmt.Sprintf("account-same-source-decoy-%d", i)
	counterpartyRef := fmt.Sprintf("counterparty-same-source-decoy-%d", i)

	dataset.Events = append(dataset.Events,
		generatedEvent(tenantID, eventID(tenantID, "ledger-a", truthID), "ledger", sourceEventID(tenantID, "ledger-a", truthID), 20000, ledgerTimestamp, accountRef, counterpartyRef, truthID, KindSameSourceDecoy),
		generatedEvent(tenantID, eventID(tenantID, "ledger-b", truthID), "ledger", sourceEventID(tenantID, "ledger-b", truthID), 20000, ledgerTimestamp.Add(time.Second), accountRef, counterpartyRef, truthID, KindSameSourceDecoy),
	)
}

func appendAmountDecoy(dataset *Dataset, tenantID string, baseTime time.Time, i int) {
	truthID := fmt.Sprintf("decoy-amount-%06d", i)
	ledgerTimestamp := baseTime.Add(time.Duration(i) * 10 * time.Second)
	accountRef := fmt.Sprintf("account-amount-decoy-%d", i)
	counterpartyRef := fmt.Sprintf("counterparty-amount-decoy-%d", i)

	dataset.Events = append(dataset.Events,
		generatedEvent(tenantID, eventID(tenantID, "ledger", truthID), "ledger", sourceEventID(tenantID, "ledger", truthID), 10000, ledgerTimestamp, accountRef, counterpartyRef, truthID, KindAmountDecoy),
		generatedEvent(tenantID, eventID(tenantID, "processor", truthID), "processor", sourceEventID(tenantID, "processor", truthID), 10500, ledgerTimestamp.Add(time.Second), accountRef, counterpartyRef, truthID, KindAmountDecoy),
	)
}

func appendAccountDecoy(dataset *Dataset, tenantID string, baseTime time.Time, i int) {
	truthID := fmt.Sprintf("decoy-account-%06d", i)
	ledgerTimestamp := baseTime.Add(time.Duration(i) * 10 * time.Second)
	counterpartyRef := fmt.Sprintf("counterparty-account-decoy-%d", i)

	dataset.Events = append(dataset.Events,
		generatedEvent(tenantID, eventID(tenantID, "ledger", truthID), "ledger", sourceEventID(tenantID, "ledger", truthID), 10000, ledgerTimestamp, fmt.Sprintf("account-decoy-ledger-%d", i), counterpartyRef, truthID, KindAccountDecoy),
		generatedEvent(tenantID, eventID(tenantID, "processor", truthID), "processor", sourceEventID(tenantID, "processor", truthID), 10000, ledgerTimestamp.Add(time.Second), fmt.Sprintf("account-decoy-processor-%d", i), counterpartyRef, truthID, KindAccountDecoy),
	)
}

func appendTimeDecoy(dataset *Dataset, tenantID string, baseTime time.Time, i int) {
	truthID := fmt.Sprintf("decoy-time-%06d", i)
	ledgerTimestamp := baseTime.Add(time.Duration(i) * 10 * time.Second)
	accountRef := fmt.Sprintf("account-time-decoy-%d", i)
	counterpartyRef := fmt.Sprintf("counterparty-time-decoy-%d", i)

	dataset.Events = append(dataset.Events,
		generatedEvent(tenantID, eventID(tenantID, "ledger", truthID), "ledger", sourceEventID(tenantID, "ledger", truthID), 10000, ledgerTimestamp, accountRef, counterpartyRef, truthID, KindTimeDecoy),
		generatedEvent(tenantID, eventID(tenantID, "processor", truthID), "processor", sourceEventID(tenantID, "processor", truthID), 10000, ledgerTimestamp.Add(10*time.Minute), accountRef, counterpartyRef, truthID, KindTimeDecoy),
	)
}

func generatedEvent(
	tenantID string,
	eventID string,
	sourceType string,
	sourceEventID string,
	amountMinor int64,
	timestamp time.Time,
	accountRef string,
	counterpartyRef string,
	truthID string,
	kind EventKind,
) GeneratedEvent {
	return GeneratedEvent{
		TenantID:        tenantID,
		EventID:         eventID,
		SourceType:      sourceType,
		SourceEventID:   sourceEventID,
		AmountMinor:     amountMinor,
		Currency:        "USD",
		AssetCode:       "USD",
		Timestamp:       timestamp,
		Direction:       "credit",
		AccountRef:      accountRef,
		CounterpartyRef: counterpartyRef,
		TruthID:         truthID,
		Kind:            kind,
	}
}

func eventID(tenantID string, source string, truthID string) string {
	return fmt.Sprintf("evt-%s-%s-%s", tenantID, source, truthID)
}

func sourceEventID(tenantID string, source string, truthID string) string {
	return fmt.Sprintf("src-%s-%s-%s", tenantID, source, truthID)
}
