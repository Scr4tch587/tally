## 0) One-sentence goal

Build a **streaming financial reconciliation engine** (Go) that ingests transaction events from multiple independent sources, performs scoring-based fuzzy matching within configurable time windows, and surfaces discrepancies in near-real-time — with reproducible benchmarks proving throughput, latency, and correctness under failure.

---

## 1) Hard rules (non-negotiable)

### Correctness

- **Every confirmed match is correct**: a matched pair must reference the same real-world transaction. False positives are worse than unmatched events (an unmatched event gets flagged for review; a false match silently hides a problem).
- **No event is silently lost**: every ingested event either matches, ages out into a discrepancy, or is explicitly in the pending window. Crash-recovery must preserve this invariant.
- **Idempotent processing**: replaying the same event produces no duplicate matches, no duplicate discrepancies, and no state corruption.

### Observability

- **Every metric claimed on the resume is measured by the benchmark harness**, not estimated. If we can't measure it, we don't claim it.
- **Structured logging and tracing on every path** — ingestion, matching, discrepancy creation, API queries.

### Separation of concerns

- Ingestion connectors know nothing about matching logic.
- The matching engine knows nothing about source-specific formats.
- The canonical event format is the contract between them.

---

## 2) High-level architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              Load Generator (Go)            │
                    │  Simulates 3 sources with correlated events │
                    └──────┬──────────┬──────────┬────────────────┘
                           │          │          │
                    Source A    Source B    Source C
                    (ledger)   (processor) (bank batch)
                           │          │          │
                    ┌──────▼──────────▼──────────▼────────────────┐
                    │           Ingestion Layer (Go)              │
                    │  Per-source connectors → canonical events   │
                    └──────────────────┬──────────────────────────┘
                                       │
                              Canonical events
                                       │
                    ┌──────────────────▼──────────────────────────┐
                    │        Streaming Matching Engine (Go)       │
                    │  Redis window ← candidates                 │
                    │  Scoring → match/pending/discard            │
                    │  Postgres ← confirmed matches              │
                    └──────┬──────────────────────┬───────────────┘
                           │                      │
                    Matches written          Aged-out events
                    to Postgres              become discrepancies
                           │                      │
                    ┌──────▼──────────────────────▼───────────────┐
                    │          Query API (Go, chi)                │
                    │  GET /matches, /discrepancies, /metrics     │
                    └─────────────────────────────────────────────┘
```

### Components

**Load generator** — Go program that simulates realistic multi-source transaction flows. Generates correlated event pairs/triples across sources with configurable: volume (events/sec), inter-source delay distribution, error injection rate (missing events, amount mismatches, duplicates), and burst patterns.

**Ingestion layer** — one connector per source type. Each connector: parses source-specific format, normalizes to canonical event, deduplicates (idempotency key per source), and writes to the ingestion pipeline.

**Streaming matching engine** — the core. Consumes canonical events, maintains a window of unmatched candidates in Redis (sorted sets keyed by amount bucket, scored by timestamp), scores incoming events against candidates, confirms matches above threshold, expires unmatched events past the window into discrepancies.

**Postgres state store** — all durable state. Canonical events, confirmed matches, discrepancies, metrics snapshots. The matching engine writes here transactionally.

**Query API** — thin HTTP layer over Postgres. Serves match results, discrepancy lists, and metric summaries. Not the focus of the project but necessary for the benchmark harness to verify correctness.

**Benchmark harness** — runs load generator at parameterized volume, collects metrics from the engine, validates correctness invariants (no lost events, no false matches under controlled conditions), and outputs a reproducible summary with throughput, p50/p95/p99 latency, match rate, and discrepancy detection time.

---

## 3) Repo structure

```
tally/
  cmd/
    engine/          # main entry point — runs ingestion + matcher + API
    loadgen/         # load generator binary
    bench/           # benchmark harness binary
  internal/
    canonical/       # canonical event types and interfaces
    ingestion/
      ledger/        # source A connector
      processor/     # source B connector
      bank/          # source C connector
    matcher/
      scorer.go      # scoring function (HANDWRITE)
      window.go      # windowing + candidate management
      engine.go      # orchestration loop
    reconcile/
      discrepancy.go # classification + aging logic
    store/
      postgres.go    # pgx queries, migrations
      redis.go       # sorted set operations for match window
    idempotency/     # dedup logic
    observe/
      metrics.go     # metric collection
      tracing.go     # OpenTelemetry setup
    api/
      handlers.go    # chi routes + handlers
  migrations/        # Postgres schema (SQL files)
  configs/           # YAML configs for engine, loadgen, bench
  scripts/
    bench_report.sh  # formats benchmark output
  docker-compose.yml
  Makefile           # build, test, bench, migrate targets
  docs/
    ARCHITECTURE.md
    DECISIONS.md     # log of architectural decisions with rationale
    BENCHMARKS.md    # latest benchmark results
