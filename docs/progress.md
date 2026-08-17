# Tally Progress Tracker

Last assessed: 2026-08-17

This document tracks implementation progress against `docs/spec.md`. Update it after every meaningful repo change so the next work session starts from reality, not memory.

## How To Update This Doc

- Read `docs/private/coach.md` before using this tracker.
- Re-read the relevant section of `docs/spec.md` before marking anything complete.
- Mark an item complete only when the implementation exists, is wired into the normal path, and has at least basic verification.
- Keep partial work unchecked and label it `PARTIAL`.
- Add blockers under `Current Blockers` when they prevent tests, local runs, or spec exit checks.
- Update `Latest Verification` whenever tests, builds, migrations, benchmarks, or manual checks are run.

## Current Snapshot

Overall status: early Phase 1, CORE foundations.

Implemented so far:

- `CanonicalEvent` shape mostly matches the current tenant-aware contract.
- Postgres and Redis local infrastructure exists in `docker-compose.yml`.
- Initial `canonical_events` migration exists and has been partially aligned with the tenant contract.
- Basic `POST /events`, `GET /events/{eventID}`, and `GET /health` HTTP routes exist.
- `POST /events` validates through `NewCanonicalEvent`, computes idempotency server-side, returns `201`/`200` on insert/duplicate, and skips Redis/matching on duplicate replay.
- Basic Postgres insert/get functions exist.
- Basic Redis candidate add/find/remove helpers exist.
- `internal/pipeline` compiles against the current `NewCanonicalEvent` constructor.
- Serializable `ConfirmMatch` persists `matches` / `match_events`, updates `match_status`, retries once on serialization failure; entity resolution and graph upsert not wired yet.
- `internal/reconcile` owns matching orchestration: Postgres-backed pending candidate lookup, scoring, `ConfirmMatch`, and Redis cleanup.
- `POST /events` inserts, adds to Redis, then calls `ReconcilePendingEvent`; the engine is constructed once in `main.go`.
- Background pending reconciliation worker (`StartPendingWorker`, 250ms interval, 500-event batch) retries recent `PENDING` events.
- Store methods `FindPendingMatchCandidates` and `FindRecentPendingEvents` query durable Postgres state for reconciliation.
- Deterministic benchmark input generation, benchmark metric computation, JSON reports, `cmd/bench`, `make bench`, and `make bench-load` exist.
- Best clean benchmark run so far: shuffled arrival, 16 workers, 160 true pairs / 832 total events, 1615.23 events/sec, 958ms p99 match latency, 100% match rate, 0 false positives.
- Sentry observability (`internal/observe`) is wired but env-gated: zerolog error/fatal/panic forwarding with breadcrumbs, `sentryhttp` panic middleware on the router, worker panic capture, native performance spans across the reconciliation path (HTTP and worker transactions), a Sentry Crons heartbeat on the pending worker, per-request scope tags (`tenant_id`, `source_type`) with event-ID context on ingestion, release tagging from git build info, flush on shutdown. Disabled unless `SENTRY_DSN` is set. Note: Sentry was not in the original spec observability plan (zerolog + OTel + CloudWatch); the spec was rewritten 2026-08-11 to make Sentry the error-monitoring and trace-backend layer (see spec §14.1 and D011), with CloudWatch retained for business metrics and infra alarms.

Major gaps:

- `go test ./...` passes when local Postgres/Redis are running; fails on store integration test when Docker is down.
- Discrepancy tables, metric snapshots, graph tables, entity resolution, gRPC graph API, product app, sandbox, and deployment infra are not implemented.
- Full crash recovery (startup Redis rebuild, expiry catch-up) is not implemented; the pending worker is the first retry primitive only.
- Load ceiling on seed 42: first false positive at 165 true pairs under shuffled 16-worker ingestion.

## Current Focus: BenchRec Real-Data Validation (started 2026-08-17)

Active arc, sequenced ahead of everything else for the Winter 2027 application cycle (spec §15 revised sequencing, D012–D014). Work plan lives in the private guide doc alongside `docs/private/coach.md`.

