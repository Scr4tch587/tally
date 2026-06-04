# Tally Progress Tracker

Last assessed: 2026-05-31

This document tracks implementation progress against `docs/spec.md`. Update it after every meaningful repo change so the next work session starts from reality, not memory.

## How To Update This Doc

- Read `docs/coach.md` before using this tracker.
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
- `internal/match` scorer is wired into HTTP ingestion; the handler skips self/same-source candidates, scores all candidates, confirms the top valid match, and removes matched Redis candidates after `ConfirmMatch`.

Major gaps:

- `go test ./...` passes when local Postgres/Redis are running; fails on store integration test when Docker is down.
- The matching pipeline is incomplete.
- Match tables, discrepancy tables, metric snapshots, graph tables, entity resolution, gRPC graph API, benchmark harness, product app, sandbox, and deployment infra are not implemented.
- README still describes the older reconciliation-only shape more than the current graph + product spec.

## Current Blockers

- [ ] `internal/store/postgres_test.go` requires local Postgres on `localhost:5432`; tests fail when Docker services are not running.
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

## Phase 1: CORE Foundations

Goal: CORE can ingest correlated events, reconcile them correctly, materialize graph events and edges, and answer basic gRPC graph queries with provenance.

### Resume Swap Gate

This is the point where Tally can credibly replace Wisp on the resume: the matching-and-measurement spine of Phase 1, not the full graph half of Phase 1.

Required floor:

- [ ] Redis Candidate Window is implemented to spec.
- [ ] Event-Level Matching is implemented and tested.
- [ ] Match Confirmation Transaction is implemented with `SERIALIZABLE` semantics.
- [x] Match blockers are resolved (`match_status` column, migrations present).
- [x] `matches` and `match_events` tables exist and are used by the normal match path.
- [ ] Benchmark Harness measures throughput, p99 latency, match rate, and false positive rate.
- [ ] Resume-ready numbers are produced by the benchmark harness, not estimated.

Strong tier-two adds before swapping if timing allows:

- [ ] Crash Recovery and Idempotency are implemented and benchmarked.
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
- [ ] `InsertEvent` accepts configurable database URL instead of hardcoded localhost.
- [ ] Store methods use tenant-scoped queries where relevant.
- [x] Match creation persists `matches` and `match_events`.
- [x] Match creation updates `match_status`.
- [x] Match creation records match score and evidence.
- [ ] Discrepancy creation and resolution methods.
- [ ] Metric snapshot write/read methods.
- [ ] Graph node/alias/edge/event upsert methods.
- [ ] Tests cover idempotent insert behavior.
- [ ] Tests cover serializable match race behavior.

### Redis Candidate Window

- [x] Basic Redis client helper.
- [x] Basic candidate add/find/remove helpers exist.
- [x] Candidate key follows spec: `candidates:{tenant_id}:{asset_code}:{amount_bucket}` with `Currency` fallback when `AssetCode` is empty.
- [x] Candidate scores use event timestamp in Unix millis, not current wall-clock seconds.
- [x] Candidate lookup checks exact and adjacent amount buckets.
- [x] Candidate lookup excludes same-source candidates in the HTTP handler after candidate event load.
- [x] Candidate lookup is tenant-scoped.
- [x] Candidate lookup is asset-scoped.
- [ ] `PARTIAL` Matched candidates are removed after durable `ConfirmMatch`; Redis removal is not part of the Postgres transaction and still needs crash-recovery handling.
- [ ] Expiry sweep removes aged-out candidates.
- [ ] Redis rebuild from Postgres pending events on startup.

### Event-Level Matching

- [x] Scoring function implemented with amount, time, and account components (`internal/match/score.go`).
- [x] Default weights implemented: amount `0.5`, time `0.3`, account `0.2`.
- [x] Match threshold implemented at `0.85`.
- [x] Amount score supports exact match and decay to tolerance.
- [x] Time score supports min (`5s`) and max (`120s`) delta behavior with plateau.
- [x] Account score supports exact, substring, and mismatch behavior.
- [x] Candidate ranking chooses top valid candidate only in HTTP ingestion.
- [x] False positives are prevented by conservative thresholding and tests (1 minor-unit amount gap scores `0.75`, below threshold).
- [x] Unit tests cover scorer edge cases (`internal/match/score_test.go`).

### Match Confirmation Transaction

- [x] Serializable transaction implemented in `ConfirmMatch`.
- [x] Verify both events are still `PENDING`.
- [x] Insert match row.
- [x] Insert match-event rows.
- [x] Update both events to `MATCHED`.
- [ ] Run entity resolution inside the confirmation transaction.
- [ ] Upsert graph state inside the confirmation transaction.
- [x] Remove Redis candidates after durable commit.
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
- [x] `POST /events` exists for local ingestion.
- [ ] `GET /metrics/current`.
- [ ] `GET /metrics/history`.
- [ ] Decide whether `/matches` and `/discrepancies` remain debug endpoints or are superseded by gRPC graph APIs.

### Observability

- [x] Basic zerolog logger exists.
- [ ] Structured logging on ingestion path.
- [ ] Structured logging on candidate lookup/add/remove.
- [ ] Structured logging on match confirmation.
- [ ] Structured logging on discrepancy creation/resolution.
- [ ] Structured logging on entity resolution.
- [ ] OpenTelemetry tracing setup.
- [ ] Spans across ingestion, matching, entity resolution, graph materialization, and gRPC queries.
- [ ] Core metrics counters and histograms.
- [ ] Metric snapshots persisted every 10 seconds.
- [ ] CloudWatch dashboard and alarms.

### Benchmark Harness

- [ ] `cmd/loadgen` exists.
- [ ] `cmd/bench` exists.
- [ ] `make bench`.
- [ ] `make bench TPS=5000 DUR=600`.
- [ ] `make bench-crash`.
- [ ] Ground-truth table for generated transactions.
- [ ] Sustained throughput measurement.
- [ ] Match latency p50/p95/p99.
- [ ] Match rate against ground truth.
- [ ] False positive rate.
- [ ] Discrepancy detection time.
- [ ] Crash recovery time.
- [ ] Entity resolution precision.
- [ ] Graph query latency for required gRPC methods.
- [ ] Benchmark report sections match the spec.

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
- [ ] Production alarms.
- [ ] Production dashboard.
- [ ] End-to-end smoke test against deployed sandbox.
- [ ] LinkedIn post.
- [ ] Coffee chat with CFM student.

## Documentation Checklist

- [x] `docs/spec.md` exists and is the source of truth.
- [x] `docs/coach.md` exists and defines coaching behavior.
- [x] `docs/progress.md` exists and tracks implementation status.
- [ ] README updated to reflect the current graph + agentic product direction.
- [ ] `docs/ARCHITECTURE.md`.
- [ ] `docs/DECISIONS.md`.
- [ ] Seed design decisions from the spec copied into `docs/DECISIONS.md`.
- [ ] `docs/BENCHMARKS.md`.
- [ ] Local development guide verified from a fresh clone.

## Handwrite Zones To Protect

Use `docs/coach.md` behavior for these areas. If Kai asks for generated code here, push back once with the specific learning/craft reason, then comply if he still wants it.

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

