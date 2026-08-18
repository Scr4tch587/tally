package benchrec

import (
	"strings"
	"time"
)

type Event struct {
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
	Metadata        map[string]string
}

func SourceTypeFor(side string) string {
	if side == SideA {
		return SourceTypeLedger
	}
	return SourceTypeBank
}

func CounterpartyRefFor(leg Leg) string {
	combined := strings.TrimSpace(strings.Join(strings.Fields(leg.References+" "+leg.Attributes), " "))
	if combined == "" {
		return leg.SourceID
	}
	return combined
}

func (l Leg) EventID(runID string) string {
	return runID + ":" + l.Side + ":" + l.SourceID
}

func ToEvent(runID string, leg Leg, stats *LoadStats) Event {
	counterpartyRef := CounterpartyRefFor(leg)
	if counterpartyRef == leg.SourceID && stats != nil {
		stats.EmptyCounterparty++
	}

	return Event{
		TenantID:        runID,
		EventID:         leg.EventID(runID),
		SourceType:      SourceTypeFor(leg.Side),
		SourceEventID:   leg.SourceID,
		AmountMinor:     leg.AmountMinor,
		Currency:        leg.Currency,
		AssetCode:       leg.Currency,
		Timestamp:       leg.ValueDate,
		Direction:       leg.Direction,
		AccountRef:      leg.AccountRef,
		CounterpartyRef: counterpartyRef,
		Metadata: map[string]string{
			"benchrec_match_id":   leg.MatchID,
			"benchrec_side":       leg.Side,
			"benchrec_match_rule": leg.MatchRule,
			"benchrec_matched_by": leg.MatchedBy,
			"benchrec_references": leg.References,
			"benchrec_attributes": leg.Attributes,
		},
	}
}

func (p Pair) Rule() string {
	if p.A.MatchRule != "" {
		return p.A.MatchRule
	}
	if p.B.MatchRule != "" {
		return p.B.MatchRule
	}
	return "(blank)"
}

func (d *Dataset) Events(runID string, includeDistractors bool) []Event {
	events := make([]Event, 0, len(d.Pairs)*2+len(d.Distractors))
	for _, pair := range d.Pairs {
		events = append(events, ToEvent(runID, pair.A, &d.Stats), ToEvent(runID, pair.B, &d.Stats))
	}
	if includeDistractors {
		for _, leg := range d.Distractors {
			events = append(events, ToEvent(runID, leg, &d.Stats))
		}
	}
	return events
}
