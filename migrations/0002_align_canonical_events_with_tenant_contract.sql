ALTER TABLE canonical_events
    ADD COLUMN tenant_id TEXT NOT NULL,
    ADD COLUMN asset_code TEXT,
    ADD COLUMN counterparty_ref TEXT NOT NULL;

UPDATE canonical_events
SET metadata = '{}'::jsonb
WHERE metadata IS NULL;

ALTER TABLE canonical_events
    ALTER COLUMN metadata SET DEFAULT '{}'::jsonb,
    ALTER COLUMN metadata SET NOT NULL;

DROP INDEX IF EXISTS idx_events_source_type_status;
DROP INDEX IF EXISTS idx_events_account_ref;
DROP INDEX IF EXISTS idx_events_amount_currency;
DROP INDEX IF EXISTS idx_events_timestamp;

CREATE INDEX idx_events_tenant_source_type_status
    ON canonical_events (tenant_id, source_type, match_status);

CREATE INDEX idx_events_tenant_account_ref
    ON canonical_events (tenant_id, account_ref);

CREATE INDEX idx_events_tenant_amount_currency
    ON canonical_events (tenant_id, amount_minor, currency);

CREATE INDEX idx_events_tenant_timestamp
    ON canonical_events (tenant_id, event_timestamp);