- [ ] Part 1: BenchRec adapter + `cmd/benchrec` replay runner (generate zone; reuses `internal/bench` exports without modifying that package); honest before-numbers on the 1:1 both-sided subset recorded here, per-stratum (rule vs MANUAL, exact-amount vs fee-gap).
- [ ] Part 2: scorer rework (HANDWRITE): reference-token similarity, day-granularity time decay, relative amount tolerance; synthetic CI gates must stay green; after-numbers recorded here.
- [ ] Part 3: scale fixes surfaced by the ~94k-event replay (candidate query indexing, worker rescan behavior), each with before/after measurements.

Dataset: `data/benchrec/` (gitignored; CC BY 4.0). Profiled 2026-08-17: 149,854 rows, 56,074 matches, 83.9% 1:1, 95.1% exact amount agreement across legs, day-granularity dates, single USD account, 29% (date, amount) collisions, reference-token overlap in 100% of sampled 1:1 pairs, 16.6% manual matches.

Deferred but still owed, in order: startup Redis rebuild (crash recovery, HANDWRITE); Stripe sandbox processor connector with payout N:M matching and BenchRec-distribution-sampled traffic (D013/D014).

Doc restructure 2026-08-17: `docs/coach.md` moved to `docs/private/coach.md` (gitignored along with all private working docs); references updated.

## Current Blockers

- [ ] `internal/store/postgres_test.go` and `internal/reconcile` integration tests require local Postgres on `localhost:5432`; tests fail when Docker services are not running.

## Resolved: Concurrent Reconciliation Fix (2026-06-05)

Planning doc: `/Users/scr4tch/.cursor/plans/tally_concurrent_reconciliation_fix.plan.md`.

Implemented path:

1. Extracted reconciliation orchestration into `internal/reconcile`.
2. Added Postgres pending-candidate query (`FindPendingMatchCandidates`) as correctness fallback to Redis.
3. Wired `POST /events` to call `ReconcilePendingEvent` after insert/Redis update.
4. Added background pending reconciliation worker (`StartPendingWorker` in `main.go`).
5. Kept `ConfirmMatch` as the only durable state transition; no threshold or scorer changes.

Acceptance gates (all passed 2026-06-05):

- [x] Deterministic reconcile tests: Postgres-only match without Redis candidates; batch `ReconcileRecentPending` matches pending pairs.
- [x] `make bench PAIRS=100 WORKERS=16 ARRIVAL=paired` clean: 100% match rate, 0 false positives, 0 HTTP errors.
- [x] `make bench PAIRS=100 WORKERS=16 ARRIVAL=shuffled` clean under the same gates.
- [x] Stepped load with `WORKERS=16 ARRIVAL=shuffled`: highest clean run 160 true pairs / 832 total events; first non-clean step 165 true pairs (1 false positive).
- [x] Prior paired single-worker resume result remains clean (160 pairs / 832 events, 237 events/sec, 8ms p99).
## Latest Verification

