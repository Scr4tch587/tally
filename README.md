# Tally

A correctness-first Go core for multi-source financial transaction reconciliation — the foundation for a counterparty graph and operator-facing product described in [`docs/spec.md`](docs/spec.md).

Tally ingests canonical transaction events from independent sources (ledger, processor, bank), indexes pending candidates in Redis, scores cross-source pairs with a weighted matcher, and confirms matches in `SERIALIZABLE` Postgres transactions. A benchmark harness with seeded ground truth measures throughput, latency, match rate, and false-positive rate against the live HTTP ingestion path.

> **Status: early Phase 1.** The matching-and-measurement spine is working locally. Discrepancy handling, crash recovery, entity resolution, graph materialization, gRPC, product app, and deployment are not built yet. See [`docs/progress.md`](docs/progress.md) for the living checklist.

---

## What works today

| Area | Status |
|------|--------|
| `POST /events` ingestion with validation and idempotent insert | Done |
| Redis sorted-set candidate window (tenant × asset × amount bucket) | Done |
| Weighted amount/time/account scorer (`internal/match`) | Done |
| `SERIALIZABLE` match confirmation → `matches` / `match_events` | Done |
| Benchmark harness with ground-truth correctness checks | Done |
| Per-source connectors (ledger / processor / bank parsers) | Not started |
| Window expiry → discrepancies | Not started |
| Crash recovery (Redis rebuild from Postgres) | Not started |
| Entity resolution and counterparty graph | Not started |
| gRPC graph API, product app, deployment | Not started |

---

## The problem

The same real-world transaction appears across multiple independent systems — an internal ledger, a payment processor webhook, a bank settlement batch. Each source uses different identifiers, timestamps, and delivery semantics.

Reconciliation matches these observations back together. Tally's design constraint: **false positives are worse than unmatched events.** An unmatched event should eventually be explicit state for review; a false match silently buries a problem.

---

## Architecture (current)

```
┌──────────────────────────────────────────────────────────┐
│  Benchmark load generator (internal/loadgen + cmd/bench) │
│  Seeded ledger/processor pairs + decoy events            │
│  Ground-truth map for correctness validation             │
└────────────────────────┬─────────────────────────────────┘
                         │  POST /events (HTTP)
                         ▼
┌──────────────────────────────────────────────────────────┐
│  CORE HTTP API (chi) — cmd via main.go                   │
│                                                          │
│  1. Validate → CanonicalEvent (server-side idempotency) │
│  2. InsertEvent (ON CONFLICT DO NOTHING)                 │
│  3. AddCandidate → Redis ZSET                            │
│  4. FindCandidates (±120s window, ±1 minor-unit buckets) │
│  5. Score cross-source candidates, rank by score         │
│  6. ConfirmMatch (SERIALIZABLE tx) → Postgres            │
│  7. RemoveCandidate from Redis                           │
└───────────────┬──────────────────────┬───────────────────┘
                │                      │
         Postgres (durable)      Redis (candidate index)
    canonical_events            candidates:{tenant}:{asset}:{bucket}
    matches / match_events      member=event_id, score=timestamp_ms
```

