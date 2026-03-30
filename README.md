# Tally

A streaming financial reconciliation engine built in Go.

Tally ingests transaction events from multiple independent sources, matches them using a weighted scoring function within a configurable time window, and surfaces discrepancies in near-real-time — with a benchmark harness that proves throughput, latency, and correctness under failure injection.

> **Status: Work in progress.** Core data model, Postgres store, and canonical event layer are functional. Matching engine, load generator, and API are in progress.

---

## The problem

In any payment system, the same real-world transaction appears across multiple independent systems — an internal ledger sees it immediately, a payment processor webhook arrives seconds later, and a bank settlement batch lands hours after that. Each source uses different identifiers, different timestamp semantics, and occasionally drops or duplicates events.

Reconciliation is the process of matching these views back to each other. Done wrong, it either floods operations teams with false alerts or — worse — silently hides real discrepancies like a ledger entry with no settlement, or a settlement with no corresponding charge.

Tally's design constraint: **false positives are worse than unmatched events.** An unmatched event gets flagged for human review. A false match silently buries a problem.

---

## Architecture

```
┌─────────────────────────────────────────────┐
│           Load Generator (Go)               │
│  1,000 TPS · 3 correlated sources           │
│  Configurable: delay, drop rate, jitter,    │
│  duplicates, amount mismatches, bursts      │
│  Ground-truth table for correctness check   │
└──────┬──────────┬──────────┬────────────────┘
       │          │          │
   Source A    Source B    Source C
   (ledger)  (processor)  (bank batch)
       │          │          │
┌──────▼──────────▼──────────▼────────────────┐
│          Ingestion Layer (Go)               │
│  Per-source connectors                      │
│  Parse → normalize → idempotency check      │
│  → CanonicalEvent → pipeline                │
└──────────────────┬──────────────────────────┘
                   │
          CanonicalEvent stream
                   │
┌──────────────────▼──────────────────────────┐
│      Streaming Matching Engine (Go)         │
│                                             │
│  Redis sorted sets — candidate window       │
│  Keyed by (currency, amount_bucket)         │
│  Scored by timestamp (ms)                   │
│                                             │
│  Weighted scorer:                           │
│    0.5 × amount_score                       │
│    0.3 × time_score                         │
│    0.2 × account_score                      │
│  Match threshold: 0.85                      │
│                                             │
│  Match confirmation: SERIALIZABLE tx        │
│  Window expiry: background goroutine        │
│  Late arrival: retroactive resolution       │
└──────┬──────────────────────┬───────────────┘
       │                      │
  Postgres write          Age-out → discrepancy
  (match + status update)
       │                      │
┌──────▼──────────────────────▼───────────────┐
│          Query API (Go, chi)                │
│  GET /matches · /discrepancies · /metrics   │
│  GET /events/{id} · /health                 │
└─────────────────────────────────────────────┘
```

---

## Components

### Load generator

A first-class Go binary — not a throw-away script. It maintains a **ground-truth table** mapping each simulated real-world transaction to the exact events each source should produce. The benchmark harness uses this table to validate that Tally's matches are correct, not just plausible.

Configurable per source:

| Source | Delay | Drop rate | Notes |
|--------|-------|-----------|-------|
| **Ledger** (A) | 0–5 ms | 0% | Internal system; clean JSON format, always complete |
| **Processor** (B) | 0–30 s | 0.1% | Stripe-like webhooks; ±1 minor unit fee rounding |
| **Bank** (C) | 30–90 s | 0% | CSV batch every 60 s; settlement time ≠ transaction time; 0.05% duplicate rate |

Global error injection: missing events, amount mismatches, and duplicates at configurable rates with a fixed seed for reproducibility.

---

### Ingestion layer

One connector per source type. Each connector is fully isolated — it knows nothing about matching logic. Its only job:

1. Parse the source-specific format (JSON for ledger and processor, CSV for bank)
2. Normalize to `CanonicalEvent` (amounts → minor units as `int64`, timestamps → UTC, account refs → lowercase stripped)
3. Compute `idempotency_key = source_type:source_event_id`
4. Write to Postgres with `ON CONFLICT DO NOTHING` — replaying events is always safe

---

### Streaming matching engine

The core of the system. When a canonical event is ingested:

**Step 1 — Candidate lookup.** Query Redis sorted sets for candidates from *other* source types in the matching amount bucket and adjacent buckets (exact ±1 minor unit, to catch fee rounding without a full scan).

**Step 2 — Scoring.** For each candidate:

```
score = 0.5 × amount_score + 0.3 × time_score + 0.2 × account_score
```

- `amount_score`: 1.0 for exact match, linear decay to 0.0 at ±2 minor units
- `time_score`: 1.0 within `min_time_delta`, linear decay to 0.0 at 120 s
- `account_score`: 1.0 exact, 0.5 substring, 0.0 otherwise

**Step 3 — Confirmation.** If the top candidate scores ≥ 0.85:

1. Begin a `SERIALIZABLE` Postgres transaction
2. Assert both events are still `PENDING` (guards against concurrent match races)
3. Insert into `matches` and `match_events`, update both events to `MATCHED`
4. Remove matched candidate from Redis
5. Commit — record match latency metric

On serialization conflict: retry once, then drop. The other goroutine's match wins.

**Step 4 — No match.** Add the event to its Redis sorted set bucket to wait for a counterpart.

**Window expiry.** A background goroutine scans for candidates older than 120 s. Expired events are removed from Redis and written to Postgres as `DISCREPANCY` records with type `MISSING_COUNTERPART`.

