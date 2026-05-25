package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tally/internal/event"
)

func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, "postgres://tally:tally@localhost:5432/tally")
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func InsertEvent(ctx context.Context, pool *pgxpool.Pool, event *event.CanonicalEvent) (bool, error) {
	tag, err := pool.Exec(ctx, "INSERT INTO canonical_events (tenant_id, event_id, source_type, source_event_id, amount_minor, currency, asset_code, event_timestamp, direction, account_ref, counterparty_ref, idempotency_key, metadata) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) ON CONFLICT DO NOTHING", event.TenantID, event.EventID, event.SourceType, event.SourceEventID, event.AmountMinor, event.Currency, event.AssetCode, event.Timestamp, event.Direction, event.AccountRef, event.CounterpartyRef, event.IdempotencyKey, event.Metadata)
	if tag.RowsAffected() == 0 {
		return false, err
	} else {
		return true, err
	}
}

func GetEvent(ctx context.Context, pool *pgxpool.Pool, eventID string) (*event.CanonicalEvent, error) {
	ev := &event.CanonicalEvent{}
	err := pool.QueryRow(
		ctx,
		`SELECT tenant_id, event_id, source_type, source_event_id, amount_minor, currency, asset_code,
		        event_timestamp, ingested_at, direction, account_ref, counterparty_ref, metadata, idempotency_key
		   FROM canonical_events
		  WHERE event_id = $1`,
		eventID,
	).Scan(
		&ev.TenantID,
		&ev.EventID,
		&ev.SourceType,
		&ev.SourceEventID,
		&ev.AmountMinor,
		&ev.Currency,
		&ev.AssetCode,
		&ev.Timestamp,
		&ev.IngestedAt,
		&ev.Direction,
		&ev.AccountRef,
		&ev.CounterpartyRef,
		&ev.Metadata,
		&ev.IdempotencyKey,
	)
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func ConfirmMatch(ctx context.Context, pool *pgxpool.Pool, eventA, eventB string) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, "SELECT match_status FROM canonical_events WHERE event_id = $1", eventA).Scan(&status)
	if err != nil {
		return err
	}

	if status != "PENDING" {
		return fmt.Errorf("Status of event is not pending: %s", eventA)
	}

	err = tx.QueryRow(ctx, "SELECT match_status FROM canonical_events WHERE event_id = $1", eventB).Scan(&status)
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
