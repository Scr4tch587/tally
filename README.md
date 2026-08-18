# Tally

A correctness-first Go core for multi-source financial transaction reconciliation — the foundation for a counterparty graph and operator-facing product described in [`docs/spec.md`](docs/spec.md).

Tally ingests canonical transaction events from independent sources (ledger, processor, bank), indexes pending candidates in Redis, scores cross-source pairs with a weighted matcher, and confirms matches in `SERIALIZABLE` Postgres transactions. A Postgres-backed reconciliation engine retries pending events on ingestion and via a background worker. A benchmark harness with seeded ground truth measures throughput, latency, match rate, and false-positive rate against the live HTTP ingestion path.

> **Status: early Phase 1.** The matching-and-measurement spine works locally under concurrent ingestion. Discrepancy handling, full crash recovery (Redis rebuild on startup), entity resolution, graph materialization, gRPC, product app, and deployment are not built yet. See [`docs/progress.md`](docs/progress.md) for the living checklist.

---

## What works today

| Area | Status |
|------|--------|
| `POST /events` ingestion with validation and idempotent insert | Done |
| Redis sorted-set candidate window (tenant × asset × amount bucket) | Done |
| Weighted amount/time/account scorer (`internal/match`) | Done |
| `SERIALIZABLE` match confirmation → `matches` / `match_events` | Done |
| Reconciliation orchestration (`internal/reconcile`) | Done |
| Postgres-backed pending candidate lookup | Done |
| Background pending reconciliation worker | Done |
| Benchmark harness with ground-truth correctness checks | Done |
| Sentry observability (error logs, panics, reconcile spans, worker cron monitor) | Done |
| CI: tests + live-server correctness benchmark gate on every push | Done |
| Multi-stage Docker build (distroless runtime) | Done |
| Kubernetes manifests, verified end-to-end on a local kind cluster | Done |
| Per-source connectors (ledger / processor / bank parsers) | Not started |
| Window expiry → discrepancies | Not started |
| Full crash recovery (Redis rebuild from Postgres on startup) | Not started |
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
│  CORE HTTP API (chi) — main.go                           │
│                                                          │
│  1. Validate → CanonicalEvent (server-side idempotency) │
│  2. InsertEvent (ON CONFLICT DO NOTHING)                 │
│  3. AddCandidate → Redis ZSET                            │
│  4. ReconcilePendingEvent(event_id)                     │
└───────────────┬──────────────────────────────────────────┘
                │
                ▼
┌──────────────────────────────────────────────────────────┐
│  Reconciliation engine (internal/reconcile)              │
│                                                          │
│  • Load current event from Postgres                      │
│  • Find pending candidates from Postgres (correctness)   │
│  • Score cross-source candidates, rank by score          │
│  • ConfirmMatch (SERIALIZABLE tx)                        │
│  • Remove matched events from Redis                      │
│  • Re-add unmatched pending event to Redis               │
└───────────────┬──────────────────────┬───────────────────┘
                │                      │
         Postgres (durable)      Redis (candidate index)
    canonical_events            candidates:{tenant}:{asset}:{bucket}
    matches / match_events      member=event_id, score=timestamp_ms

