package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func deterministicMatchID(eventA, eventB string) string {
	if eventA < eventB {
		return eventA + ":" + eventB
	}
	return eventB + ":" + eventA
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

func ConfirmMatch(ctx context.Context, pool *pgxpool.Pool, eventA, eventB string, score float64, evidence map[string]any) error {
	if eventA == eventB {
		return fmt.Errorf("cannot match event to itself: %s", eventA)
	}

	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	if evidence == nil {
		evidenceJSON = []byte("{}")
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastErr = confirmMatchOnce(ctx, pool, eventA, eventB, score, evidenceJSON)
		if lastErr == nil || !isSerializationFailure(lastErr) || attempt == 1 {
			return lastErr
		}
	}
	return lastErr
}

func confirmMatchOnce(ctx context.Context, pool *pgxpool.Pool, eventA, eventB string, score float64, evidenceJSON []byte) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var statusA, tenantA string
	err = tx.QueryRow(ctx, "SELECT match_status, tenant_id FROM canonical_events WHERE event_id = $1", eventA).Scan(&statusA, &tenantA)
	if err != nil {
		return err
	}
	if statusA != "PENDING" {
		return fmt.Errorf("event %s is not pending", eventA)
	}

	var statusB, tenantB string
	err = tx.QueryRow(ctx, "SELECT match_status, tenant_id FROM canonical_events WHERE event_id = $1", eventB).Scan(&statusB, &tenantB)
	if err != nil {
		return err
	}
	if statusB != "PENDING" {
		return fmt.Errorf("event %s is not pending", eventB)
	}
	if tenantA != tenantB {
		return fmt.Errorf("events %s and %s belong to different tenants", eventA, eventB)
	}

	matchID := deterministicMatchID(eventA, eventB)
	_, err = tx.Exec(ctx,
		`INSERT INTO matches (match_id, tenant_id, match_score, evidence)
		 VALUES ($1, $2, $3, $4)`,
		matchID, tenantA, score, evidenceJSON,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO match_events (match_id, event_id) VALUES ($1, $2), ($1, $3)`,
		matchID, eventA, eventB,
	)
	if err != nil {
		return err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE canonical_events
		    SET match_status = 'MATCHED'
		  WHERE event_id IN ($1, $2)
		    AND match_status = 'PENDING'`,
		eventA, eventB,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 2 {
		return fmt.Errorf("expected 2 events updated to MATCHED, got %d", tag.RowsAffected())
	}

	return tx.Commit(ctx)
}