- [x] `go test ./...` run on 2026-05-31 with Docker Postgres up and passed.
- [x] `go test ./internal/event` run on 2026-05-31 and passed.
- [x] `go test ./internal/pipeline` run on 2026-05-31 and passed with no test files.
- [x] `POST /events` smoke test on 2026-05-31: valid insert `201`, duplicate replay `200` without Redis side effects, invalid source `400`, forged idempotency key ignored in DB.
- [x] `go test ./internal/store` on 2026-05-31 with Docker Postgres up: insert metadata + `ConfirmMatch` integration passed.
- [x] `go test ./internal/match/...` on 2026-05-31 and passed.
- [x] `go test ./internal/store -run 'TestCandidateKey|TestCandidateBuckets|TestAddCandidate|TestFindCandidates|TestRemoveCandidate' -v -count=1` on 2026-06-04 with Docker Redis up and passed.
- [x] `go test ./... -count=1` on 2026-06-04 with Docker Postgres/Redis up and passed.
- [x] HTTP smoke test on 2026-06-04: health `200`; ledger+processor correlated events returned `201`/`201`, both became `MATCHED`, `matches` stored score/evidence `1`, and Redis candidate keys were removed.
- [x] HTTP same-source smoke test on 2026-06-04: two ledger events returned `201`/`201`, both remained `PENDING`, no match row was created, and Redis candidate keys remained.
- [x] `go test ./internal/store -run 'TestInsertEvent|TestConfirmMatch' -v -count=1` on 2026-06-04 with Docker Postgres up: metadata persistence, insert idempotency, match persistence/replay failure, and concurrent shared-event match race passed.
- [x] `go test ./... -count=1` rerun on 2026-06-04 after adding store race/idempotency tests and passed.
- [x] `go test ./cmd/bench ./internal/bench ./internal/loadgen -count=1` on 2026-06-04 after adding benchmark harness packages and passed.
- [x] `go test ./... -count=1` on 2026-06-04 after adding `cmd/bench`, `internal/bench`, `internal/loadgen`, and Make targets passed.
- [x] `go run ./cmd/bench -h` on 2026-06-04 printed the expected correctness/load benchmark flags.
- [x] CORE local server started on 2026-06-04 after `docker compose up -d` and `make migrate`; `GET /health` returned `200`.
- [x] `go test ./... -count=1` rerun on 2026-06-04 after run-scoped benchmark event IDs and concurrent match polling changes passed.
- [x] Live paired single-worker benchmark on 2026-06-04: 100 true pairs / 520 total events, 249.34 events/sec, 8ms p99, 100% match rate, 0 false positives.
- [x] Live refined paired single-worker load benchmark on 2026-06-04 stopped at first non-clean step: best clean run 160 true pairs / 832 total events, 237.01 events/sec, 8ms p99, 100% match rate, 0 false positives; 165 true pairs produced 1 false positive.
- [x] Ranked-candidate retry experiment on 2026-06-05 did not fix concurrent benchmark cleanliness: shuffled 16-worker 100-pair run had 91% match rate, paired 16-worker 100-pair run had 55% match rate.
- [x] `go test ./... -count=1` on 2026-06-05 after `internal/reconcile` extraction and Postgres pending-candidate query passed.
- [x] Store tests for `FindPendingMatchCandidates` and `FindRecentPendingEvents` on 2026-06-05 passed.
- [x] Reconcile tests on 2026-06-05: Postgres-only match without Redis candidates, unmatched event restored to Redis, `ReconcileRecentPending` matches pending pairs.
- [x] Live concurrent benchmarks on 2026-06-05 with worker enabled: paired 16-worker 100-pair and shuffled 16-worker 100-pair both 100% match rate, 0 false positives, 0 missed, 0 HTTP errors.
- [x] Stepped load benchmark on 2026-06-05 (shuffled, 16 workers): best clean run 160 true pairs / 832 total events, 1615.23 events/sec, 958ms p99; first non-clean step 165 true pairs (1 false positive).
- [x] `README.md` updated on 2026-06-05 to reflect reconciliation engine, worker, and new benchmark numbers.
- [x] `go test ./... -count=1` on 2026-08-11 with Docker Postgres/Redis up passed after Sentry integration (`internal/observe`, logger, router, worker, `main.go`).
- [x] Server boot smoke test on 2026-08-11 with `SENTRY_DSN` set: startup logged `sentry error monitoring enabled`, `GET /health` returned `200`; without `SENTRY_DSN` the integration is a no-op.
- [x] `go test ./... -count=1` on 2026-08-11 passed after adding reconcile performance spans, worker Crons heartbeat, and ingestion scope tags.
- [x] HTTP smoke test on 2026-08-11 with `SENTRY_TRACES_SAMPLE_RATE=1.0`: correlated ledger/processor pair returned `201`/`201` and both events became `MATCHED` with one match row — span instrumentation does not disturb the matching path.
- [x] Confirmed sentry-go v0.48 has no Go profiling support (`ProfilesSampleRate` absent); profiling deliberately not planned.
- [x] Correctness gate on 2026-08-11 after span instrumentation (Sentry disabled, shuffled, 16 workers, 100 pairs): clean — 100% match rate, 0 false positives, 0 missed, 0 HTTP errors.
- [x] Postgres/Redis addresses made configurable via `DATABASE_URL`/`REDIS_ADDR` on 2026-08-17; `go build ./...` passed, localhost defaults unchanged.
- [x] Multi-stage Dockerfile (distroless static runtime) built successfully on 2026-08-17 (`docker build -t tally:local .`).
- [x] GitHub Actions CI added 2026-08-17: `test` job (vet + full suite against service containers) and `bench-gate` job (live server, 100-pair 16-worker shuffled and paired correctness runs, `jq` asserts clean). First run on main: both jobs green in 1m26s.
- [x] Kubernetes manifests (`deploy/k8s/`) deployed to a local kind cluster via `make k8s-up` on 2026-08-17: postgres StatefulSet ready, migration Job completed, tally Deployment rolled out with `/health` probes; correlated ledger/processor pair posted in-cluster returned `201`/`201`, both events `MATCHED`, match row persisted with score 1.0.

