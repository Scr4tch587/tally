# Tally

A correctness-first Go core for multi-source financial transaction reconciliation — the foundation for a counterparty graph and operator-facing product described in [`docs/spec.md`](docs/spec.md).

Tally ingests canonical transaction events from independent sources (ledger, processor, bank), indexes pending candidates in Redis, scores cross-source pairs on amount, time, account, and reference-string similarity, and confirms matches in `SERIALIZABLE` Postgres transactions. Confirmation runs on a background worker rather than inline on ingestion, so a decision is made once candidates have had a chance to arrive.

The engine is measured two ways: a synthetic harness with seeded ground truth, and a replay of **BenchRec**, a real bank-to-ledger reconciliation corpus of 149,854 events. Both sets of numbers are published below and always labelled.

> **Status: Phase 1, validated on real data.** The matching-and-measurement spine works under concurrent ingestion and has been measured against a real corpus. Discrepancy handling, full crash recovery (Redis rebuild on startup), entity resolution, graph materialization, gRPC, product app, and cloud deployment are not built. See [`docs/progress.md`](docs/progress.md) for the living checklist.

---

## What works today

| Area | Status |
|------|--------|
| `POST /events` ingestion with validation and idempotent insert | Done |
| Redis sorted-set candidate window (tenant × asset × amount bucket) | Done |
| Scorer: amount, time, account, character n-gram reference similarity | Done |
| Account mismatch as a hard veto | Done |
| `SERIALIZABLE` match confirmation → `matches` / `match_events` | Done |
| Reconciliation orchestration (`internal/reconcile`) | Done |
| Postgres-backed pending candidate lookup | Done |
| Background pending worker with keyset-cursor sweep | Done |
| Synthetic benchmark harness with ground-truth correctness checks | Done |
| BenchRec real-data adapter and replay runner | Done |
| Sentry observability (error logs, panics, reconcile spans, worker cron monitor) | Done |
| CI: tests + live-server correctness benchmark gate on every push | Done |
| Multi-stage Docker build (distroless runtime) | Done |
| Kubernetes manifests, verified end-to-end on a local kind cluster | Done |
| Margin rule (refuse to confirm near-tied candidates) | Not started |
| Pending expiry → discrepancies | Not started |
| Parallel worker sweep | Not started |
| Per-source connectors (ledger / processor / bank parsers) | Not started |
| Full crash recovery (Redis rebuild from Postgres on startup) | Not started |
| Entity resolution and counterparty graph | Not started |
| gRPC graph API, product app, cloud deployment | Not started |

---

## The problem

The same real-world transaction appears across multiple independent systems — an internal ledger, a payment processor webhook, a bank settlement batch. Each source uses different identifiers, timestamps, and delivery semantics.

Reconciliation matches these observations back together. Tally's design constraint: **false positives are worse than unmatched events.** An unmatched event should eventually be explicit state for review; a false match silently buries a problem.

