package event

import (
	"fmt"
	"time"
	"encoding/json"
	"errors"
)

type CanonicalEvent struct {
	EventID string
	SourceEventID string
	SourceType string
	AmountMinor int64
	Currency string
	Timestamp time.Time
	Metadata map[string]string
	IngestedAt time.Time
	Direction string
	AccountRef string 
	IdempotencyKey string
}

func NewCanonicalEvent(eventID string, sourceType string, amountMinor int64, currency string, ts time.Time) (*CanonicalEvent, error) {
	if eventID == "" {
		return nil, fmt.Errorf("event ID is required")
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amountMinor must be positive")
	}
	metadata := map[string]string{}
	return &CanonicalEvent{EventID: eventID, SourceType: sourceType, AmountMinor: amountMinor, Currency: currency, Timestamp: ts, Metadata: metadata}, nil
}

type Normalizer interface {
	Normalize(raw []byte) (*CanonicalEvent, error)
}

type LedgerNormalizer struct{}

func (n *LedgerNormalizer) Normalize(raw []byte) (*CanonicalEvent, error) {
	newEvent := CanonicalEvent{}
	err := json.Unmarshal(raw, &newEvent)
	if err != nil {
		return nil, err
	}
	return &newEvent, nil
}

var ErrInvalidAmount = errors.New("invalid amount")
var ErrUnsupportedCurrency = errors.New("unsupported currency")

func ValidateAmount(amount int64, currency string) error {
	if amount <= 0 {
		return fmt.Errorf("ValidateAmount: %w", ErrInvalidAmount)
	}
	if currency != "USD" && currency != "EUR" && currency != "GBP" {
		return fmt.Errorf("SupportCurrency: %w", ErrUnsupportedCurrency)
	}

	return nil
}