## Phase 1: CORE Foundations

Goal: CORE can ingest correlated events, reconcile them correctly, materialize graph events and edges, and answer basic gRPC graph queries with provenance.

### Resume Swap Gate

This is the point where Tally can credibly replace Wisp on the resume: the matching-and-measurement spine of Phase 1, not the full graph half of Phase 1.

Required floor:

- [x] Redis Candidate Window is implemented to spec.
- [x] Event-Level Matching is implemented and tested.
- [x] Match Confirmation Transaction is implemented with `SERIALIZABLE` semantics.
- [x] Match blockers are resolved (`match_status` column, migrations present).
- [x] `matches` and `match_events` tables exist and are used by the normal match path.
- [x] Benchmark Harness measures throughput, p99 latency, match rate, and false positive rate.
- [x] Resume-ready numbers are produced by the benchmark harness, not estimated.

Strong tier-two adds before swapping if timing allows:

- [ ] `PARTIAL` Crash Recovery and Idempotency: pending worker retries recent `PENDING` events; startup Redis rebuild and expiry catch-up not implemented.
- [ ] Discrepancies and Late Arrivals are implemented and measured.
- [ ] Benchmark Harness reports crash recovery time and discrepancy detection time.

Not required for the resume swap, even though they remain Phase 1 spec work:

- [ ] Entity Resolution.
- [ ] Counterparty Graph Materialization.
- [ ] CORE gRPC Graph API.

### Canonical Event Contract

- [x] Define `CanonicalEvent` with `TenantID`, event IDs, source type, amount, currency, asset code, timestamps, direction, account ref, counterparty ref, metadata, and idempotency key.
- [x] Generate idempotency key as `tenant_id:source_type:source_event_id` in the constructor.
- [x] Validate required tenant, event, source, account, counterparty, amount, currency, timestamp, and direction fields in the constructor.
- [x] Validate `SourceType` against the allowed source set: `ledger`, `processor`, `bank`.
- [x] Normalize timestamps to UTC.
- [ ] Normalize account refs according to connector rules.
- [ ] Preserve noisy `CounterpartyRef` for entity resolution.
- [ ] Decide whether `Currency` and `AssetCode` should both be required for fiat sandbox data or whether one derives from the other.

### Source Normalization And Ingestion

- [ ] `PARTIAL` Ledger JSON normalizer exists, but it currently expects canonical-shaped JSON rather than source-specific ledger input.
- [ ] Processor connector/parser.
- [ ] Bank CSV connector/parser.
- [ ] Connector isolation: connectors know nothing about matching logic.
- [x] Server-side idempotency key computation on ingestion (HTTP path via `NewCanonicalEvent`).
- [x] API ingestion uses constructor instead of trusting decoded request fields (`PostEventRequest` DTO).
- [ ] Source-specific metadata preservation.
- [ ] `PARTIAL` Ingestion logging includes source type, event ID, and source event ID on validation failure; correlation ID and stage not wired yet.

### Postgres CORE Schema

