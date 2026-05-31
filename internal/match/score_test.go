package match

import (
	"math"
	"testing"
	"time"

	"tally/internal/event"
)

func mustEvent(t *testing.T, opts eventOpts) *event.CanonicalEvent {
	t.Helper()

	o := defaultEventOpts()
	opts.apply(&o)

	ev, err := event.NewCanonicalEvent(
		o.tenantID,
		o.eventID,
		o.sourceType,
		o.sourceEventID,
		o.amountMinor,
		o.assetCode,
		o.currency,
		o.timestamp,
		o.direction,
		o.accountRef,
		o.counterpartyRef,
		o.metadata,
	)
	if err != nil {
		t.Fatalf("NewCanonicalEvent: %v", err)
	}
	return ev
}

type eventOpts struct {
	tenantID        *string
	eventID         *string
	sourceType      *string
	sourceEventID   *string
	amountMinor     *int64
	assetCode       *string
	currency        *string
	timestamp       *time.Time
	direction       *string
	accountRef      *string
	counterpartyRef *string
}

type defaultOpts struct {
	tenantID        string
	eventID         string
	sourceType      string
	sourceEventID   string
	amountMinor     int64
	assetCode       string
	currency        string
	timestamp       time.Time
	direction       string
	accountRef      string
	counterpartyRef string
	metadata        map[string]string
}

func defaultEventOpts() defaultOpts {
	return defaultOpts{
		tenantID:        "tenant-test",
		eventID:         "evt-a",
		sourceType:      "ledger",
		sourceEventID:   "src-a",
		amountMinor:     1250,
		assetCode:       "",
		currency:        "USD",
		timestamp:       time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
		direction:       "debit",
		accountRef:      "cash",
		counterpartyRef: "Acme Supplies",
		metadata:        map[string]string{},
	}
}

func (o eventOpts) apply(d *defaultOpts) {
	if o.tenantID != nil {
		d.tenantID = *o.tenantID
	}
	if o.eventID != nil {
		d.eventID = *o.eventID
	}
	if o.sourceType != nil {
		d.sourceType = *o.sourceType
	}
	if o.sourceEventID != nil {
		d.sourceEventID = *o.sourceEventID
	}
	if o.amountMinor != nil {
		d.amountMinor = *o.amountMinor
	}
	if o.assetCode != nil {
		d.assetCode = *o.assetCode
	}
	if o.currency != nil {
		d.currency = *o.currency
	}
	if o.timestamp != nil {
		d.timestamp = *o.timestamp
	}
	if o.direction != nil {
		d.direction = *o.direction
	}
	if o.accountRef != nil {
		d.accountRef = *o.accountRef
	}
	if o.counterpartyRef != nil {
		d.counterpartyRef = *o.counterpartyRef
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestAmountScore(t *testing.T) {
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	a := mustEvent(t, eventOpts{timestamp: &base, amountMinor: ptrInt64(1250)})

	tests := []struct {
		name    string
		amountB int64
		want    float64
	}{
		{"exact match", 1250, 1.0},
		{"one minor unit off", 1251, 0.5},
		{"two minor units off", 1252, 0.0},
		{"three minor units off", 1253, 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := mustEvent(t, eventOpts{
				eventID:       ptrString("evt-b"),
				sourceType:    ptrString("processor"),
				sourceEventID: ptrString("src-b"),
				amountMinor:   &tc.amountB,
				timestamp:     &base,
			})
			assertFloat(t, "amountScore", amountScore(a, b), tc.want)
		})
	}
}

func TestTimeScore(t *testing.T) {
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	a := mustEvent(t, eventOpts{timestamp: &base})

	tests := []struct {
		name      string
		offsetSec float64
		want      float64
	}{
		{"same instant", 0, 1.0},
		{"within plateau", 3, 1.0},
		{"at plateau edge", 5, 1.0},
		{"mid decay window", 62.5, 0.5},
		{"at max delta", 120, 0.0},
		{"beyond max delta", 121, 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bTime := base.Add(time.Duration(tc.offsetSec * float64(time.Second)))
			b := mustEvent(t, eventOpts{
				eventID:       ptrString("evt-b"),
				sourceType:    ptrString("processor"),
				sourceEventID: ptrString("src-b"),
				timestamp:     &bTime,
			})
			assertFloat(t, "timeScore", timeScore(a, b), tc.want)
		})
	}
}