```

---

## 4) Canonical event format

This is the internal contract. All connectors normalize to this. The matching engine only sees this type.

```go
type CanonicalEvent struct {
    EventID       string    // globally unique, assigned by connector
    SourceType    string    // "ledger" | "processor" | "bank"
    SourceEventID string    // original ID from the source system
    AmountMinor   int64     // amount in minor units (cents)
    Currency      string    // ISO 4217
    Timestamp     time.Time // when the transaction occurred (source-reported)
    IngestedAt    time.Time // when Tally received it
    Direction     string    // "debit" | "credit"
    AccountRef    string    // normalized account/wallet reference
    Metadata      map[string]string // source-specific fields preserved for debugging
    IdempotencyKey string   // source_type + source_event_id
}
```

### Normalization rules

- All amounts converted to minor units (cents/pence) as int64. No floats ever.
- All timestamps normalized to UTC.
- Account references normalized to a canonical format (strip prefixes, lowercase).
- Currency codes uppercased, validated against a known set.

---

## 5) Data model (Postgres)

### 5.1 canonical_events

```sql
CREATE TABLE canonical_events (
    event_id        TEXT PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_event_id TEXT NOT NULL,
    amount_minor    BIGINT NOT NULL,
    currency        TEXT NOT NULL,
    event_timestamp TIMESTAMPTZ NOT NULL,
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    direction       TEXT NOT NULL,
    account_ref     TEXT NOT NULL,
    metadata        JSONB,
    idempotency_key TEXT NOT NULL UNIQUE,
    match_status    TEXT NOT NULL DEFAULT 'PENDING'
    -- PENDING | MATCHED | DISCREPANCY
);

CREATE INDEX idx_events_source_type_status ON canonical_events (source_type, match_status);
CREATE INDEX idx_events_account_ref ON canonical_events (account_ref);
CREATE INDEX idx_events_amount_currency ON canonical_events (amount_minor, currency);
CREATE INDEX idx_events_timestamp ON canonical_events (event_timestamp);
```

### 5.2 matches

```sql
CREATE TABLE matches (
    match_id     TEXT PRIMARY KEY,
    score        REAL NOT NULL,
    matched_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    match_type   TEXT NOT NULL
    -- EXACT | FUZZY | PARTIAL
);
```

### 5.3 match_events (junction table)

```sql
CREATE TABLE match_events (
    match_id TEXT NOT NULL REFERENCES matches(match_id),
    event_id TEXT NOT NULL REFERENCES canonical_events(event_id),
    PRIMARY KEY (match_id, event_id)
);
```

Why a junction table instead of just two columns on `matches`: partial matches (one event on source A maps to two on source B that sum to the same amount) need N events per match. This is rare but supporting it from the start avoids a schema migration later.

### 5.4 discrepancies

```sql
CREATE TABLE discrepancies (
    discrepancy_id TEXT PRIMARY KEY,
    event_id       TEXT NOT NULL REFERENCES canonical_events(event_id),
    type           TEXT NOT NULL,
    -- MISSING_COUNTERPART | AMOUNT_MISMATCH | DUPLICATE_DETECTED | LATE_ARRIVAL
    detected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at    TIMESTAMPTZ,
    resolution     TEXT
    -- NULL | AUTO_RESOLVED | MANUAL
);