- [x] `canonical_events` table exists.
- [x] `canonical_events` includes tenant ID, asset code, counterparty ref, metadata default, idempotency key, and match status.
- [x] Tenant-aware indexes exist for source/status, account, amount/currency, and timestamp.
- [x] `matches` table.
- [x] `match_events` junction table.
- [ ] `discrepancies` table.
- [ ] `metric_snapshots` table.
- [ ] `counterparty_nodes` table.
- [ ] `counterparty_aliases` table.
- [ ] `counterparty_edges` table.
- [ ] `graph_events` table.
- [ ] Core graph indexes from the spec.
- [ ] Migration tests or at least a clean recreate path verified from empty Postgres.

### Postgres Store Layer

- [x] Basic Postgres connection helper.
- [x] `InsertEvent` writes canonical events with `ON CONFLICT DO NOTHING`.
- [x] `GetEvent` reads canonical events.
- [x] `FindPendingMatchCandidates` returns pending cross-source candidates from Postgres for one event.
- [x] `FindRecentPendingEvents` returns recent pending event IDs for worker retry.
- [ ] `InsertEvent` accepts configurable database URL instead of hardcoded localhost.
- [ ] Store methods use tenant-scoped queries where relevant.
- [x] Match creation persists `matches` and `match_events`.
- [x] Match creation updates `match_status`.
- [x] Match creation records match score and evidence.
- [ ] Discrepancy creation and resolution methods.
- [ ] Metric snapshot write/read methods.
- [ ] Graph node/alias/edge/event upsert methods.
- [x] Tests cover idempotent insert behavior.
- [x] Tests cover serializable match race behavior.
- [x] Tests cover `FindPendingMatchCandidates` tenant/asset/amount/time filtering.
- [x] Tests cover `FindRecentPendingEvents` pending-only results and limit.

### Redis Candidate Window

- [x] Basic Redis client helper.
- [x] Basic candidate add/find/remove helpers exist.
- [x] Candidate key follows spec: `candidates:{tenant_id}:{asset_code}:{amount_bucket}` with `Currency` fallback when `AssetCode` is empty.
- [x] Candidate scores use event timestamp in Unix millis, not current wall-clock seconds.
- [x] Candidate lookup checks exact and adjacent amount buckets.
- [x] Candidate lookup excludes same-source candidates in reconciliation after candidate load.
- [x] Candidate lookup is tenant-scoped.
- [x] Candidate lookup is asset-scoped.
- [x] Matched candidates are removed after durable `ConfirmMatch` in `internal/reconcile`; Redis removal is not part of the Postgres transaction.
- [x] Unmatched pending events are re-added to Redis after reconciliation when no match confirms.
- [ ] Expiry sweep removes aged-out candidates.
- [ ] Redis rebuild from Postgres pending events on startup.

### Event-Level Matching

- [x] Scoring function implemented with amount, time, and account components (`internal/match/score.go`).
- [x] Default weights implemented: amount `0.5`, time `0.3`, account `0.2`.
- [x] Match threshold implemented at `0.85`.
- [x] Amount score supports exact match and decay to tolerance.
- [x] Time score supports min (`5s`) and max (`120s`) delta behavior with plateau.
- [x] Account score supports exact, substring, and mismatch behavior.
- [x] Candidate ranking chooses top valid candidate in `internal/reconcile`.
- [x] False positives are prevented by conservative thresholding and tests (1 minor-unit amount gap scores `0.75`, below threshold).
- [x] Unit tests cover scorer edge cases (`internal/match/score_test.go`).
- [x] Reconcile integration tests cover Postgres-only matching and batch pending retry.

### Match Confirmation Transaction

- [x] Serializable transaction implemented in `ConfirmMatch`.
- [x] Verify both events are still `PENDING`.
- [x] Insert match row.
- [x] Insert match-event rows.
- [x] Update both events to `MATCHED`.
- [ ] Run entity resolution inside the confirmation transaction.
- [ ] Upsert graph state inside the confirmation transaction.
- [x] Remove Redis candidates after durable commit (in `internal/reconcile`).
- [x] Retry serialization conflicts once.
- [x] Ensure replaying the same event cannot create duplicate matches (second confirm fails when status is not `PENDING`).

### Discrepancies And Late Arrivals

