package store

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"context"
	"tally/internal/event"
	"time"
	"fmt"
	"github.com/jackc/pgx/v5"
)

func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, "postgres://tally:tally@localhost:5432/tally")
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func InsertEvent(ctx context.Context, pool *pgxpool.Pool, event *event.CanonicalEvent) error {
	_, err := pool.Exec(ctx, "INSERT INTO canonical_events VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING", event.EventID, event.SourceType, event.AmountMinor, event.Currency, event.EventID)
	return err
}

func GetEvent(ctx context.Context, pool *pgxpool.Pool, eventID string) (*event.CanonicalEvent, error) {
	var sourceType string
	var amountMinor int64
	var currency string
	err := pool.QueryRow(ctx, "SELECT source_type, amount_minor, currency FROM canonical_events WHERE event_id = $1", eventID).Scan(&sourceType, &amountMinor, &currency)
	if err != nil {
		return nil, err
	}
	event, err := event.NewCanonicalEvent(eventID, sourceType, amountMinor, currency, time.Now())
	if err != nil {
		return nil, err
	}
	return event, nil
}

func ConfirmMatch(ctx context.Context, pool *pgxpool.Pool, eventA, eventB string) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, "SELECT status FROM canonical_events WHERE event_id = $1", eventA).Scan(&status)
	if err != nil {
		return err
	}

	if status != "PENDING" {
		return fmt.Errorf("Status of event is not pending: %s", eventA)
	}
	
	err = tx.QueryRow(ctx, "SELECT status FROM canonical_events WHERE event_id = $1", eventB).Scan(&status)
	if err != nil {
		return err
	}

	if status != "PENDING" {
		return fmt.Errorf("Status of event is not pending: %s", eventB)
	}

	_, err = tx.Exec(ctx, "INSERT INTO matches VALUES ($1, $2, $3)", fmt.Sprintf("%s-%s", eventA, eventB), eventA, eventB)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, "UPDATE canonical_events SET status = 'MATCHED' WHERE event_id = $1 or event_id = $2", eventA, eventB)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}