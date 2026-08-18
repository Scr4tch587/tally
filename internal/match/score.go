package match

import "tally/internal/event"
import "strings"

const maxAmountDiffMinor int64 = 2
const maxTimeDelta float64 = 120.0
const minTimeDelta float64 = 5.0
const matchThreshold float64 = 0.79
const nGramSize int = 6
const (
	wAmount  = 0.4
	wTime    = 0.25
	wAccount = 0.15
	wText    = 0.2
)

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

func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z')
}

func referenceRuns(s string) []string {
	s_norm := strings.ToLower(s)

	return strings.FieldsFunc(s_norm, func(r rune) bool { return !isAlphanumeric(r) })
}

func nGrams(runs []string, k int) map[string]struct{} {
	grams := map[string]struct{}{}
	for _, run := range runs {
		for i := 0; i <= len(run)-k; i++ {
			grams[run[i:i+k]] = struct{}{}
		}
	}
	return grams
}

func overlapCoefficient(a, b map[string]struct{}) float64 {
	if len(a) == 0 {
		return 0.0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	intersection := 0
	for gram := range a {
		if _, ok := b[gram]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(a))
}

func textScore(a, b *event.CanonicalEvent) (float64, bool) {
	aRuns := referenceRuns(a.CounterpartyRef)
	bRuns := referenceRuns(b.CounterpartyRef)
	aGrams := nGrams(aRuns, nGramSize)
	bGrams := nGrams(bRuns, nGramSize)
	if len(aGrams) == 0 && len(bGrams) == 0 {
		return 0.0, false
	}
	if len(aGrams) == 0 || len(bGrams) == 0 {
		return 0.0, true
	}
	return overlapCoefficient(aGrams, bGrams), true
}

func Score(a, b *event.CanonicalEvent) (score float64, evidence map[string]any, ok bool) {

	amountScore := amountScore(a, b)
	timeScore := timeScore(a, b)
	accountScore := accountScore(a, b)
	textScore, textMatch := textScore(a, b)

	if textMatch {
		score = amountScore*wAmount + timeScore*wTime + accountScore*wAccount + textScore*wText
	} else {
		score = (amountScore*wAmount + timeScore*wTime + accountScore*wAccount) / (1 - wText)
	}
	ok = score >= matchThreshold

	evidence = make(map[string]any)
	evidence["amount_score"] = amountScore
	evidence["time_score"] = timeScore
	evidence["account_score"] = accountScore
	evidence["text_score"] = textScore
	evidence["text_match"] = textMatch
	evidence["match_score"] = score
	evidence["threshold"] = matchThreshold

	return score, evidence, ok
}