- [ ] Window expiry opens explicit discrepancies.
- [ ] Discrepancy records include type and resolution state.
- [ ] Late arrivals check plausible unresolved discrepancies.
- [ ] Valid late matches mark discrepancies `AUTO_RESOLVED`.
- [ ] Graph event provenance records late-arrival resolution.
- [ ] Tests cover missing counterpart, amount mismatch, duplicate, and late-arrival cases.

### Crash Recovery And Idempotency

- [x] `PARTIAL` Background worker scans recent pending events and retries reconciliation (`StartPendingWorker`).
- [ ] Startup scans Postgres for pending events inside the current window.
- [ ] Startup rebuilds Redis candidate windows.
- [ ] Startup runs expiry catch-up for events that aged out during downtime.
- [ ] Redis loss is validated as a cold-cache performance hit, not data loss.
- [ ] Ingestion replay creates no duplicate events.
- [ ] Match replay creates no duplicate matches.
- [ ] Graph replay creates no duplicate graph events.

### Entity Resolution

- [ ] Descriptor normalization.
- [ ] Exact alias candidate retrieval.
- [ ] Fuzzy alias candidate retrieval.
- [ ] Same account/intermediary candidate retrieval.
- [ ] Recurring edge candidate retrieval.
- [ ] Resolution scoring implemented with alias, account, history, amount, and recency weights.
- [ ] New node creation when no candidate clears threshold.
- [ ] Alias seeding and alias strength updates.
- [ ] Resolution evidence stored in graph-event provenance.
- [ ] Entity resolution precision benchmark against seeded ground truth aliases.

### Counterparty Graph Materialization

- [ ] Node upsert.
- [ ] Alias upsert.
- [ ] Edge upsert.
- [ ] Graph event insert.
- [ ] Edge rollups for payment count and total volume.
- [ ] Typical amount rollup.
- [ ] Cadence calculation.
- [ ] Variance score calculation.
- [ ] Reliability score calculation.
- [ ] Concentration share calculation.
- [ ] Compound edge representation for processor/pass-through relationships.
- [ ] Provenance preserved from source observations through graph events.

### CORE gRPC Graph API

- [ ] `graph.proto` exists.
- [ ] `GraphQueryService` exists.
- [ ] `SearchNodes`.
- [ ] `GetSubgraph`.
- [ ] `ExpandNode`.
- [ ] `GetNodeTimeline`.
- [ ] `CompareNodes`.
- [ ] `GetResolutionEvidence`.
- [ ] `GetLedgerIntegrityStatus`.
- [ ] Stable IDs in responses.
- [ ] Provenance in responses by default.
- [ ] Tenant context required on every request.
- [ ] CORE rejects cross-tenant reads.
- [ ] No operator-facing write RPCs for ledger/entity-resolution decisions.

### Ops HTTP API

- [x] `GET /health` exists and checks Postgres and Redis.
- [x] `GET /events/{eventID}` exists.
- [x] `POST /events` exists for local ingestion; delegates to `internal/reconcile` after insert.
- [ ] `GET /metrics/current`.
- [ ] `GET /metrics/history`.
- [ ] Decide whether `/matches` and `/discrepancies` remain debug endpoints or are superseded by gRPC graph APIs.

### Observability