CREATE INDEX idx_discrepancies_type ON discrepancies (type);
CREATE INDEX idx_discrepancies_unresolved ON discrepancies (resolved_at) WHERE resolved_at IS NULL;
```

### 5.5 metric_snapshots

```sql
CREATE TABLE metric_snapshots (
    snapshot_id    BIGSERIAL PRIMARY KEY,
    recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    window_start   TIMESTAMPTZ NOT NULL,
    window_end     TIMESTAMPTZ NOT NULL,
    events_ingested BIGINT NOT NULL,
    matches_confirmed BIGINT NOT NULL,
    discrepancies_opened BIGINT NOT NULL,
    match_rate     REAL NOT NULL,
    p50_match_latency_ms REAL,
    p95_match_latency_ms REAL,
    p99_match_latency_ms REAL,
    pending_window_size  BIGINT NOT NULL
);
```

---

## 6) Simulated sources (load generator)

The load generator is not a throwaway script. It's a first-class Go program that produces realistic, reproducible transaction flows.

### Source A: Internal Ledger

- Emits events immediately (near-zero delay from "real" transaction time).
- Clean format, always has all fields.
- Represents: your own system's view of what happened.
- Event format before normalization: JSON with `transaction_id`, `wallet_from`, `wallet_to`, `amount`, `currency`, `created_at`.

### Source B: Payment Processor (simulated Stripe-like webhooks)

- Emits events with 0–30 second delay (configurable distribution, default: uniform).
- Occasionally drops events entirely (configurable rate, default: 0.1%).
- Amount may differ by ±1 minor unit due to fee rounding.
- Represents: a third-party processor's view.
- Event format before normalization: JSON with `charge_id`, `merchant_account`, `amount_cents`, `fee_cents`, `net_amount_cents`, `currency`, `processed_at`.

### Source C: Bank Settlement (batch file)

- Emits events in batches at configurable intervals (default: every 60 seconds).
- Batch contains all transactions settled in that window.
- Timestamps are settlement time, not original transaction time (offset of 30–90 seconds from real time).
- Occasionally includes duplicate entries (configurable rate, default: 0.05%).
- Represents: the bank's settlement view.
- Event format before normalization: CSV rows with `settlement_id`, `ref_number`, `debit_credit`, `amount`, `ccy`, `value_date`, `settlement_time`.

### Correlation model

The load generator maintains a ground truth table: for each simulated real-world transaction, it knows exactly which events each source should produce. This ground truth is passed to the benchmark harness for correctness validation — the harness can compare Tally's matches against the known-correct pairings.

### Configurable parameters

```yaml
loadgen:
  target_tps: 1000           # transactions per second (each produces 1-3 source events)
  duration_seconds: 300       # run duration
  seed: 42                    # reproducible randomness
  sources:
    ledger:
      delay_ms: { min: 0, max: 5 }
      drop_rate: 0.0
    processor:
      delay_ms: { min: 0, max: 30000 }
      drop_rate: 0.001
      amount_jitter_minor: 1
    bank:
      batch_interval_ms: 60000
      delay_ms: { min: 30000, max: 90000 }
      duplicate_rate: 0.0005
  error_injection:
    missing_events_rate: 0.001
    amount_mismatch_rate: 0.002
    duplicate_rate: 0.0005