That constraint is not yet fully met on real data — the BenchRec replay has a 4.6% false-positive rate. See [Real-data validation](#real-data-validation-benchrec).

---

## Architecture

```
┌───────────────────────────────────────────────────────────┐
│  Event sources                                            │
│  • Synthetic: internal/loadgen + cmd/bench                 │
│  • Real data: internal/benchrec + cmd/benchrec             │
└────────────────────────┬──────────────────────────────────┘
                         │  POST /events (HTTP)
                         ▼
┌───────────────────────────────────────────────────────────┐
│  CORE HTTP API (chi) — main.go                            │
│                                                           │
│  1. Validate → CanonicalEvent (server-side idempotency)   │
│  2. InsertEvent (ON CONFLICT DO NOTHING)                  │
│  3. AddCandidate → Redis ZSET                             │
│     (no matching here — see Why confirmation is deferred) │
└────────────────────────┬──────────────────────────────────┘
                         │
                         ▼
┌───────────────────────────────────────────────────────────┐
│  Background worker (250ms tick, 500-event batch)          │
│  FindPendingEventsAfter(cursor) → ReconcilePendingEvent   │
│  Keyset cursor on (ingested_at, event_id); resets on a    │
│  short page, so every pending event is tried each sweep   │
└────────────────────────┬──────────────────────────────────┘
                         │
                         ▼
┌───────────────────────────────────────────────────────────┐
│  Reconciliation engine (internal/reconcile)               │
│                                                           │
│  • Load event from Postgres                               │
│  • Find pending candidates from Postgres                   │
│  • Score cross-source candidates, rank by score            │
│  • ConfirmMatch (SERIALIZABLE tx)                          │
│  • Remove matched events from Redis                        │
│  • Re-add unmatched pending event to Redis                 │
└───────────────┬───────────────────────┬───────────────────┘
                │                       │
         Postgres (durable)       Redis (candidate index)
    canonical_events             candidates:{tenant}:{asset}:{bucket}
    matches / match_events       member=event_id, score=timestamp_ms
```

**Postgres is durable truth. Redis is a rebuildable candidate cache.** Reconciliation queries Postgres pending state so concurrent arrival does not depend on Redis visibility order.

### Why confirmation is deferred

Ingestion originally reconciled each event inline, the moment it arrived. That means deciding against whatever happened to be pending at that instant — so when a decoy was already pending and the true partner had not yet been POSTed, the decoy won uncontested. No amount of scoring quality helps, because the true partner is not in the candidate set to be ranked against.

Moving confirmation onto the worker sweep raised the real-data match rate from 0.9189 to 0.9565 and roughly halved false positives on a 666-pair control, at the cost of match latency. Both numbers are below.

### Why the sweep uses a cursor

The worker originally scanned `ORDER BY ingested_at DESC LIMIT 500`. That was adequate while ingestion also matched inline and the worker only caught stragglers. As the *sole* matching path it livelocked: during a burst, newer arrivals continuously displaced the window, and once ingestion stopped, the newest 500 pending events were a fixed set that could not match — their partners were buried behind them — so they were rescanned forever. Measured at full scale: 138,652 events pending, unchanged after 20 seconds on an idle server, and the replay's match rate collapsed to 7.3%.

Flipping to `ASC` only mirrors the failure, with permanently unmatchable old events blocking the head instead. Any fixed slice under a static ordering rescans itself when its rows do not change state; progress requires the selection to advance.

`FindPendingEventsAfter` pages on `(ingested_at, event_id) > cursor`. The tuple is required because `ingested_at` is not unique under concurrent ingestion, so a cursor on it alone skips or repeats rows at page boundaries. Keyset rather than `OFFSET` because rows leaving the set as they become `MATCHED` shift every subsequent offset, silently skipping events.

---

## Matching pipeline

1. **Idempotent insert.** `InsertEvent` uses `ON CONFLICT DO NOTHING` on `idempotency_key` (`tenant_id:source_type:source_event_id`). Duplicate replays return `200` and skip Redis indexing.

2. **Candidate indexing.** The event is added to Redis sorted sets at `candidates:{tenant_id}:{asset}:{amount_bucket}` for the exact amount and adjacent buckets (`amount ± 1` minor unit). Score = event timestamp in Unix milliseconds.

3. **Candidate retrieval.** `internal/reconcile` queries Postgres for pending candidates: same tenant, opposite source, same asset/currency, exact or adjacent amount bucket, timestamp within ±120 seconds. Anything outside that window is never scored — a real recall ceiling, not a scoring outcome.

4. **Scoring** (`internal/match/score.go`). Same-source candidates are skipped. Remaining pairs score:

   ```
   score = 0.40 × amount_score
         + 0.25 × time_score
         + 0.15 × account_score
         + 0.20 × text_score
   ```

   - `amount_score`: 1.0 exact, linear decay to 0.0 at ±2 minor units
   - `time_score`: 1.0 within 5s, linear decay to 0.0 at 120s
   - `account_score`: 1.0 exact (case-insensitive), 0.5 substring, 0.0 otherwise
   - `text_score`: character 6-gram overlap between `counterparty_ref` values

   Match confirms only if `score ≥ 0.79` **and** `account_score > 0`.

5. **Confirmation.** The top-ranked candidate above threshold is confirmed in a `SERIALIZABLE` transaction (`internal/store/postgres.go`): both events must still be `PENDING` and share a tenant; insert `matches` (with score and evidence JSON) and `match_events`; update both events to `MATCHED`. Serialization conflicts retry once.

6. **Cleanup.** Matched events are removed from Redis. Unmatched pending events are re-added so future arrivals can find them.

### The text component

Reference strings are the only discriminating signal on real data once amount, date, and account agree. The primitive is deliberately character-level, not token-level:

- Strings are lowercased and split into alphanumeric runs.
- Every 6-character window **within a run** becomes a gram; grams never span a run boundary, or fixed-width padding fuses unrelated fields into false matches.
- Similarity is the overlap coefficient `|A∩B| / min(|A|,|B|)`.

Two measured reasons for those choices. Whole-token equality finds a shared reference in only **25.0%** of true BenchRec pairs, against **82.4%** for 6-grams, because references sit embedded inside longer runs — `V41806706` inside `V418067061617140`. And Jaccard collapses on this data because the two sides differ wildly in length: a ~150-character attributes field against a ~20-character reference scores 0.069 under Jaccard for what is a perfect match.

### Missing text is not zero text

If neither side yields any grams, the component is dropped and the remaining weights are renormalized — no evidence is not evidence of disagreement. If only *one* side yields grams, the score is 0.0 and still counts. That asymmetry is deliberate: ranking is comparative, so treating one-sided-missing as neutral would let a candidate with **no** reference data outrank one with weak-but-real overlap, making missing data a competitive advantage.

### Why the threshold is low

0.79 looks permissive, and it is, deliberately. A true pair with perfect amount, date, and account but no text overlap scores exactly 0.80. Any threshold high enough to reject a decoy on text alone also rejects those pairs — 18% of real true pairs share no 6-gram at all. Measured: at a 0.85 threshold only 16.6% of true pairs clear the sum.

So discrimination is meant to be **comparative** — best candidate versus runner-up — not absolute within a single pair. The threshold is a sanity floor; ranking does the work. The remaining piece of that design, refusing to confirm when the top two candidates score within a margin of each other, is **not yet implemented**, and it is the main reason the false-positive rate is not lower.

---

## Synthetic benchmark harness

`cmd/bench`, `internal/loadgen`, `internal/bench`. Generates deterministic datasets — true ledger/processor pairs plus decoys (same-source, amount skew, account mismatch, time skew) — posts them to the running server, polls Postgres for confirmed matches, and compares against a ground-truth map.

| Metric | How |
|--------|-----|
| Throughput | Events posted / wall-clock duration |
| Match rate | Confirmed true matches / expected true pairs |
| False positive rate | Confirmed matches not in ground truth |
| Latency p50/p95/p99 | Ms from when both events' POST completes to first DB observation of the match |

A run is **clean** when match rate = 100%, false positives = 0, missed = 0, HTTP errors = 0.

**Measured clean runs (synthetic data, local Docker Postgres/Redis):**

| Scenario | Result |
|----------|--------|
| Correctness gate: shuffled, 16 workers, 100 pairs | 100% match rate, 0 false positives, 0 missed |
| Correctness gate: paired, 16 workers, 100 pairs | 100% match rate, 0 false positives, 0 missed |
| Highest clean stepped load: shuffled, 16 workers (2026-06-05) | 160 true pairs / 832 total events, **1615 events/sec**, **958 ms p99** |

First non-clean stepped load step on the same seed: 165 true pairs (1 false positive). These load figures predate the deferred-confirmation change and have not been re-measured.

```bash
docker compose up -d
make migrate
go run .                       # separate terminal

make bench PAIRS=100 WORKERS=16 ARRIVAL=shuffled OUTPUT=bench-results/gate-shuffled.json
make bench PAIRS=100 WORKERS=16 ARRIVAL=paired   OUTPUT=bench-results/gate-paired.json
make bench-load WORKERS=16 ARRIVAL=shuffled OUTPUT=bench-results/load-shuffled-w16.json
```

Reports are written to `bench-results/` (gitignored).

---

## Real-data validation (BenchRec)

The synthetic harness measures the engine against data it generated itself. BenchRec measures it against real bank statement lines reconciled to real internal ledger entries.

**Dataset.** BenchRec cash reconciliation dataset v1.0, released for an ICAIF 2023 competition, licensed **CC BY 4.0**. 149,854 rows, 56,074 distinct matches, anonymized with structure preserved. Files live in `data/benchrec/`, gitignored for size.

**Scope of every number below, stated up front:**

- **1:1 both-sided subset only** — 47,024 of 56,074 matches (83.9%). N:M and one-sided groups are not scored as ground truth.
- Those excluded legs *are* still replayed, as ambient traffic. The engine has to decline them, not merely find partners known to exist.
- **Single account, single currency.** The account component contributes no discrimination on this corpus.
- **Anonymized tokens.** Reference strings are scrambled consistently, so overlap is real but the strings are not human-meaningful.
- **One institution's custody flows.** Not representative of card, ACH, or crypto reconciliation.

```bash
go run ./cmd/benchrec -limit 0 -reset -output bench-results/benchrec-after-full.json
```

`-limit N` replays a contiguous window of value dates rather than the whole corpus, keeping same-day collision density realistic while running in seconds. `-dump-misses N` writes missed pairs and false positives with their raw reference strings. `-distractors=false` restricts the replay to the 1:1 subset alone.

### Measured results

Both runs: 47,024 ground-truth pairs, 55,806 distractor legs, 149,854 events, shuffled arrival, 16 workers, 0 HTTP errors.

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

A third category is reported separately and excluded from the false-positive count: **17,899 "related, out of scope" matches**, where both confirmed events share a `matchId` but that group is N:M rather than 1:1. Those are defensible partial matches, and folding them into false positives would misstate precision.

**Time to reconcile the corpus end to end** (separate replay of the same configuration, sampling `PENDING` every 5s):

| | |
|---|---|
| Peak backlog | 144,824 pending at t+68s |
| Fully drained | t+418s |
| Drain window | 350s, 122,772 events reconciled |
| Sustained reconcile rate | ~351 events/sec |

The 22,052 events remaining at the floor have no possible partner — one-sided ledger legs, N:M leftovers, and pairs outside the ±120s candidate window. That is the correct resting state, not a stall.

### What produced the improvement

1. **Confirmation moved off the ingestion path** onto the worker, so decisions are made once candidates have had a chance to arrive. Largest single contributor.
2. **Keyset cursor for the pending sweep**, without which the previous change collapses the full replay to 7.3%.
3. **Character n-gram reference similarity** in the scorer, replacing the assumption that shared references match at token level.

### Honest weaknesses

- **MANUAL is effectively unsolved at 13.4%**, and the cause is retrieval rather than scoring: only 61.9% of those pairs agree within one cent and only 77.2% share a value date, so the candidate query never surfaces most of them.
- **RULE 5, 7, 8 and 9 are structurally unreachable** for the same reason — all 52 RULE 5 pairs agree to the cent but none share a value date, against a ±120 second window.
- **A 4.6% false-positive rate is not zero**, and this project defines a clean run as zero. The margin rule is the obvious next lever and is not implemented.
- **Match latency regressed sharply** under burst load. See below.

### Corrections to earlier profiling

Two figures published in an earlier draft of `docs/spec.md` §9.2 were wrong and are corrected here by measurement on the full subset: collisions are **12.6% of ledger legs facing more than one candidate**, not 29% of statement lines (median candidates per leg is 1); and the shared signal is character-level, not token-level (25.0% token overlap versus 82.4% at 6-grams). The MANUAL stratum is 1,787 pairs (3.8%) of the 1:1 subset and lives in `matchRule`, not `matchedBy`.

---

## Known limitations

- **No discrepancy path.** Events that never match stay `PENDING`; there is no window-expiry sweep or `discrepancies` table.
- **No pending expiry.** Events that can never match are re-attempted on every sweep forever — roughly 22,000 events of wasted work per pass at full BenchRec scale. Correctness is unaffected.
- **Partial crash recovery.** The background worker is the first retry primitive, but startup Redis rebuild from Postgres pending events is not implemented.
- **Redis removal is post-commit.** Not part of the Postgres transaction; a crash between commit and Redis cleanup leaves stale index entries until recovery exists.
- **Match latency under burst load.** Because confirmation happens on the worker sweep, a large backfill queues behind it. Steady state is unaffected (p50 627ms on the 520-event synthetic gate), but per-match latency during the BenchRec replay was p50 161.8s / p99 320.6s, against p50 247ms / p99 491ms when confirmation ran inline. In that regime per-match latency measures queue depth, not engine speed; time-to-drain is the meaningful figure. The sweep is **work-bound, not tick-bound** — it sustains ~351 events/sec, so 500 attempts take roughly 1.4s against a 250ms tick and ticks already run back-to-back. Raising the batch limit would not help; parallelizing the sweep would.
- **Load ceiling (synthetic).** On seed 42 with 40% decoys, correctness broke at 165 true pairs under shuffled 16-worker ingestion. Measured before deferred confirmation; not re-measured.
- **N:M matching is not implemented.** Only 1:1 pairs are matched or scored.

---

## HTTP API

| Endpoint | Description |
|----------|-------------|
| `POST /events` | Ingest a canonical event; inserts and indexes it. Matching happens on the worker sweep, so a `201` does not mean a match was attempted. |
| `GET /events/{eventID}` | Fetch a canonical event by ID |
| `GET /health` | Postgres + Redis connectivity check |

Planned but not implemented: `GET /metrics/current`, `GET /metrics/history`, gRPC graph queries.

---

## Data model

**`canonical_events`** — every ingested event. Tenant-scoped with `match_status` (`PENDING` or `MATCHED`). Unique index on `idempotency_key`. Partial index on `(ingested_at, event_id) WHERE match_status = 'PENDING'` backs the worker's cursor sweep.

**`matches`** — confirmed match rows with `match_score` and `evidence` (JSONB scoring breakdown, including `text_score`, `text_match`, and `account_veto`).

**`match_events`** — junction linking each match to its two (eventually N) canonical events.

Not yet migrated: `discrepancies`, `metric_snapshots`, counterparty graph tables.

---

## Project layout

```
tally/
  cmd/
    bench/             # Synthetic benchmark harness binary
    benchrec/          # BenchRec real-data replay runner
  internal/
    api/               # HTTP handlers and routes
    bench/             # Correctness, latency, report computation
    benchrec/          # BenchRec CSV adapter → CanonicalEvent
    event/             # CanonicalEvent contract and validation
    loadgen/           # Deterministic synthetic dataset generation
    match/             # Scoring function
    reconcile/         # Reconciliation orchestration and pending worker
    store/             # Postgres and Redis access
  migrations/          # Postgres schema
  scratch/             # Throwaway data-profiling scripts
  docs/
    spec.md            # Full product + architecture spec
    progress.md        # Implementation tracker
    private/           # Local working docs, gitignored
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

Every push runs two GitHub Actions jobs against Postgres/Redis service containers: `go vet` plus the full test suite, and a benchmark gate that boots the server, runs the 100-pair 16-worker correctness benchmark for both shuffled and paired arrival, and fails unless both runs are clean. Reports are uploaded as build artifacts.

The synthetic gate is the only thing that can catch multi-account regressions — BenchRec has a single account, so it is structurally blind to that class of bug.

### Kubernetes (local)

`deploy/k8s/` holds manifests for the full stack: Postgres StatefulSet with a PVC and `pg_isready` readiness probe, Redis (deliberately on `emptyDir` — the candidate index is rebuildable from Postgres), a migration Job fed from ConfigMaps, and the tally Deployment with liveness/readiness probes on `/health`.

```bash
make k8s-up    # create kind cluster, build + load image, deploy, wait for rollout
make k8s-down  # delete the cluster
```

Verified end-to-end on kind: a correlated ledger/processor pair posted through the in-cluster service reconciles to `MATCHED` with a persisted match row. Cloud deployment (EKS per spec) is not set up.

### Error monitoring (Sentry)

Optional and disabled unless `SENTRY_DSN` is set, so local dev and benchmarks are unaffected by default.

```bash
SENTRY_DSN=https://<key>@<org>.ingest.sentry.io/<project> go run .
```

| Variable | Effect |
|----------|--------|
| `SENTRY_DSN` | Enables Sentry when set; no-op otherwise |
| `SENTRY_ENVIRONMENT` | Environment tag (default `development`) |
| `SENTRY_TRACES_SAMPLE_RATE` | Enables performance tracing on HTTP requests when > 0 (e.g. `0.2`) |

What gets reported:

- Every zerolog `error`/`fatal`/`panic` log via the official `sentryzerolog` writer, with lower-level logs attached as breadcrumbs.
- HTTP handler panics with request context, via `sentryhttp` middleware on the chi router.
- Background worker panics, captured and re-raised.
- With tracing enabled: transactions on `POST /events` and worker runs, with child spans for event fetch, candidate query, scoring, and match confirmation.
- A Sentry Crons heartbeat (`pending-reconcile-worker`) so a stalled worker raises a missed-check-in alert.
- Ingestion errors carry `tenant_id`/`source_type` tags and event-ID context.
- Release tagged with the git commit via Go build info; events flush on shutdown.

---

## Key design decisions

**No floats on money.** Amounts are `int64` minor units throughout. The BenchRec adapter parses decimal strings to exact cents and refuses anything that is not 2-decimal.

**Serializable isolation for match confirmation.** Two concurrent requests cannot both match the same event; integration tests cover replay and race behavior.

**Redis as a narrow index, Postgres as truth.** Durable reconciliation queries Postgres pending state so concurrent arrival does not depend on Redis visibility order.

**Confirmation is deferred, not inline.** Matching at arrival decides against a partially-arrived candidate set. Measured worth: +3.8 points of match rate and roughly half the false positives, traded against burst-load latency.

**The pending sweep must advance.** A fixed slice under a static ordering rescans itself forever once its rows stop changing state. Keyset pagination on `(ingested_at, event_id)`.

**Amount bucketing with adjacency.** Exact and ±1 minor-unit buckets catch small fee-rounding differences without unbounded scans. This is also a hard recall ceiling: on BenchRec, 97.4% of true pairs fall inside it, and the remaining 2.6% are never scored.

**Idempotency at the database layer.** `ON CONFLICT DO NOTHING` makes ingestion replays safe without application-level dedup.

**Account mismatch is a veto, not a weight.** Different accounts are different money, so no amount of matching reference text can override it. Without the veto, two events with identical amount, timestamp, and description but different accounts scored 0.85 and confirmed.

**Precision comes from ranking, not from a high threshold.** The 0.79 threshold is a sanity floor. Because 18% of real true pairs share no reference text, a threshold strict enough to reject a text-less decoy also rejects them. Discrimination is comparative between candidates instead — which is why the unimplemented margin rule, not a higher threshold, is the remaining lever on false positives.

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
- `docs/private/` — local working docs; kept out of git

---

## Attribution

The BenchRec cash reconciliation dataset v1.0 (ICAIF 2023) is used under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). Dataset files are not redistributed in this repository.
