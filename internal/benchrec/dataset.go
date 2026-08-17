package benchrec

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"sort"
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
	WindowDays          int `json:"window_days"`
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

func expectNegative(side, debitOrCredit string) bool {
	if side == SideA {
		return debitOrCredit == "DR"
	}
	return debitOrCredit == "CR"
}

func parseValueDate(raw string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), time.UTC)
}

type groupShape struct {
	aRows int
	bRows int
	rows  int
	date  string
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
			if shape.date == "" {
				shape.date = strings.TrimSpace(field(record, index, "A_valueDate"))
			}
		}
		if field(record, index, "B_id") != "" {
			shape.bRows++
			if shape.date == "" {
				shape.date = strings.TrimSpace(field(record, index, "B_valueDate"))
			}
		}
	}

	return shapes, order, rows, nil
}

func isOneToOne(shape *groupShape) bool {
	return shape != nil && shape.rows == 2 && shape.aRows == 1 && shape.bRows == 1
}

func Load(path string, limit int, seed int64) (*Dataset, error) {
	shapes, order, rows, err := scanShapes(path)
	if err != nil {
		return nil, err
	}

	dataset := &Dataset{
		Stats: LoadStats{RowsRead: rows, DistinctMatchIDs: len(shapes)},
	}

	oneToOne := make([]string, 0)
	for _, matchID := range order {
		if isOneToOne(shapes[matchID]) {
			oneToOne = append(oneToOne, matchID)
		}
	}
	dataset.Stats.OneToOneGroups = len(oneToOne)

	chosen := oneToOne
	window := map[string]bool{}
	if limit > 0 && limit < len(oneToOne) {
		chosen, window = selectDateWindow(oneToOne, shapes, limit, seed)
	}

	selected := make(map[string]bool, len(chosen))
	for _, matchID := range chosen {
		selected[matchID] = true
	}
	dataset.Stats.WindowDays = len(window)

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
		if len(window) > 0 && !window[leg.ValueDate.Format("2006-01-02")] {
			continue
		}
		dataset.Distractors = append(dataset.Distractors, leg)
	}

	return dataset, nil
}

func selectDateWindow(oneToOne []string, shapes map[string]*groupShape, limit int, seed int64) ([]string, map[string]bool) {
	byDate := make(map[string][]string)
	for _, matchID := range oneToOne {
		date := shapes[matchID].date
		byDate[date] = append(byDate[date], matchID)
	}

	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	rng := rand.New(rand.NewSource(seed))
	start := rng.Intn(len(dates))

	chosen := make([]string, 0, limit)
	window := make(map[string]bool)
	for i := 0; i < len(dates) && len(chosen) < limit; i++ {
		date := dates[(start+i)%len(dates)]
		window[date] = true
		chosen = append(chosen, byDate[date]...)
	}

	return chosen, window
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
	if negative != expectNegative(side, debitOrCredit) {
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