```

---

## 7) Matching engine design

### 7.1 Candidate windowing (Redis)

When a canonical event is ingested and no immediate match is found, it enters the **candidate window** in Redis.

Structure: Redis sorted set per `(currency, amount_bucket)` pair.

- Key: `candidates:{currency}:{amount_bucket}`
- Member: `event_id`
- Score: event timestamp (Unix millis)

**Amount bucketing**: Events are placed in the bucket for their exact amount AND adjacent buckets (±1 minor unit) to support fuzzy amount matching without scanning all candidates.

**Window expiry**: A background goroutine periodically scans for candidates older than `max_window_duration` (default: 120 seconds). Expired candidates are removed from Redis and written to Postgres as discrepancies.

### 7.2 Scoring function (HANDWRITE zone)

When a new event arrives, the engine queries Redis for candidates from OTHER source types in the matching amount buckets. For each candidate pair, compute a match score:

```
score = w_amount * amount_score + w_time * time_score + w_account * account_score
```

**amount_score**: 1.0 if exact match, decays linearly to 0.0 at `max_amount_tolerance` (default: 2 minor units).

**time_score**: 1.0 if timestamps within `min_time_delta`, decays linearly to 0.0 at `max_time_delta` (default: 120 seconds).

**account_score**: 1.0 if account refs match exactly, 0.5 if one is a substring of the other, 0.0 otherwise.

Default weights: `w_amount = 0.5, w_time = 0.3, w_account = 0.2`.

Match is confirmed if `score >= match_threshold` (default: 0.85).

If multiple candidates score above threshold, take the highest. If tied, take the earliest (prefer older candidates to keep the window small).

### 7.3 Match confirmation flow

When a match is confirmed:

1. Begin Postgres transaction (SERIALIZABLE).
2. Verify both events still have `match_status = 'PENDING'` (guard against race conditions).
3. Insert into `matches` and `match_events`.
4. Update both events' `match_status` to `'MATCHED'`.
5. Remove matched candidate from Redis.
6. Commit.
7. Record match latency metric (time from second event's ingestion to match confirmation).

If the transaction fails due to serialization conflict (another goroutine matched one of the events), retry once, then drop — the other match wins.

### 7.4 Late arrival handling

An event arrives after its expected counterpart has already been marked as a discrepancy:

1. On ingestion, check if a `DISCREPANCY` exists for a plausible counterpart.
2. If found: attempt to match. If match confirms, resolve the discrepancy (`resolved_at = now()`, `resolution = 'AUTO_RESOLVED'`).
3. Record this as a late arrival in metrics.

This means discrepancies are not terminal — they can be resolved retroactively. This is realistic (bank settlements are often late) and gives a good interview story about eventual consistency.

---

## 8) Idempotency and crash recovery (HANDWRITE zone)

### 8.1 Ingestion idempotency

Every canonical event has an `idempotency_key` = `source_type:source_event_id`. The `UNIQUE` constraint on `canonical_events.idempotency_key` prevents duplicate ingestion. On conflict, return the existing event — do not error.

### 8.2 Match idempotency

The SERIALIZABLE transaction in section 7.3 is the idempotency mechanism. The `match_status = 'PENDING'` condition check prevents double-matching. If an event is already `MATCHED` or `DISCREPANCY`, the transaction aborts cleanly.

### 8.3 Crash recovery

On startup, the engine must recover consistent state:

1. **Redis ↔ Postgres sync**: Scan `canonical_events` where `match_status = 'PENDING'` and `ingested_at` is within the window. Rebuild the Redis candidate sets from this data. This means Redis is a cache of pending candidates, not the source of truth — Postgres is.
2. **In-flight match check**: Any events that were in the process of being matched when the crash occurred will still be `PENDING` in Postgres (the transaction didn't commit). They'll re-enter the candidate window on recovery.
3. **Window expiry catch-up**: Any events that should have expired during downtime will be caught by the first expiry sweep after startup.

The key insight: Redis is rebuildable from Postgres. Losing Redis is a performance hit (cold cache), not a correctness failure.

---

## 9) Observability

### 9.1 Structured logging (zerolog)

Every log line includes: `correlation_id`, `source_type`, `event_id`, and the current processing stage (`ingested`, `candidate_added`, `match_attempted`, `match_confirmed`, `discrepancy_created`, `discrepancy_resolved`).

### 9.2 OpenTelemetry tracing

Spans for:

- Ingestion: connector parse → normalize → dedup check → Postgres write → Redis candidate add
- Matching: candidate lookup → scoring → match confirmation → Postgres transaction
- Expiry: window scan → discrepancy creation

### 9.3 Metrics (collected internally, queryable via API)

**Throughput metrics:**

- `events_ingested_total` (counter, by source_type)
- `events_ingested_per_second` (gauge, by source_type)
- `matches_confirmed_total` (counter)
- `discrepancies_opened_total` (counter, by type)

**Latency metrics:**

- `match_latency_ms` (histogram — time from second event ingestion to match confirmation)
- `ingestion_latency_ms` (histogram — time from event receipt to Postgres write)

**Window metrics:**

- `pending_window_size` (gauge — current number of unmatched candidates in Redis)
- `window_expiry_total` (counter — events that aged out)

**Quality metrics:**

- `match_rate` (gauge — matches / total eligible event pairs, rolling 60s window)
- `late_arrival_resolution_total` (counter)

### 9.4 Metric snapshots

Every 10 seconds, the engine writes a `metric_snapshots` row to Postgres. The benchmark harness reads these for its report.

---

## 10) Query API

Minimal. Exists for the benchmark harness and basic debugging.

### 10.1 Match results

`GET /matches?limit=&cursor=&since=`

Returns paginated matches with linked event IDs and scores.

### 10.2 Discrepancies

`GET /discrepancies?type=&resolved=false&limit=&cursor=`

Returns paginated discrepancies, filterable by type and resolution status.

### 10.3 Event lookup

`GET /events/{event_id}`

Returns the canonical event with its current match status.

### 10.4 Metrics

`GET /metrics/current`

Returns latest metric values (throughput, latency percentiles, match rate, window size).

`GET /metrics/history?since=&until=`

Returns metric snapshots for a time range.

### 10.5 Health

`GET /health`

Returns Postgres and Redis connectivity status + current window size.

---

## 11) Benchmark harness

### 11.1 What it measures

The harness runs a full load test and produces a report with:

|Metric|How measured|
|---|---|
|**Sustained throughput** (events/sec)|Total events ingested / duration, measured at load generator|
|**Match latency p50/p95/p99**|From `metric_snapshots`, computed over full run|
|**Match rate**|Tally's confirmed matches / load generator's ground truth pairs|
|**False positive rate**|Matches that don't appear in ground truth / total matches|
|**Discrepancy detection time**|Time from when a deliberately-dropped event's window expires to when the discrepancy appears|
|**Crash recovery time**|Time from engine restart to Redis candidate window fully rebuilt|
|**Correctness under crash**|After simulated kill + restart: verify no lost events, no duplicate matches, no phantom discrepancies|

### 11.2 How it runs

```bash
make bench                    # default: 1000 tps, 5 minutes
make bench TPS=5000 DUR=600   # custom: 5000 tps, 10 minutes
make bench-crash              # runs bench with mid-test engine kill + restart
```

Steps:

1. Start fresh Postgres + Redis (docker-compose).
2. Run migrations.
3. Start engine.
4. Start load generator with specified parameters.
5. Wait for duration + drain time (2x max window duration).
6. Query engine metrics API.
7. Compare engine matches against load generator ground truth.
8. Output report to stdout and `docs/BENCHMARKS.md`.

### 11.3 Report format

```
=== Tally Benchmark Report ===
Date:       2026-XX-XX
Duration:   300s
Target TPS: 1000
Seed:       42