func TestAccountScore(t *testing.T) {
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	a := mustEvent(t, eventOpts{accountRef: ptrString("cash"), timestamp: &base})

	tests := []struct {
		name     string
		accountB string
		want     float64
	}{
		{"exact match", "cash", 1.0},
		{"case insensitive exact", "CASH", 1.0},
		{"substring", "operating-cash", 0.5},
		{"reverse substring", "cas", 0.5},
		{"no relationship", "payroll", 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := mustEvent(t, eventOpts{
				eventID:       ptrString("evt-b"),
				sourceType:    ptrString("processor"),
				sourceEventID: ptrString("src-b"),
				accountRef:    &tc.accountB,
				timestamp:     &base,
			})
			assertFloat(t, "accountScore", accountScore(a, b), tc.want)
		})
	}
}

func TestScore(t *testing.T) {
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	t.Run("strong ledger processor pair", func(t *testing.T) {
		a := mustEvent(t, eventOpts{timestamp: &base, amountMinor: ptrInt64(1250), accountRef: ptrString("cash")})
		bTime := base.Add(2 * time.Second)
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1250),
			accountRef:    ptrString("cash"),
			timestamp:     &bTime,
		})

		score, evidence, ok := Score(a, b)
		if !ok {
			t.Fatalf("expected ok true, score=%v", score)
		}
		assertFloat(t, "score", score, 1.0)
		assertFloat(t, "evidence match_score", evidence["match_score"].(float64), score)
		assertFloat(t, "evidence amount", evidence["amount_score"].(float64), 1.0)
		assertFloat(t, "evidence time", evidence["time_score"].(float64), 1.0)
		assertFloat(t, "evidence account", evidence["account_score"].(float64), 1.0)
		if evidence["threshold"].(float64) != matchThreshold {
			t.Fatalf("threshold = %v, want %v", evidence["threshold"], matchThreshold)
		}
	})

	t.Run("one minor unit off below threshold", func(t *testing.T) {
		a := mustEvent(t, eventOpts{timestamp: &base, amountMinor: ptrInt64(1250), accountRef: ptrString("cash")})
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1251),
			accountRef:    ptrString("cash"),
			timestamp:     &base,
		})

		score, _, ok := Score(a, b)
		if ok {
			t.Fatalf("expected ok false with 1 minor unit difference, score=%v", score)
		}
		assertFloat(t, "score", score, 0.75)
	})

	t.Run("amount mismatch below threshold", func(t *testing.T) {
		a := mustEvent(t, eventOpts{timestamp: &base, amountMinor: ptrInt64(1250)})
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1253),
			timestamp:     &base,
		})

		score, _, ok := Score(a, b)
		if ok {
			t.Fatalf("expected ok false, score=%v", score)
		}
		assertFloat(t, "score", score, 0.5)
	})

	t.Run("time beyond window below threshold", func(t *testing.T) {
		a := mustEvent(t, eventOpts{timestamp: &base})
		late := base.Add(130 * time.Second)
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			timestamp:     &late,
		})

		_, _, ok := Score(a, b)
		if ok {
			t.Fatal("expected ok false for events 130s apart")
		}
	})

	t.Run("account mismatch below threshold", func(t *testing.T) {
		a := mustEvent(t, eventOpts{timestamp: &base, accountRef: ptrString("cash")})
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			accountRef:    ptrString("payroll"),
			timestamp:     &base,
		})

		_, _, ok := Score(a, b)
		if ok {
			t.Fatal("expected ok false for unrelated accounts")
		}
	})

	t.Run("at threshold boundary", func(t *testing.T) {
		a := mustEvent(t, eventOpts{
			timestamp:   &base,
			amountMinor: ptrInt64(1250),
			accountRef:  ptrString("cash"),
		})
		midWindow := base.Add(62*time.Second + 500*time.Millisecond)
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1250),
			accountRef:    ptrString("cash"),
			timestamp:     &midWindow,
		})

		score, _, ok := Score(a, b)
		assertFloat(t, "score", score, 0.85)
		if !ok {
			t.Fatalf("expected ok true at score == threshold, got %v", score)
		}
	})
}

func ptrString(s string) *string { return &s }
func ptrInt64(n int64) *int64    { return &n }
