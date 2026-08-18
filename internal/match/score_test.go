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

	t.Run("one minor unit off still matches", func(t *testing.T) {
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
		if !ok {
			t.Fatalf("expected ok true with 1 minor unit difference, score=%v", score)
		}
		assertFloat(t, "score", score, 0.8)
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
		assertFloat(t, "score", score, 0.6)
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

	t.Run("just above threshold matches", func(t *testing.T) {
		a := mustEvent(t, eventOpts{
			timestamp:   &base,
			amountMinor: ptrInt64(1250),
			accountRef:  ptrString("cash"),
		})
		late := base.Add(100 * time.Second)
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1250),
			accountRef:    ptrString("cash"),
			timestamp:     &late,
		})

		score, _, ok := Score(a, b)
		if !ok {
			t.Fatalf("expected ok true just above threshold, got %v", score)
		}
		if score < matchThreshold {
			t.Fatalf("score %v should be >= threshold %v", score, matchThreshold)
		}
	})

	t.Run("just below threshold does not match", func(t *testing.T) {
		a := mustEvent(t, eventOpts{
			timestamp:   &base,
			amountMinor: ptrInt64(1250),
			accountRef:  ptrString("cash"),
		})
		later := base.Add(105 * time.Second)
		b := mustEvent(t, eventOpts{
			eventID:       ptrString("evt-b"),
			sourceType:    ptrString("processor"),
			sourceEventID: ptrString("src-b"),
			amountMinor:   ptrInt64(1250),
			accountRef:    ptrString("cash"),
			timestamp:     &later,
		})

		score, _, ok := Score(a, b)
		if ok {
			t.Fatalf("expected ok false just below threshold, got %v", score)
		}
		if score >= matchThreshold {
			t.Fatalf("score %v should be < threshold %v", score, matchThreshold)
		}
	})
}

func ptrString(s string) *string { return &s }
func ptrInt64(n int64) *int64    { return &n }

// BenchRec is pure ASCII, so byte-slicing in nGrams is safe here. Non-ASCII
// input would slice multi-byte characters and produce garbage grams.
func TestReferenceRuns(t *testing.T) {
	got := referenceRuns("V418067061617140 4839566721VI")
	want := []string{"v418067061617140", "4839566721vi"}
	if len(got) != len(want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runs = %v, want %v", got, want)
		}
	}

	if runs := referenceRuns("001446684-767-0 L-7722566049VI"); len(runs) != 5 {
		t.Fatalf("expected hyphens to split into 5 runs, got %v", runs)
	}
}

func TestNGrams(t *testing.T) {
	grams := nGrams(referenceRuns("41806706 V41806706"), 6)
	want := []string{"418067", "180670", "806706", "v41806"}
	if len(grams) != len(want) {
		t.Fatalf("gram count = %d, want %d: %v", len(grams), len(want), grams)
	}
	for _, g := range want {
		if _, ok := grams[g]; !ok {
			t.Fatalf("missing gram %q in %v", g, grams)
		}
	}

	if g := nGrams([]string{"joqge"}, 6); len(g) != 0 {
		t.Fatalf("run shorter than k must yield no grams, got %v", g)
	}
}

func TestOverlapCoefficient(t *testing.T) {
	empty := map[string]struct{}{}
	full := nGrams(referenceRuns("41806706 V41806706"), 6)

	if v := overlapCoefficient(empty, full); v != 0.0 {
		t.Fatalf("empty set must score 0, got %v", v)
	}
	if v := overlapCoefficient(empty, empty); v != 0.0 {
		t.Fatalf("two empty sets must score 0 and never NaN, got %v", v)
	}
	if v := overlapCoefficient(full, full); v != 1.0 {
		t.Fatalf("identical sets must score 1, got %v", v)
	}
}

func TestTextScore(t *testing.T) {
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	pair := func(refA, refB string) (*event.CanonicalEvent, *event.CanonicalEvent) {
		a := mustEvent(t, eventOpts{timestamp: &base, counterpartyRef: ptrString(refA)})
		b := mustEvent(t, eventOpts{
			eventID:         ptrString("evt-b"),
			sourceType:      ptrString("processor"),
			sourceEventID:   ptrString("src-b"),
			timestamp:       &base,
			counterpartyRef: ptrString(refB),
		})
		return a, b
	}

	t.Run("reference embedded in a longer run", func(t *testing.T) {
		a, b := pair("41806706 V41806706", "V418067061617140 4839566721VI")
		score, applies := textScore(a, b)
		if !applies {
			t.Fatal("expected text component to apply")
		}
		if score != 1.0 {
			t.Fatalf("smaller reference fully absorbed should score 1.0, got %v", score)
		}
	})

	t.Run("shared prefix different tail", func(t *testing.T) {
		a, b := pair("008641132 14476 SKITE52", "TUP 29PL00864113266IFV /606/488740")
		score, applies := textScore(a, b)
		if !applies {
			t.Fatal("expected text component to apply")
		}
		if score <= 0.0 {
			t.Fatalf("shared 6-gram prefix should score above zero, got %v", score)
		}
	})

	t.Run("unrelated references score zero", func(t *testing.T) {
		a, b := pair("JOQGE 5280766176VI", "VOLERY 8929848666FP")
		score, _ := textScore(a, b)
		if score != 0.0 {
			t.Fatalf("unrelated references should score 0, got %v", score)
		}
	})

	t.Run("both sides without grams is missing not zero", func(t *testing.T) {
		a, b := pair("ab cd", "ef gh")
		score, applies := textScore(a, b)
		if applies {
			t.Fatal("no grams on either side must report the component as not applicable")
		}
		if score != 0.0 {
			t.Fatalf("score = %v, want 0.0", score)
		}
	})

	t.Run("one side without grams counts as zero evidence", func(t *testing.T) {
		a, b := pair("41806706 V41806706", "ab cd")
		score, applies := textScore(a, b)
		if !applies {
			t.Fatal("one-sided missing grams must still apply, so blank data cannot outrank weak data")
		}
		if score != 0.0 {
			t.Fatalf("score = %v, want 0.0", score)
		}
	})
}
