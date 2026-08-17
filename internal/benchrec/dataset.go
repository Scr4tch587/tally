package benchrec

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	SideA = "A"
	SideB = "B"

	SourceTypeLedger = "ledger"
	SourceTypeBank   = "bank"
)

type Leg struct {
	Side        string
	MatchID     string
	SourceID    string
	AmountMinor int64
	Direction   string
	ValueDate   time.Time
	Currency    string
	AccountRef  string
	References  string
	Attributes  string
	MatchRule   string
	MatchedBy   string
}

type Pair struct {
	MatchID string
	A       Leg
	B       Leg
}

type Dataset struct {
	Pairs       []Pair
	Distractors []Leg
	Stats       LoadStats
}

type LoadStats struct {
	RowsRead            int `json:"rows_read"`
	DistinctMatchIDs    int `json:"distinct_match_ids"`
	OneToOneGroups      int `json:"one_to_one_groups"`
	SignDirectionConfl  int `json:"sign_direction_conflicts"`
	AmountParseFailures int `json:"amount_parse_failures"`
	EmptyCounterparty   int `json:"empty_counterparty_fallbacks"`
}

var amountPattern = regexp.MustCompile(`^(-?)(\d+)\.(\d{2})$`)

func parseAmountMinor(raw string) (int64, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false, fmt.Errorf("empty amount")
	}

	groups := amountPattern.FindStringSubmatch(trimmed)
	if groups == nil {
		return 0, false, fmt.Errorf("amount %q is not a 2-decimal value", trimmed)
	}

	var whole, frac int64
	if _, err := fmt.Sscanf(groups[2], "%d", &whole); err != nil {
		return 0, false, err
	}
	if _, err := fmt.Sscanf(groups[3], "%d", &frac); err != nil {
		return 0, false, err
	}

	return whole*100 + frac, groups[1] == "-", nil
}

func parseValueDate(raw string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), time.UTC)
}

type groupShape struct {
	aRows int
	bRows int
	rows  int
}

func scanShapes(path string) (map[string]*groupShape, []string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer file.Close()

	reader := newCSVReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, nil, 0, err
	}
	index := columnIndex(header)

	shapes := make(map[string]*groupShape)
	order := make([]string, 0)
	rows := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, 0, err
		}
		rows++

		matchID := field(record, index, "matchId")
		shape, ok := shapes[matchID]
		if !ok {
			shape = &groupShape{}
			shapes[matchID] = shape
			order = append(order, matchID)
		}
		shape.rows++
		if field(record, index, "A_id") != "" {
			shape.aRows++
		}
		if field(record, index, "B_id") != "" {
			shape.bRows++
		}
	}

	return shapes, order, rows, nil
}

func isOneToOne(shape *groupShape) bool {
	return shape != nil && shape.rows == 2 && shape.aRows == 1 && shape.bRows == 1
}

func Load(path string, limit int) (*Dataset, error) {
	shapes, order, rows, err := scanShapes(path)
	if err != nil {
		return nil, err
	}

	dataset := &Dataset{
		Stats: LoadStats{RowsRead: rows, DistinctMatchIDs: len(shapes)},
	}

	selected := make(map[string]bool)
	for _, matchID := range order {
		if !isOneToOne(shapes[matchID]) {
			continue
		}
		dataset.Stats.OneToOneGroups++
		if limit > 0 && len(selected) >= limit {
			continue
		}
		selected[matchID] = true
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := newCSVReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	index := columnIndex(header)

	building := make(map[string]*Pair)
	valueDates := make(map[time.Time]bool)
	pending := make([]Leg, 0)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		matchID := field(record, index, "matchId")
		for _, side := range []string{SideA, SideB} {
			if field(record, index, side+"_id") == "" {
				continue
			}

			leg, err := readLeg(record, index, side, matchID, &dataset.Stats)
			if err != nil {
				dataset.Stats.AmountParseFailures++
				continue
			}

			if selected[matchID] {
				pair, ok := building[matchID]
				if !ok {
					pair = &Pair{MatchID: matchID}
					building[matchID] = pair
				}
				if side == SideA {
					pair.A = leg
				} else {
					pair.B = leg
				}
				valueDates[leg.ValueDate] = true
				continue
			}

			pending = append(pending, leg)
		}
	}

	for _, matchID := range order {
		if pair, ok := building[matchID]; ok {
			dataset.Pairs = append(dataset.Pairs, *pair)
		}
	}

	for _, leg := range pending {
		if limit > 0 && !valueDates[leg.ValueDate] {
			continue
		}
		dataset.Distractors = append(dataset.Distractors, leg)
	}

	return dataset, nil
}

func readLeg(record []string, index map[string]int, side, matchID string, stats *LoadStats) (Leg, error) {
	amountMinor, negative, err := parseAmountMinor(field(record, index, side+"_amount"))
	if err != nil {
		return Leg{}, err
	}

	valueDate, err := parseValueDate(field(record, index, side+"_valueDate"))
	if err != nil {
		return Leg{}, err
	}

	debitOrCredit := strings.ToUpper(strings.TrimSpace(field(record, index, side+"_debitOrCredit")))
	direction := "credit"
	if debitOrCredit == "DR" {
		direction = "debit"
	}
	if negative != (debitOrCredit == "DR") {
		stats.SignDirectionConfl++
	}

	return Leg{
		Side:        side,
		MatchID:     matchID,
		SourceID:    field(record, index, side+"_id"),
		AmountMinor: amountMinor,
		Direction:   direction,
		ValueDate:   valueDate,
		Currency:    strings.TrimSpace(field(record, index, side+"_currencyCode")),
		AccountRef:  strings.TrimSpace(field(record, index, side+"_account")),
		References:  field(record, index, side+"_transactionReferences"),
		Attributes:  field(record, index, side+"_transactionAttributes"),
		MatchRule:   strings.TrimSpace(field(record, index, "matchRule")),
		MatchedBy:   strings.TrimSpace(field(record, index, "matchedBy")),
	}, nil
}

func newCSVReader(r io.Reader) *csv.Reader {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.ReuseRecord = false
	return reader
}

func columnIndex(header []string) map[string]int {
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))] = i
	}
	return index
}

func field(record []string, index map[string]int, name string) string {
	i, ok := index[name]
	if !ok || i >= len(record) {
		return ""
	}
	return record[i]
}
