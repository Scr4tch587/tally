package event

func GroupEventsBySource(events []*CanonicalEvent) map[string][]*CanonicalEvent {
	results := make(map[string][]*CanonicalEvent)
	for _, event := range events {
		results[event.SourceType] = append(results[event.SourceType], event)
	}
	return results
}

func FilterByAmountRange(events []*CanonicalEvent, min, max int64) []*CanonicalEvent {
	results := []*CanonicalEvent{}
	for _, event := range events {
		if event.AmountMinor >= min && event.AmountMinor <= max {
			results = append(results, event)
		}
	}
	return results
}		