- [x] Basic zerolog logger exists.
- [x] Sentry init from `SENTRY_DSN` / `SENTRY_ENVIRONMENT` / `SENTRY_TRACES_SAMPLE_RATE` env (`internal/observe/sentry.go`), no-op when unset.
- [x] zerolog error/fatal/panic events forwarded to Sentry with breadcrumbs (`sentryzerolog` writer in `internal/logger`).
- [x] HTTP panic capture and optional request tracing via `sentryhttp` middleware on the chi router.
- [x] Background worker panic capture and re-raise (`observe.CapturePanic` in `StartPendingWorker`).
- [x] Sentry release tagged from git commit via Go build info; events flushed on shutdown.
- [x] Native sentry-go performance spans on the reconciliation path: `POST /events` transactions with child spans for event fetch, candidate query, scoring, and match confirmation; worker runs report `reconcile.pending_worker` transactions.
- [x] Sentry Crons heartbeat monitor on the pending reconciliation worker (per-minute check-in, missed-check-in alerting, 2-minute margin).
- [x] Ingestion requests tag Sentry scope with `tenant_id`/`source_type` and attach event IDs as context.
- [x] Operational failures log at error level so they reach Sentry (`POST /events` insert failure, health-check Postgres/Redis failures upgraded from info).
- [ ] Structured logging on ingestion path.
- [ ] Structured logging on candidate lookup/add/remove.
- [ ] Structured logging on match confirmation.
- [ ] Structured logging on discrepancy creation/resolution.
- [ ] Structured logging on entity resolution.
- [ ] OpenTelemetry tracing setup exporting to Sentry via the `sentryotel` span processor.
- [ ] Spans across ingestion, matching, entity resolution, graph materialization, and gRPC queries.
- [ ] Sentry alert rules: new issue types, error-rate spike, p99 transaction latency breach on match and gRPC paths.
- [ ] Distributed tracing across PRODUCT → CORE gRPC via sentry-trace propagation.
- [ ] End-to-end Sentry delivery verified against a real Sentry project (needs a real DSN).
- [ ] Core metrics counters and histograms.
- [ ] Metric snapshots persisted every 10 seconds.
- [ ] CloudWatch dashboard and alarms for business metrics (match rate, discrepancy spike, pending window overflow, ingestion stall).

### Benchmark Harness

- [ ] `cmd/loadgen` exists.
- [x] `cmd/bench` exists.
- [x] `make bench`.
- [ ] `make bench TPS=5000 DUR=600`.
- [ ] `make bench-crash`.
- [x] Ground-truth map for generated transactions.
- [x] Sustained throughput measurement.
- [x] Match latency p50/p95/p99.
- [x] Match rate against ground truth.
- [x] False positive rate.
- [ ] Discrepancy detection time.
- [ ] Crash recovery time.
- [ ] Entity resolution precision.
- [ ] Graph query latency for required gRPC methods.
- [x] Benchmark report sections include correctness/load JSON shape and core resume-gate metrics.

## Phase 2: Multi-Tenant Sandbox Foundation

Goal: a new user can sign in and land in a seeded sandbox tenant with 12-24 months of believable history.

- [ ] Product app directory exists.
- [ ] Google sign-in.
- [ ] Tenant model.
- [ ] Membership model.
- [ ] Sandbox tenant creation on first sign-in.
- [ ] Sandbox reset flow.
- [ ] Preset switch flow.
- [ ] PRODUCT-owned schema for saved views, annotations, notes, tags, and pinned insights.
- [ ] PRODUCT never reads CORE tables directly.
- [ ] CORE read paths enforce tenant scoping.
- [ ] SaaS startup preset.
- [ ] E-commerce business preset.
- [ ] Freelance/agency preset.
- [ ] Crypto-native business preset.
- [ ] Each preset produces 12-24 months of history.
- [ ] Presets include churning customer story beat.
- [ ] Presets include drifting vendor story beat.
- [ ] Presets include duplicate counterparty story beat.
- [ ] Presets include fraud-ish pattern story beat.

## Phase 3: Product Surface

Goal: operator can ask a graph question, watch the graph animate, drill into nodes, and save interpretation-layer artifacts.

- [ ] Next.js app shell.
- [ ] tRPC backend.
- [ ] Auth-to-tenant middleware.
- [ ] CORE gRPC client wrapper in PRODUCT.
- [ ] React Flow graph canvas.
- [ ] Node details panel.
- [ ] Edge details panel.
- [ ] Timeline view.
- [ ] Claude-backed operator agent.
- [ ] Operator prompt.
- [ ] Operator tool definitions.
- [ ] Permission enforcement: operator is read-only on CORE.
- [ ] Permission enforcement: operator can write PRODUCT interpretation artifacts.
- [ ] Visualization DSL operation: `focus_subgraph`.
- [ ] Visualization DSL operation: `annotate_nodes`.
- [ ] Visualization DSL operation: `expand_node`.
- [ ] Visualization DSL operation: `timeline`.
- [ ] Visualization DSL operation: `compare`.
- [ ] Hero flow: vendor concentration increase.
- [ ] Hero flow: customers drifting off cadence.
- [ ] Hero flow: suspicious duplicate vendors.
- [ ] Save annotations.
- [ ] Save views.
- [ ] Save notes.
- [ ] Save tags.
- [ ] Save pinned insights.
- [ ] Sentry Next.js SDK wired for PRODUCT client and server errors.
- [ ] Operator agent tool executions instrumented as Sentry spans.
- [ ] Sentry Session Replay enabled on the operator UI.

