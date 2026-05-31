CREATE TABLE matches (
    match_id     TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    match_score  DOUBLE PRECISION NOT NULL,
    evidence     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE match_events (
    match_id  TEXT NOT NULL REFERENCES matches (match_id),
    event_id  TEXT NOT NULL REFERENCES canonical_events (event_id),
    PRIMARY KEY (match_id, event_id)
);

CREATE INDEX idx_match_events_event_id ON match_events (event_id);

CREATE INDEX idx_matches_tenant_created ON matches (tenant_id, created_at DESC);