┌──────────────────────────────────────────────────────────┐
│  Background worker (250ms tick, 500-event batch)         │
│  FindRecentPendingEvents → ReconcilePendingEvent         │
└──────────────────────────────────────────────────────────┘
```

**Postgres is durable truth. Redis is a rebuildable candidate cache.** Request-time reconciliation uses Postgres pending candidates so concurrent arrival does not depend on Redis visibility order. The background worker retries recent pending events to close races and transient serialization failures.

---

## Matching pipeline

When a new event is ingested (`internal/api/handlers.go`):

1. **Idempotent insert.** `InsertEvent` uses `ON CONFLICT DO NOTHING` on `idempotency_key` (`tenant_id:source_type:source_event_id`). Duplicate replays return `200` and skip Redis/matching.

2. **Candidate indexing.** The event is added to Redis sorted sets at keys `candidates:{tenant_id}:{asset}:{amount_bucket}` for the exact amount and adjacent buckets (`amount ± 1` minor unit). Score = event timestamp in Unix milliseconds.

3. **Reconciliation.** `internal/reconcile` loads the event and queries Postgres for pending candidates: same tenant, opposite source, same asset/currency, exact or adjacent amount bucket, timestamp within ±120 seconds.

4. **Scoring.** Same-source candidates are skipped. Remaining pairs are scored (`internal/match/score.go`):

   ```
   score = 0.5 × amount_score + 0.3 × time_score + 0.2 × account_score
   ```

   - `amount_score`: 1.0 exact, linear decay to 0.0 at ±2 minor units
   - `time_score`: 1.0 within 5 s, linear decay to 0.0 at 120 s
   - `account_score`: 1.0 exact (case-insensitive), 0.5 substring, 0.0 otherwise

   Match confirms only if `score ≥ 0.85`.

5. **Confirmation.** Top-ranked candidate above threshold is confirmed in a `SERIALIZABLE` transaction (`internal/store/postgres.go`): both events must still be `PENDING`, same tenant; insert `matches` (with score + evidence JSON) and `match_events`; update both events to `MATCHED`. Serialization conflicts retry once.

6. **Cleanup.** Matched events are removed from Redis. Unmatched pending events are re-added to Redis so future arrivals can find them.

Confirmation happens on the background worker, not inline on ingestion. `POST /events` inserts the event and indexes it in Redis, and the worker sweeps `PENDING` events on a 250ms tick. Deciding at ingestion time meant deciding against whatever happened to be pending at that instant, so an already-pending decoy could win uncontested before the true partner had even been POSTed; deferring the decision measurably reduces false positives (see the BenchRec results below). The sweep pages through pending events with a keyset cursor on `(ingested_at, event_id)` rather than repeatedly reading a fixed slice.

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

**Best measured clean runs** (synthetic data, 2026-06-05, local Docker Postgres/Redis):

| Scenario | Result |
|----------|--------|
| Correctness gate: shuffled, 16 workers, 100 pairs | 100% match rate, 0 false positives, 0 missed |
| Correctness gate: paired, 16 workers, 100 pairs | 100% match rate, 0 false positives, 0 missed |
| Highest clean stepped load: shuffled, 16 workers | 160 true pairs / 832 total events, **1615 events/sec**, **958 ms p99**, 100% match rate, 0 false positives |

First non-clean stepped load step on the same seed: 165 true pairs (1 false positive).

```bash
# Start dependencies and apply migrations
docker compose up -d
make migrate

# Start the server (separate terminal)
go run .

# Concurrent correctness gates
make bench PAIRS=100 WORKERS=16 ARRIVAL=shuffled OUTPUT=bench-results/concurrency-shuffled-w16.json
make bench PAIRS=100 WORKERS=16 ARRIVAL=paired OUTPUT=bench-results/concurrency-paired-w16.json

