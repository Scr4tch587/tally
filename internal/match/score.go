package match

import "tally/internal/event"
import "strings"

const maxAmountDiffMinor int64 = 2
const maxTimeDelta float64 = 120.0
const minTimeDelta float64 = 5.0
const matchThreshold float64 = 0.85

func amountScore(a, b *event.CanonicalEvent) float64 {
	d := a.AmountMinor - b.AmountMinor
	if d < 0 {
		d = -d
	}
	if d >= maxAmountDiffMinor {
		return 0.0
	}
	return 1.0 - float64(d)/float64(maxAmountDiffMinor)
}

func timeScore(a, b *event.CanonicalEvent) float64 {
	d := a.Timestamp.Sub(b.Timestamp).Seconds()
	if d < 0 {
		d = -d
	}
	if d <= minTimeDelta {
		return 1.0
	}
	if d >= maxTimeDelta {
		return 0.0
	}
	return 1.0 - float64(d-minTimeDelta)/float64(maxTimeDelta-minTimeDelta)
}

func accountScore(a, b *event.CanonicalEvent) float64 {
	a_norm := strings.ToLower(a.AccountRef)
	b_norm := strings.ToLower(b.AccountRef)
	if a_norm == b_norm {
		return 1.0
	}

	if strings.Contains(a_norm, b_norm) || strings.Contains(b_norm, a_norm) {
		return 0.5
	}

	return 0.0
}

func Score(a, b *event.CanonicalEvent) (score float64, evidence map[string]any, ok bool) {

	amountScore := amountScore(a, b)
	timeScore := timeScore(a, b)
	accountScore := accountScore(a, b)

	score = amountScore*0.5 + timeScore*0.3 + accountScore*0.2
	ok = score >= matchThreshold

	evidence = make(map[string]any)
	evidence["amount_score"] = amountScore
	evidence["time_score"] = timeScore
	evidence["account_score"] = accountScore
	evidence["match_score"] = score
	evidence["threshold"] = matchThreshold

	return score, evidence, ok
}