--- Throughput ---
Events ingested:     892,341
Sustained rate:      2,974 events/sec (across 3 sources)
Effective TPS:       991 transactions/sec

--- Match Quality ---
Ground truth pairs:  296,700
Confirmed matches:   294,812
Match rate:          99.36%
False positives:     0
Unmatched (expected): 297 (injected drops)
Unmatched (unexpected): 1,591

--- Latency ---
Match latency p50:   3.2ms
Match latency p95:   8.7ms
Match latency p99:   14.1ms
Ingestion latency p50: 0.8ms
Ingestion latency p95: 2.1ms

--- Discrepancy Detection ---
Avg detection time:  62.3s (window-bound)
Discrepancies opened: 1,888
Auto-resolved (late): 297

--- Window ---
Peak window size:    12,847
Avg window size:     4,291

--- Crash Recovery (if run with --crash) ---
Kill time:           150s into run
Recovery time:       1.2s (Redis rebuild)
Events lost:         0
Duplicate matches:   0
```

---

## 12) Project timeline (19 weeks)

Estimated effort: ~10 hours/week = ~190 hours total.

Structure: 4 months local development → 2 weeks AWS at scale → 1 week metrics collection and documentation.

---

### Phase 1: Foundation (weeks 1–3, ~30 hours)

**Goal**: Events flow from one source into Postgres. The skeleton runs.

Deliverables:

- Go project scaffolding (modules, Makefile, docker-compose with Postgres + Redis)
- Postgres migrations for all tables in section 5
- Canonical event type definition
- Source A (ledger) connector — ingest JSON, normalize, write to Postgres with idempotency
- Basic load generator — single source only, configurable TPS, no correlation yet
- `make migrate`, `make run`, `make test` targets
- Structured logging wired from day one (zerolog, correlation IDs)

**Handwrite**: Schema design (section 5), canonical event type. **Generate**: Project scaffolding, docker-compose, Makefile, migration runner, pgx boilerplate, zerolog setup.

**Exit check**: `make run` starts the engine, load generator pushes 100 events/sec, all land in `canonical_events` with correct idempotency (rerunning inserts no duplicates).

---

### Phase 2: Matching engine core (weeks 4–7, ~40 hours)

**Goal**: Two sources, streaming matcher producing real matches, first local benchmarks.

Deliverables:

- Source B (processor) connector
- Redis candidate window (sorted sets, amount bucketing)
- Scoring function (section 7.2)
- Match confirmation with SERIALIZABLE Postgres transactions (section 7.3)
- Window expiry → discrepancy creation
- Load generator upgraded: two correlated sources, ground truth file output
- First integration test: generate 1,000 transactions, verify all matched correctly against ground truth
- Basic `make bench` that runs the load generator at 500 TPS for 60 seconds and prints match rate + throughput

**Handwrite**: Scoring function, match confirmation transaction logic, windowing/expiry strategy, Redis key design. **Generate**: Redis sorted set wrappers, Source B connector, load generator correlation model.

**Exit check**: `make bench` at 500 TPS shows >98% match rate and 0 false positives. You can explain every line of the scoring function and the Postgres transaction from memory.

---

### Phase 3: Full pipeline (weeks 8–11, ~40 hours)

**Goal**: Three sources working, late arrivals handled, local benchmarks at higher TPS.

Deliverables:

- Source C (bank batch) connector with CSV parsing, batch-mode delivery
- Late arrival handling (section 7.4) — match against existing discrepancies, auto-resolve
- Load generator upgraded: three correlated sources, full error injection config (drops, jitter, duplicates)
- Ingestion idempotency hardened: replay full load generator output, verify zero duplicates
- `make bench` upgraded: ground truth comparison, false positive check, discrepancy detection time
- Local benchmarks at 1,000–2,000 TPS with all three sources

**Handwrite**: Late arrival matching logic, discrepancy auto-resolution state transitions. **Generate**: Source C connector and CSV parser, load generator error injection, bench report formatting.

**Exit check**: `make bench` at 1,000 TPS with three sources shows >99% match rate, 0 false positives, late arrivals resolve correctly. Error injection produces expected discrepancy counts.

---

### Phase 4: Correctness under failure (weeks 12–14, ~30 hours)

**Goal**: Crash recovery proven, idempotency bulletproof, local crash benchmarks passing.

Deliverables:

- Crash recovery implementation (section 8.3): Redis candidate window rebuilt from Postgres on startup
- Crash test harness: `make bench-crash` kills engine at a random point during a bench run, restarts it, verifies zero lost events and zero duplicate matches
- Idempotency stress test: concurrent event replay from multiple goroutines, verify no corruption
- Graceful shutdown: drain in-flight matches before exit, flush metrics
- Edge case tests: what happens when Redis is down? (engine falls back to Postgres-only matching, slower but correct). What happens when Postgres is down? (engine refuses to process, health endpoint reports unhealthy)

**Handwrite**: Crash recovery logic (Redis rebuild), idempotency guarantees, graceful shutdown sequence. **Generate**: Crash test harness scripting, health check endpoint, concurrent replay test fixtures.

**Exit check**: `make bench-crash` passes 10 consecutive runs with 0 lost events, 0 duplicate matches. You can draw the crash recovery sequence on a whiteboard.

---

### Phase 5: Observability + local benchmarks (weeks 15–17, ~30 hours)

**Goal**: Full tracing, metrics collection, polished local benchmark suite producing resume-ready numbers.

Deliverables:

- OpenTelemetry tracing on all paths (ingestion → matching → confirmation → discrepancy)
- Metric collection: internal counters → `metric_snapshots` table (section 9.3)
- Query API (section 10): matches, discrepancies, events, metrics, health
- Benchmark harness finalized: full report format (section 11.3), `make bench` and `make bench-crash`
- Local benchmark run at max stable TPS on your machine (likely 3,000–5,000 TPS depending on hardware)
- `docs/DECISIONS.md` started with all decisions made so far

**Handwrite**: Benchmark harness design (what metrics, how to validate, report structure), DECISIONS.md entries. **Generate**: OpenTelemetry wiring, API handlers, metric snapshot writer, report formatter.

**Exit check**: `make bench` produces a clean, complete report. `make bench-crash` passes. You have real local numbers: throughput, match rate, latency percentiles, crash recovery time. These are your baseline before AWS.

---

### Phase 6: AWS deployment + scale testing (weeks 18–19, ~20 hours)

**Goal**: Deploy to AWS, run the engine across multiple instances, prove horizontal scaling, collect at-scale metrics.

#### Infrastructure (AWS CDK)

Deploy with `cdk deploy`, destroy with `cdk destroy`. Everything ephemeral — never leave resources running overnight.

Components:

- **RDS Postgres** (db.t3.medium, ~$1.15/day) — single instance, no replicas needed
- **ElastiCache Redis** (cache.t3.micro, ~$0.40/day) — single node
- **ECS Fargate** for engine instances (0.5 vCPU / 1GB each, ~$0.60/day per instance)
- **ECS Fargate** for load generator (1 vCPU / 2GB, ~$1.20/day)
- **S3** for benchmark results and ground truth files (pennies)
- **CloudWatch** for centralized logs and metrics (free tier covers this)

**Estimated cost for 2 weeks**: Run infrastructure ~8 hours/day for ~10 days of active testing = ~80 instance-hours.

|Resource|Daily (8hr)|10 days|
|---|---|---|
|RDS Postgres (db.t3.medium)|$0.38|$3.80|
|ElastiCache (cache.t3.micro)|$0.13|$1.30|
|ECS engine × 1 instance|$0.20|$2.00|
|ECS engine × 4 instances|$0.80|$8.00|
|ECS load generator|$0.40|$4.00|
|Data transfer + S3 + CW|—|~$2.00|
|**Total (single engine)**||**~$13**|
|**Total (4-instance scaling test)**||**~$19**|

Worst case with some overnight accidents and extra runs: **$40–60**. Well under $100.

#### Scaling test plan

**Single-instance baseline**: Deploy 1 engine instance. Run `make bench` at 2,000, 5,000, and 10,000 TPS. Record throughput ceiling (where match rate or latency degrades).

**Horizontal scaling**: The matching engine partitions by `account_ref` hash. Each engine instance owns a partition of the account space. Events are routed to the correct instance via an SQS queue per partition (or a shared queue with message filtering).

Implementation:

- Add a `partition_key` (hash of `account_ref` mod N) to canonical events
- Each engine instance is configured with its partition range
- Load generator distributes events across partitions
- Redis keys are already partitioned by nature (each amount bucket is per-currency, and accounts within a partition won't collide with other partitions)

Deploy 2, then 4 engine instances. Run the same benchmark at each count. The expected result: near-linear throughput scaling (2 instances ≈ 2x throughput, 4 instances ≈ 4x) with constant latency.

**What to measure on AWS that you can't measure locally**:

- Network latency between engine ↔ Postgres ↔ Redis (adds ~1–2ms per hop vs local)
- Throughput ceiling under real network conditions
- Behavior under ECS task restarts (Fargate kills and restarts a task — does crash recovery work?)
- CloudWatch integration: are logs, metrics, and traces flowing correctly?

#### CDK stack structure

```
infra/
  cdk/
    bin/tally.ts
    lib/
      network-stack.ts     # VPC, subnets, security groups
      data-stack.ts        # RDS, ElastiCache
      engine-stack.ts      # ECS service (configurable instance count)
      loadgen-stack.ts     # ECS task for load generator
      observability-stack.ts  # CloudWatch dashboards, log groups