# Stepped load search for largest clean run
make bench-load WORKERS=16 ARRIVAL=shuffled OUTPUT=bench-results/load-shuffled-w16.json
```

Reports are written to `bench-results/` (default: `bench-results/latest.json`).

---

## Real-data validation (BenchRec)

The synthetic harness above measures the engine against data it generated itself. BenchRec measures it against real bank statement lines reconciled to real internal ledger entries.

**Dataset.** BenchRec cash reconciliation dataset v1.0, released for an ICAIF 2023 competition, licensed **CC BY 4.0**. Files live in `data/benchrec/` and are gitignored for size. 149,854 rows, 56,074 distinct matches, anonymized with structure preserved.

**Scope of the numbers below, stated up front:**

- **1:1 both-sided subset only** — 47,024 of 56,074 matches (83.9%). N:M and one-sided groups are not scored as ground truth.
- Those excluded legs *are* still replayed, as ambient traffic. The engine has to decline them, not merely find partners known to exist.
- **Single account, single currency.** The account component of the scorer contributes no discrimination on this corpus.
- **Anonymized tokens.** Reference strings are scrambled consistently, so overlap is real but the strings are not human-meaningful.
- **One institution's custody flows.** Not representative of card, ACH, or crypto reconciliation.

**Replaying the corpus:**

```bash
go run ./cmd/benchrec -limit 0 -reset -output bench-results/benchrec-after-full.json
```

`-limit N` replays a contiguous window of value dates instead of the whole corpus, which keeps same-day collision density realistic while running in seconds. `-dump-misses N` writes missed pairs and false positives with their raw reference strings.

### Measured results

Both runs: 47,024 ground-truth pairs, 55,806 distractor legs, 149,854 events, shuffled arrival, 16 workers, local Docker Postgres/Redis, 0 HTTP errors.

| | before (2026-08-17) | after (2026-08-18) |
|---|---|---|
| Match rate | 0.8873 | **0.9334** |
| Confirmed true | 41,723 | **43,891** |
| False positives | 4,615 | **2,111** |
| False-positive rate | 0.0996 | **0.0459** |
| Missed | 5,301 | **3,133** |
| Ingest throughput | 1,105 events/sec | **3,081 events/sec** |

Per stratum, by the dataset's own `matchRule` provenance:

| Stratum | pairs | before | after |
|---|---|---|---|
| RULE 1 | 31,576 | 0.9381 | **0.9768** |
| RULE 3 | 9,274 | 0.8614 | **0.9437** |
| RULE 4 | 4,266 | 0.8994 | **0.9376** |
| RULE 6 | 57 | 0.9649 | **0.9825** |
| MANUAL | 1,787 | 0.1231 | 0.1337 |
| RULE 5 / 7 / 8 / 9 | 64 | 0.0000 | 0.0000 |

A third category is reported separately and excluded from the false-positive count: **17,899 "related, out of scope" matches**, where both confirmed events belong to the same `matchId` but that group is N:M rather than 1:1. Those are defensible partial matches, not errors, and folding them into false positives would misstate precision.

### What changed between the two runs

1. **Character n-gram reference similarity** added to the scorer. Whole-token overlap holds for only 25.0% of true pairs; character 6-gram overlap holds for 82.4%, because shared references sit embedded inside longer runs.
2. **Confirmation moved off the ingestion path** onto the worker, so decisions are made once candidates have had a chance to arrive.
3. **Keyset cursor for the pending sweep**, replacing a fixed `ORDER BY ingested_at DESC LIMIT 500` slice that livelocked once the worker became the sole matching path.

### Honest weaknesses

- **MANUAL is effectively unsolved at 13.4%**, and the cause is retrieval rather than scoring: only 61.9% of those pairs agree within one cent and only 77.2% share a value date, so the candidate query never surfaces most of them. The scorer never sees them to score.
- **RULE 5, 7, 8 and 9 are structurally unreachable** for the same reason — all 52 RULE 5 pairs agree to the cent but none share a value date, and the candidate window is ±120 seconds.
- **A 4.6% false-positive rate is not zero**, and this project's stated constraint is that false matches are worse than unmatched events. Refusing to confirm when the top two candidates score within a margin of each other is the obvious next lever and is not yet implemented.
- **Match latency regressed sharply** when confirmation moved to the worker. See below.


---

## Known limitations

- **No discrepancy path.** Events that never match stay `PENDING`; there is no window-expiry sweep or `discrepancies` table yet.
- **Partial crash recovery.** The background worker is the first retry primitive, but startup Redis rebuild from Postgres pending events is not implemented.
- **Redis removal is post-commit.** Not part of the Postgres transaction; a crash between commit and Redis cleanup leaves stale index entries until recovery exists.
- **Load ceiling.** On seed 42 with 40% decoys, correctness breaks at 165 true pairs (1 false positive) under shuffled 16-worker ingestion. This is a synthetic-data figure.
- **Match latency under burst load.** Because confirmation happens on the worker sweep rather than inline, a large backfill queues behind the sweep. Steady state is unaffected (p50 627ms on the 520-event synthetic gate), but per-match latency during the 149,854-event BenchRec replay was p50 161.8s / p99 320.6s, against p50 247ms / p99 491ms when confirmation ran inline. The worker processes 500 events per 250ms tick sequentially, well under the 3,081 events/sec ingest rate, so the backlog builds during a burst. Time-to-drain is the meaningful metric for a bulk replay; per-match latency in that regime measures queue depth rather than engine speed.
- **No pending expiry.** Events that can never match are re-attempted on every sweep forever. At full BenchRec scale that is roughly 22,000 events of wasted work per pass. Correctness is unaffected.

---

## HTTP API

| Endpoint | Description |
|----------|-------------|
| `POST /events` | Ingest a canonical event; runs reconciliation inline |
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
    reconcile/         # Reconciliation orchestration and pending worker
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

Postgres and Redis addresses are configurable via `DATABASE_URL` and `REDIS_ADDR`; both default to the local Docker Compose ports.

### CI

Every push runs two GitHub Actions jobs against Postgres/Redis service containers: `go vet` plus the full test suite, and a benchmark gate that boots the server, runs the 100-pair 16-worker correctness benchmark for both shuffled and paired arrival, and fails unless both runs are clean (100% match rate, 0 false positives, 0 HTTP errors). Reports are uploaded as build artifacts.

### Kubernetes (local)

`deploy/k8s/` holds manifests for the full stack: Postgres StatefulSet with a PVC and `pg_isready` readiness probe, Redis (deliberately on `emptyDir` — the candidate index is rebuildable from Postgres), a migration Job fed from ConfigMaps, and the tally Deployment with liveness/readiness probes on `/health`.

```bash
make k8s-up    # create kind cluster, build + load image, deploy, wait for rollout
make k8s-down  # delete the cluster
```

Verified end-to-end on kind: a correlated ledger/processor pair posted through the in-cluster service reconciles to `MATCHED` with a persisted match row. Cloud deployment (EKS per spec) is not set up.

### Error monitoring (Sentry)

Sentry is optional and disabled unless `SENTRY_DSN` is set, so local dev and benchmarks are unaffected by default.

```bash
SENTRY_DSN=https://<key>@<org>.ingest.sentry.io/<project> go run .
```

| Variable | Effect |
|----------|--------|
| `SENTRY_DSN` | Enables Sentry when set; no-op otherwise |
| `SENTRY_ENVIRONMENT` | Environment tag (default `development`) |
| `SENTRY_TRACES_SAMPLE_RATE` | Enables performance tracing on HTTP requests when > 0 (e.g. `0.2`) |

What gets reported:

- Every zerolog `error`/`fatal`/`panic` log (reconciliation engine, background worker, handlers) via the official `sentryzerolog` writer, with lower-level logs attached as breadcrumbs.
- HTTP handler panics with request context, via `sentryhttp` middleware on the chi router.
- Background worker panics, captured and re-raised.
- With tracing enabled: performance transactions on `POST /events` and worker runs, with child spans for event fetch, candidate query, scoring, and match confirmation.
- A Sentry Crons heartbeat (`pending-reconcile-worker`, per-minute check-in) so a stalled worker raises a missed-check-in alert.
- Ingestion errors carry `tenant_id`/`source_type` tags and event-ID context for per-tenant filtering.
- The release is tagged with the git commit via Go build info; events flush on shutdown.

---

## Key design decisions

**No floats on money.** Amounts are `int64` minor units throughout.

**Serializable isolation for match confirmation.** Two concurrent requests cannot both match the same event; integration tests cover replay and race behavior.

**Redis as a narrow index, Postgres as truth.** Candidate lookup can use Redis for speed; durable reconciliation queries Postgres pending state so concurrent arrival does not depend on Redis visibility order.

**Background pending retry.** A process-local worker rescans recent pending events and reuses the same reconciliation path, closing request-order gaps and transient serialization races.

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
| Error monitoring | Sentry (`sentry-go` + `sentryzerolog` + `sentryhttp`) |
| Containers | Multi-stage Docker build on distroless static |
| Orchestration | Kubernetes (local kind cluster) |
| CI | GitHub Actions (tests + correctness benchmark gate) |

Planned (per spec, not wired): OpenTelemetry tracing exported to Sentry via `sentryotel`, gRPC, Next.js product surface, AWS CDK / EKS Fargate.

---

## Documentation

- [`docs/spec.md`](docs/spec.md) — full architecture, graph model, and roadmap
- [`docs/progress.md`](docs/progress.md) — what's done, blockers, and latest verification
- `docs/private/` — local working docs (coaching conventions, drafts); kept out of git
