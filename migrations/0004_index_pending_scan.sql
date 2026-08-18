CREATE INDEX IF NOT EXISTS idx_events_pending_scan
    ON canonical_events (ingested_at, event_id)
    WHERE match_status = 'PENDING';