**Late arrivals.** On ingestion, if a plausible counterpart is already a discrepancy, Tally attempts a retroactive match. If confirmed: the discrepancy is marked `AUTO_RESOLVED`. This models realistic bank settlement delays.

---

### Crash recovery

Redis is a **rebuildable cache**, not the source of truth. On startup:

1. Scan Postgres for `match_status = 'PENDING'` events within the current window
2. Rebuild Redis sorted sets from that scan
3. Run expiry catch-up for anything that aged out during downtime

Losing Redis is a cold-cache performance hit, not a correctness failure. Postgres is always authoritative.

---

### Observability

**Structured logging** (zerolog): every log line carries `correlation_id`, `source_type`, `event_id`, and stage (`ingested` → `candidate_added` → `match_confirmed` / `discrepancy_created` / `discrepancy_resolved`).

**OpenTelemetry tracing**: spans across the full ingestion and matching path.

**Metrics** (internal + queryable via API):

| Category | Metrics |
|----------|---------|
| Throughput | `events_ingested_total` by source, `matches_confirmed_total`, `discrepancies_opened_total` by type |
| Latency | `match_latency_ms` histogram (p50/p95/p99), `ingestion_latency_ms` histogram |
| Window health | `pending_window_size`, `window_expiry_total`, `late_arrival_resolution_total` |
| Quality | `match_rate` (rolling 60 s) |

Every 10 seconds, a metric snapshot row is written to Postgres for the benchmark harness to read.

---

### Query API

Thin HTTP layer (chi) over Postgres. Exists for the benchmark harness and debugging.

| Endpoint | Description |
|----------|-------------|
| `GET /matches` | Paginated matches with scores and linked event IDs |
| `GET /discrepancies` | Filterable by type and resolution status |
| `GET /events/{id}` | Canonical event with current match status |
| `GET /metrics/current` | Live throughput, latency percentiles, match rate, window size |
| `GET /metrics/history` | Metric snapshots for a time range |
| `GET /health` | Postgres + Redis connectivity, current window size |

---

### Benchmark harness

Runs a parameterized load test end-to-end and produces a report with:

- **Throughput**: events/s sustained, peak, and by source
- **Latency**: p50/p95/p99 match latency from second-event ingestion to match confirmation
- **Correctness**: match rate against ground-truth table, false positive count, undetected discrepancy count
- **Recovery**: re-runs with Redis cleared mid-test to validate crash recovery path

Every metric claimed is measured — nothing estimated.

---

## Data model

**`canonical_events`** — every ingested event. `match_status` tracks lifecycle: `PENDING → MATCHED | DISCREPANCY`. Unique index on `idempotency_key` enforces deduplication at the database level.

**`matches` + `match_events`** — confirmed matches. A junction table (not two columns) supports N-to-M partial matches where two source-B events sum to one source-A event — rare but real, and avoids a schema migration later.

**`discrepancies`** — aged-out events with type (`MISSING_COUNTERPART | AMOUNT_MISMATCH | DUPLICATE_DETECTED | LATE_ARRIVAL`) and resolution tracking. Not terminal: `resolved_at` is set on auto-resolution.

**`metric_snapshots`** — periodic metric rows consumed by the benchmark harness.

---

## Key design decisions

**No floats, ever.** All amounts are `int64` minor units (cents, pence). Floating-point arithmetic on money is a correctness bug, not a performance tradeoff.

**Serializable isolation for match confirmation.** Two goroutines racing to match the same event pair must not both succeed. `SERIALIZABLE` transactions make this impossible without application-level locking.

**Redis is a cache, not the source of truth.** The candidate window lives in Redis for speed, but it's fully rebuildable from Postgres. This means a Redis failure causes latency degradation, not data loss.

**Amount bucketing with adjacency.** Placing each event in its exact bucket and adjacent buckets (±1 minor unit) catches fee-rounding mismatches without scanning unbounded candidate sets.

**Idempotency at the database layer.** `ON CONFLICT DO NOTHING` on `idempotency_key` means the ingestion layer never needs to check for duplicates — the constraint handles it, and retries are always safe.

**Discrepancies are not terminal.** Late bank settlements are expected in real systems. Marking discrepancies resolvable and handling late arrivals explicitly makes Tally's correctness model match how production reconciliation actually works.

---

## Running locally

**Prerequisites:** Go 1.22+, Docker

```bash
# Start Postgres and Redis
docker compose up -d

# Apply schema migrations
make migrate

# Run
go run main.go
```

---

## Tech stack

| Component | Choice | Why |
|-----------|--------|-----|
| Language | Go | Concurrency model fits a streaming pipeline. Strong systems signal. |
| Router | chi | Minimal, idiomatic, no magic. |
| Postgres driver | pgx/v5 | Raw SQL, no ORM. Connection pooling built in. Supports `SERIALIZABLE` isolation. |
| Postgres | 16+ | `SERIALIZABLE` transactions for match correctness. Window functions for analytics. JSONB for metadata. |
| Redis | 7+ | Sorted sets for candidate windowing. Sub-ms lookups. Rebuildable from Postgres (cache, not source of truth). |
| Logging | zerolog | Structured JSON, zero-allocation. Standard in Go systems. |
| Tracing | OpenTelemetry → stdout / Jaeger | Vendor-neutral. Exports to Jaeger locally or X-Ray on AWS. |
| Containerization | Docker + docker-compose | Local dev. Postgres + Redis + engine in one `docker compose up`. |
| Orchestration | Kubernetes / EKS Fargate | Planned (later phases). Pod scaling, HPA, crash recovery under real orchestration. |
| Infra | AWS CDK (TypeScript) | Planned (later phases). Deploy for scale testing, tear down after. |
| CI | GitHub Actions | Lint, test, bench on PR. |