## Phase 4: Agentic Sandbox

Goal: demo user can generate or perturb a sandbox business without touching production-style operator flows.

- [ ] Sandbox agent prompt.
- [ ] Sandbox agent tool definitions.
- [ ] Sandbox-only permission guard.
- [ ] Separate sandbox UI entry point.
- [ ] Sandbox Controls panel.
- [ ] Landing-page pre-sign-in demo surface.
- [ ] Generate sandbox data on command.
- [ ] Simulate customer churn.
- [ ] Inject vendor fraud pattern.
- [ ] Fast-forward six months respecting cadences.
- [ ] Scope adversarial scenarios for testing.
- [ ] No accidental escalation path from operator mode into sandbox mutation mode.
- [ ] Sandbox agent tool executions instrumented as Sentry spans.

## Phase 5: Deployment And Distribution

Goal: live multi-tenant sandbox is deployed, demoable, observable, and shareable.

- [ ] `infra/cdk` exists.
- [ ] Network stack.
- [ ] Data stack for RDS Postgres and ElastiCache Redis.
- [ ] EKS Fargate cluster stack.
- [ ] CORE workload stack.
- [ ] PRODUCT workload stack.
- [ ] Observability stack.
- [ ] Kubernetes manifests or CDK-generated equivalents.
- [ ] CORE deployed behind clean service boundary.
- [ ] PRODUCT deployed behind clean service boundary.
- [ ] `tally.kaizhang.ca` landing page.
- [ ] Live sandbox CTA.
- [ ] Production CloudWatch logs.
- [ ] Production CloudWatch metrics.
- [ ] Production CloudWatch alarms for business metrics.
- [ ] Production CloudWatch dashboard.
- [ ] Sentry `production` environment split with release tagging from the deploy pipeline.
- [ ] Sentry alert rules live in production (new issues, error-rate spike, latency breach).
- [ ] Distributed trace verified end-to-end in production: browser → tRPC → gRPC → CORE.
- [ ] End-to-end smoke test against deployed sandbox.
- [ ] LinkedIn post.
- [ ] Coffee chat with CFM student.

## Documentation Checklist

- [x] `docs/spec.md` exists and is the source of truth.
- [x] `docs/private/coach.md` exists and defines coaching behavior.
- [x] `docs/progress.md` exists and tracks implementation status.
- [x] README updated to reflect reconciliation engine, concurrent benchmark results, and current CORE architecture (2026-06-05).
- [ ] `docs/ARCHITECTURE.md`.
- [ ] `docs/DECISIONS.md`.
- [ ] Seed design decisions from the spec copied into `docs/DECISIONS.md`.
- [ ] `docs/BENCHMARKS.md`.
- [ ] Local development guide verified from a fresh clone.

## Handwrite Zones To Protect

Use `docs/private/coach.md` behavior for these areas. If Kai asks for generated code here, push back once with the specific learning/craft reason, then comply if he still wants it.

- [ ] Scoring function.
- [ ] Match confirmation transaction.
- [ ] Windowing and expiry logic.
- [ ] Crash recovery and idempotency.
- [ ] Benchmark harness.
- [ ] Postgres schema design for CORE and graph.
- [ ] Entity resolution layer.
- [ ] gRPC schema design.
- [ ] Agent prompts and tool definitions.
- [ ] Permission model.

## Vibe-Code Zones

Generate freely here, while still keeping the spec boundary intact.

- [ ] Next.js product surface.
- [ ] React Flow visualization plumbing.
- [ ] tRPC boilerplate.
- [ ] Auth scaffolding.
- [ ] Multi-tenant infra plumbing.
- [ ] Data generator.
- [ ] Agent SDK/plumbing around handwritten prompts and tool definitions.

