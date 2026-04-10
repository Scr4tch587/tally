CREATE TABLE canonical_events (
    event_id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_event_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency TEXT NOT NULL,
    event_timestamp TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    direction TEXT NOT NULL,
    account_ref TEXT NOT NULL,
    metadata JSONB,
    idempotency_key TEXT NOT NULL UNIQUE,
    match_status TEXT NOT NULL DEFAULT 'PENDING'
);

CREATE INDEX idx_events_source_type_status ON canonical_events (source_type, match_status);
CREATE INDEX idx_events_account_ref ON canonical_events (account_ref);
CREATE INDEX idx_events_amount_currency ON canonical_events (amount_minor, currency);
CREATE INDEX idx_events_timestamp ON canonical_events (event_timestamp);