Matching runs **synchronously inside each `POST /events` request**. There is no background reconciliation worker yet — concurrent out-of-order ingestion can leave true pairs unmatched until a pending-event sweep is added (see [Known limitations](#known-limitations)).

---

## Matching pipeline

When a new event is ingested (`internal/api/handlers.go`):

1. **Idempotent insert.** `InsertEvent` uses `ON CONFLICT DO NOTHING` on `idempotency_key` (`tenant_id:source_type:source_event_id`). Duplicate replays return `200` and skip Redis/matching.

2. **Candidate indexing.** The event is added to Redis sorted sets at keys `candidates:{tenant_id}:{asset}:{amount_bucket}` for the exact amount and adjacent buckets (`amount ± 1` minor unit). Score = event timestamp in Unix milliseconds.

3. **Candidate lookup.** `FindCandidates` queries the same buckets for members whose timestamp falls within ±120 seconds of the incoming event.

4. **Scoring.** Same-source candidates are skipped. Remaining pairs are scored (`internal/match/score.go`):

   ```
   score = 0.5 × amount_score + 0.3 × time_score + 0.2 × account_score
   ```

   - `amount_score`: 1.0 exact, linear decay to 0.0 at ±2 minor units
   - `time_score`: 1.0 within 5 s, linear decay to 0.0 at 120 s
   - `account_score`: 1.0 exact (case-insensitive), 0.5 substring, 0.0 otherwise

   Match confirms only if `score ≥ 0.85`.

5. **Confirmation.** Top-ranked candidate above threshold is confirmed in a `SERIALIZABLE` transaction (`internal/store/postgres.go`): both events must still be `PENDING`, same tenant; insert `matches` (with score + evidence JSON) and `match_events`; update both events to `MATCHED`. Serialization conflicts retry once.

6. **Cleanup.** Matched events are removed from Redis candidate sets.

---

## Benchmark harness

The harness lives in `cmd/bench`, `internal/loadgen`, and `internal/bench`. It generates deterministic datasets: true ledger/processor pairs plus decoys (same-source, amount skew, account mismatch, time skew). After posting events to the running server, it polls Postgres for confirmed matches and compares against the ground-truth map.

**Metrics measured:**

| Metric | How |
|--------|-----|
| Throughput | Events posted / wall-clock duration |
| Match rate | Confirmed true matches / expected true pairs |
| False positive rate | Confirmed matches not in ground truth |
| Latency p50/p95/p99 | Ms from when both events' POST completes to first DB observation of the match |

A run is **clean** when match rate = 100%, false positives = 0, missed matches = 0, and HTTP errors = 0.

**Best measured clean run** (2026-06-04, local Docker Postgres/Redis, paired arrival, **1 worker**): 160 true pairs / 832 total events (40% decoy ratio), **237 events/sec**, **8 ms p99** reconcile latency, **100% match rate**, **0 false positives**.

```bash
# Start dependencies and apply migrations
docker compose up -d
make migrate

# Start the server (separate terminal)
go run .

# Correctness gate (defaults: 1000 pairs, 16 workers — use WORKERS=1 for clean runs)
make bench WORKERS=1 PAIRS=160 ARRIVAL=paired

# Stepped load search for largest clean run under p99 ≤ 250 ms
make bench-load WORKERS=1 ARRIVAL=paired
```

Reports are written to `bench-results/latest.json` by default.

---

## Known limitations

- **Concurrent ingestion.** Request-local matching can miss true pairs when both halves arrive in parallel before the counterpart is indexed. Measured: shuffled 16-worker / 100-pair run → 91% match rate; paired 16-worker / 100-pair → 55%. The planned fix is a separate pending-event reconciliation sweep decoupled from the request lifecycle.
- **No discrepancy path.** Events that never match stay `PENDING`; there is no window-expiry sweep or `discrepancies` table yet.
- **No crash recovery.** Redis is designed as a rebuildable cache, but startup rebuild from Postgres is not implemented.
- **Redis removal is post-commit.** Not part of the Postgres transaction; a crash between commit and Redis cleanup leaves stale index entries until recovery exists.

---

## HTTP API

| Endpoint | Description |
|----------|-------------|
| `POST /events` | Ingest a canonical event; runs matching inline |
| `GET /events/{eventID}` | Fetch a canonical event by ID |
| `GET /health` | Postgres + Redis connectivity check |

Planned but not implemented: `GET /metrics/current`, `GET /metrics/history`, gRPC graph queries.

---

## Data model

**`canonical_events`** — every ingested event. Tenant-scoped with `match_status` (`PENDING` or `MATCHED`). Unique index on `idempotency_key`.

**`matches`** — confirmed match rows with `match_score` and `evidence` (JSONB scoring breakdown).

**`match_events`** — junction linking each match to its two (or eventually N) canonical events.

Not yet migrated: `discrepancies`, `metric_snapshots`, counterparty graph tables (`counterparty_nodes`, `counterparty_edges`, `graph_events`, etc.).

---

## Project layout

```
tally/
  cmd/bench/           # Benchmark harness binary
  internal/
    api/               # HTTP handlers and routes
    bench/             # Correctness, latency, report computation
    event/             # CanonicalEvent contract and validation
    loadgen/           # Deterministic benchmark dataset generation
    match/             # Weighted scoring function
    store/             # Postgres and Redis access
  migrations/          # Postgres schema (canonical_events, matches)
  docs/
    spec.md            # Full product + architecture spec
    progress.md        # Implementation tracker
    coach.md           # Development conventions
```

---

## Running locally

**Prerequisites:** Go 1.26+, Docker, `psql` (for migrations)

```bash
docker compose up -d
make migrate
go run .
```

Run tests (requires Postgres and Redis):

```bash
go test ./...
```

---

## Key design decisions

**No floats on money.** Amounts are `int64` minor units throughout.

**Serializable isolation for match confirmation.** Two concurrent requests cannot both match the same event; integration tests cover replay and race behavior.

**Redis as a narrow index, Postgres as truth.** Candidate lookup is fast; durable state and match decisions live in Postgres.

**Amount bucketing with adjacency.** Exact and ±1 minor-unit buckets catch small fee-rounding differences without unbounded scans.

**Idempotency at the database layer.** `ON CONFLICT DO NOTHING` makes ingestion replays safe without application-level dedup logic.

**Conservative scoring threshold.** A 1 minor-unit amount gap scores 0.75 — below the 0.85 threshold — prioritizing precision over recall.

---

## Tech stack

| Component | Choice |
|-----------|--------|
| Language | Go |
| HTTP router | chi |
| Postgres driver | pgx/v5 (`SERIALIZABLE` transactions) |
| Postgres | 16 |
| Redis | 7 (sorted sets for candidate windowing) |
| Logging | zerolog |

Planned (per spec, not wired): OpenTelemetry, gRPC, Next.js product surface, AWS CDK / EKS Fargate.

---

## Documentation

- [`docs/spec.md`](docs/spec.md) — full architecture, graph model, and roadmap
- [`docs/progress.md`](docs/progress.md) — what's done, blockers, and latest verification
- [`docs/coach.md`](docs/coach.md) — handwrite vs generate zones and coaching notes