```

**Handwrite**: Partition strategy design, scaling test plan. **Generate**: All CDK code, ECS task definitions, SQS queue config, CloudWatch dashboard definitions.

---

### Phase 7: Final metrics + documentation (week 19–20, ~10 hours)

**Goal**: Collect final numbers, write documentation, polish the repo for public viewing.

Deliverables:

- Final benchmark runs on AWS (single instance + 4 instances) with clean reports
- `docs/BENCHMARKS.md`: local baseline numbers + AWS single-instance numbers + AWS multi-instance scaling numbers, all from the same benchmark harness with the same seed
- `docs/ARCHITECTURE.md`: system diagram, data flow, component responsibilities
- `docs/DECISIONS.md`: complete with all decisions from the build (should have 15–20 entries by now)
- README.md: project overview, quick start, architecture diagram, key metrics, link to benchmark results
- Clean up repo: remove dead code, ensure `make bench` still passes, ensure `docker-compose up` works from a fresh clone
- `cdk destroy` — tear down all AWS resources

**Exit criteria (project "done")**:

- [ ] Three sources ingesting, normalizing, deduplicating
- [ ] Streaming matcher producing correct matches with scoring
- [ ] Discrepancies created on window expiry
- [ ] Late arrivals auto-resolve discrepancies
- [ ] Crash recovery rebuilds Redis from Postgres with zero data loss
- [ ] Idempotent under event replay
- [ ] `make bench` produces reproducible report locally
- [ ] `make bench-crash` passes with 0 lost events, 0 duplicate matches
- [ ] AWS deployment runs at higher TPS with horizontal scaling
- [ ] Benchmark report includes: local baseline, AWS single-instance, AWS multi-instance numbers
- [ ] ARCHITECTURE.md, DECISIONS.md, BENCHMARKS.md written
- [ ] README.md polished for public repo
- [ ] All AWS resources destroyed

---

## 13) Expansion paths (post-project)

Ordered by resume impact. These are only worth doing if you have time after the core is done and benchmarked — the core + AWS scaling story is already strong.

### 13.1 Rules engine for auto-resolution

Configurable rules that auto-resolve known discrepancy patterns. Example: "Source B amount differs from Source A by exactly 1 minor unit AND currency is USD → auto-resolve as fee rounding." Rules defined in YAML, hot-reloadable. Interview story: domain-specific logic vs. generic systems, operational automation.

### 13.2 Anomaly detection

Statistical monitoring on reconciliation metrics. Z-score alerting on discrepancy rates per source. Systematic offset detection (Source B consistently -1 from Source A). Simple stats, not ML. Interview story: "what happens when the system detects something is wrong with a data source."

### 13.3 Multi-currency support

Reconciliation across currency pairs with exchange rate lookup and tolerance adjustment. Adds realistic complexity without changing core architecture.

### 13.4 Real data source integration

Replace one simulated source with actual Stripe test-mode webhooks. Webhook signature verification, Stripe's retry behavior, real event schema parsing. Turns "simulation" into "real integration."

### 13.5 Backfill and replay

Re-reconcile historical data when matching logic changes or a new source is added. Requires separating live state from replay state. Operationally important in real fintech systems.

---

## 14) Tech stack summary

|Component|Choice|Why|
|---|---|---|
|Language|Go|Stripe's primary backend language. Concurrency model fits streaming pipeline. Strong systems signal.|
|Router|chi|Minimal, idiomatic, no magic. Stripe engineers will respect this over a framework.|
|Postgres driver|pgx|Raw SQL, no ORM. Connection pooling built in. Supports SERIALIZABLE isolation.|
|Postgres|16+|SERIALIZABLE transactions for match correctness. Window functions for analytics. JSONB for metadata.|
|Redis|7+|Sorted sets for candidate windowing. Sub-ms lookups. Rebuildable from Postgres (cache, not source of truth).|
|Logging|zerolog|Structured JSON, zero-allocation. Standard in Go systems.|
|Tracing|OpenTelemetry → stdout/Jaeger|Vendor-neutral. Can export to Jaeger locally or X-Ray on AWS.|
|Containerization|Docker + docker-compose|Local dev. Postgres + Redis + engine in one `docker-compose up`.|
|Infra|AWS CDK (TypeScript)|Phases 6–7. Deploy for scale testing, tear down after.|
|CI|GitHub Actions|Lint, test, bench on PR.|

---

## 15) Key design decisions log (seed entries)

These go in `docs/DECISIONS.md`. Add to this as you build.

**D001: Postgres over DynamoDB.** Stripe runs on relational databases. SERIALIZABLE transactions give us correctness guarantees that DynamoDB's transaction model makes harder. Window functions simplify analytical queries. Single datastore reduces operational complexity. Tradeoff: vertical scaling ceiling is lower, but we're not targeting that scale in MVP.

**D002: Redis as rebuildable cache, not source of truth.** If Redis dies, we lose performance (cold candidate window) but not correctness. On restart, we rebuild from Postgres. This is a deliberate architectural choice — we accept slower recovery in exchange for simpler correctness reasoning.

**D003: Junction table for match_events instead of two-column match.** Supports partial matches (1-to-N) without schema migration. Slight overhead for the common case (1-to-1 matches have an extra join) but avoids painting ourselves into a corner.

**D004: Amount bucketing in Redis instead of scanning.** Putting candidates in buckets by `(currency, amount ± tolerance)` means we never scan more than 3 buckets per incoming event. Tradeoff: more Redis keys, slightly more complex candidate insertion. But matching is O(bucket_size) instead of O(total_candidates).

**D005: Scoring-based matching over exact-then-fuzzy cascade.** A single scoring function with configurable weights is simpler to reason about, test, and tune than a multi-stage cascade. Tradeoff: slightly more computation per candidate (always compute full score), but the math is trivial compared to the I/O.