package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type CanonicalEvent struct {
	TenantID        string
	EventID         string // globally unique, assigned by connector
	SourceType      string // "ledger" | "processor" | "bank"
	SourceEventID   string // original ID from the source system
	AmountMinor     int64
	Currency        string    // ISO 4217 when fiat
	AssetCode       string    // optional: "USD" | "USDC" | etc. for sandbox/demo cases
	Timestamp       time.Time // source-reported transaction time
	IngestedAt      time.Time
	Direction       string // "debit" | "credit"
	AccountRef      string // normalized account or wallet reference
	CounterpartyRef string // raw payee / customer / merchant descriptor
	Metadata        map[string]string
	IdempotencyKey  string // tenant_id + source_type + source_event_id
}

func RequireNonEmpty(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}

	return nil
}

func ValidateSourceType(sourceType string) error {
	if sourceType != "ledger" && sourceType != "processor" && sourceType != "bank" {
		return fmt.Errorf("SourceType must be ledger, processor, or bank, not %s", sourceType)
	}
	return nil
}

func NewCanonicalEvent(tenantID string, eventID string, sourceType string, sourceEventID string, amountMinor int64, assetCode string, currency string, timestamp time.Time, direction string, accountRef string, counterpartyRef string, metadata map[string]string) (*CanonicalEvent, error) {
	err := RequireNonEmpty("TenantID", tenantID)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = RequireNonEmpty("EventID", eventID)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = RequireNonEmpty("SourceType", sourceType)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = ValidateSourceType(sourceType)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = RequireNonEmpty("SourceEventID", sourceEventID)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = RequireNonEmpty("AccountRef", accountRef)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	err = RequireNonEmpty("CounterpartyRef", counterpartyRef)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	if timestamp.IsZero() {
		return nil, fmt.Errorf("Timestamp must not be zero")
	}
	timestamp = timestamp.UTC()

	err = ValidateAmount(amountMinor, currency)
	if err != nil {
		return nil, fmt.Errorf("ValidationError: %w", err)
	}

	if direction != "credit" && direction != "debit" {
		return nil, fmt.Errorf("Direction must be debit or credit")
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	idempotencyKey := tenantID + ":" + sourceType + ":" + sourceEventID
	ingestedAt := time.Now()
	return &CanonicalEvent{TenantID: tenantID, EventID: eventID, SourceType: sourceType, SourceEventID: sourceEventID, AmountMinor: amountMinor, Currency: currency, AssetCode: assetCode, Timestamp: timestamp, IngestedAt: ingestedAt, Direction: direction, AccountRef: accountRef, CounterpartyRef: counterpartyRef, Metadata: metadata, IdempotencyKey: idempotencyKey}, nil
}

type Normalizer interface {
	Normalize(raw []byte) (*CanonicalEvent, error)
}

type LedgerNormalizer struct{}

func (n *LedgerNormalizer) Normalize(raw []byte) (*CanonicalEvent, error) {
	var input struct {
		TenantID        string            `json:"TenantID"`
		EventID         string            `json:"EventID"`
		SourceType      string            `json:"SourceType"`
		SourceEventID   string            `json:"SourceEventID"`
		AmountMinor     int64             `json:"AmountMinor"`
		Currency        string            `json:"Currency"`
		AssetCode       string            `json:"AssetCode"`
		Timestamp       time.Time         `json:"Timestamp"`
		Direction       string            `json:"Direction"`
		AccountRef      string            `json:"AccountRef"`
		CounterpartyRef string            `json:"CounterpartyRef"`
		Metadata        map[string]string `json:"Metadata"`
	}

	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}

	return NewCanonicalEvent(
		input.TenantID,
		input.EventID,
		input.SourceType,
		input.SourceEventID,
		input.AmountMinor,
		input.AssetCode,
		input.Currency,
		input.Timestamp,
		input.Direction,
		input.AccountRef,
		input.CounterpartyRef,
		input.Metadata,
	)
}

var ErrInvalidAmount = errors.New("amount must be positive")
var ErrUnsupportedCurrency = errors.New("unsupported currency")

func ValidateAmount(amount int64, currency string) error {
	if amount <= 0 {
		return fmt.Errorf("Error validating amount: %w", ErrInvalidAmount)
	}
	if currency != "USD" && currency != "EUR" && currency != "GBP" {
		return fmt.Errorf("Error validating currency: %w", ErrUnsupportedCurrency)
	}

	return nil
}
