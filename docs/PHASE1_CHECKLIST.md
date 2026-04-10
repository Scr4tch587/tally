# Phase 1 Checklist

This file is the working memory for Phase 1. An AI agent should treat it as the source of truth for scope, progress, decisions, blockers, and next steps.

If we make progress, update this file in the same change set. Do not rely on chat history as memory.

## Agent Update Rules

Use these rules every time work is done:

1. Update the checkbox status for any completed or newly-started item.
2. Update `Current Focus` so the next session can resume immediately.
3. Add a short entry to `Session Log` with:
   - date
   - what changed
   - what was verified
   - what remains
4. If a decision changes the plan, add it to `Decisions / Constraints`.
5. If a blocker appears, add it to `Open Questions / Blockers`.
6. If implementation diverges from the original spec, note the reason here instead of assuming the difference will be remembered later.

## Phase 1 Goal

From the spec:

- Events flow from one source into Postgres.
- The skeleton runs.
- `make run` starts the engine.
- A basic load generator pushes 100 events/sec from Source A only.
- All events land in `canonical_events`.
- Re-running the same events produces no duplicates.

## Phase 1 Deliverables

- Go project scaffolding
- `docker-compose.yml` with Postgres and Redis
- Postgres migrations for all Phase 1 tables from spec section 5
- Canonical event type definition
- Source A ledger connector: ingest source JSON, normalize, write to Postgres with idempotency
- Basic single-source load generator with configurable TPS
- `make migrate`, `make run`, `make test`
- Structured logging with zerolog and correlation IDs

## Current Focus

`Establish the Phase 1 schema, starting with the canonical_events migration and migration wiring.`

## Current Repo Assessment

Status is based on the repository state reviewed on 2026-04-09.

| Area | Status | Notes |
| --- | --- | --- |
| Go module | Partial | Repo builds around a single `main.go`, but the spec expects clearer project scaffolding and command entry points. |
| Docker Compose | Done | [docker-compose.yml](/Users/scr4tch/Documents/Coding/Projects/tally/docker-compose.yml) defines Postgres and Redis. |
| Makefile | Missing | No `Makefile` exists yet. |
| Migrations | Missing | No `migrations/` directory exists yet. |
| Docs workspace | Partial | `README.md` and `spec.md` exist; `docs/` did not previously exist. |
| Canonical event model | Partial | [internal/event/canonical.go](/Users/scr4tch/Documents/Coding/Projects/tally/internal/event/canonical.go) exists, but it does not yet match the Phase 1 contract. Missing fields include `SourceEventID`, `IngestedAt`, `Direction`, `AccountRef`, and `IdempotencyKey`. |
| Ledger connector | Missing | Current API accepts canonical JSON directly rather than ledger-source JSON plus normalization. |
| Postgres event ingestion | Partial | [internal/store/postgres.go](/Users/scr4tch/Documents/Coding/Projects/tally/internal/store/postgres.go) inserts some fields, but not the full schema and not the spec-defined idempotency key. |
| Structured logging | Partial | [internal/logger/logger.go](/Users/scr4tch/Documents/Coding/Projects/tally/internal/logger/logger.go) uses zerolog, but correlation IDs and stage fields are not wired through request handling. |
| Basic HTTP skeleton | Partial | [main.go](/Users/scr4tch/Documents/Coding/Projects/tally/main.go) starts a server and health route. |
| Single-source load generator | Missing | No dedicated load generator command exists yet. |
| Tests | Missing | No Phase 1 tests are present yet. |
| Redis usage | Out of phase | Redis candidate-window logic exists, but that is not needed to finish Phase 1 and should not distract from Phase 1 completion criteria. |

## Working Principles For Phase 1

- Prefer finishing the spec-defined foundation cleanly over preserving premature Phase 2 logic.
- Keep Source A ingestion isolated from matching logic.
- Use Postgres as the Phase 1 source of truth.
- Treat Redis work as optional for Phase 1 unless it is required to keep the app booting.
- Do not add Source B, Source C, fuzzy scoring, or discrepancy workflows until the Phase 1 exit check passes.

## Definition Of Done

Phase 1 is done when all of the following are true:

- [ ] `docker compose up -d` starts local dependencies successfully.
- [ ] `make migrate` creates the required Phase 1 database schema.
- [ ] `make run` starts the engine cleanly.
- [ ] Source A ledger events can be posted in source-specific format.
- [ ] Source A events are normalized into the canonical event shape before storage.
- [ ] Events are inserted into `canonical_events` with a correct spec-aligned idempotency key.
- [ ] Replaying the same Source A event does not create duplicates.
- [ ] A basic single-source load generator can drive 100 events/sec.
- [ ] Logs are structured and include a correlation ID and processing stage.
- [ ] `make test` passes Phase 1 unit/integration tests.

## Ordered Checklist

### 1. Align the skeleton with Phase 1 scope

- [ ] Decide whether to keep a single binary entry point or move to `cmd/engine` and `cmd/loadgen`.
- [ ] Create any missing top-level directories required for Phase 1:
  `cmd/`, `migrations/`, `configs/`, `docs/`.
- [ ] Add a `Makefile` with at least `make migrate`, `make run`, and `make test`.
- [ ] Confirm local run commands and environment variables are explicit rather than hardcoded where practical.
- [ ] Record the agreed Phase 1 structure here after implementation.

### 2. Implement the Phase 1 schema

- [ ] Create SQL migrations for the section 5 tables needed by the spec.
- [x] At minimum, ensure `canonical_events` is correct and usable for the Phase 1 exit check.
- [ ] Decide whether `matches`, `match_events`, `discrepancies`, and `metric_snapshots` are created now for spec alignment or stubbed as future-ready migrations.
- [x] Ensure `canonical_events.idempotency_key` is unique.
- [x] Ensure schema uses `match_status` exactly as described by the spec.
- [ ] Add a migration application path wired to `make migrate`.
- [ ] Verify migrations from a fresh database.

### 3. Correct the canonical event contract

- [ ] Replace the current partial event model with a Phase 1-compatible canonical type.
- [ ] Include all contract fields needed by the spec:
  `EventID`, `SourceType`, `SourceEventID`, `AmountMinor`, `Currency`, `Timestamp`, `IngestedAt`, `Direction`, `AccountRef`, `Metadata`, `IdempotencyKey`.
- [ ] Normalize timestamps to UTC.
- [ ] Normalize currency to uppercase and validate allowed values.
- [ ] Normalize account references consistently.
- [ ] Ensure amounts are stored only as `int64` minor units.
- [ ] Separate raw source payload parsing from canonical model validation.

### 4. Build the Source A ledger connector

- [ ] Define the raw Source A payload shape from the spec:
  `transaction_id`, `wallet_from`, `wallet_to`, `amount`, `currency`, `created_at`.
- [ ] Decide how debit/credit direction is derived for Source A.
- [ ] Implement normalization from Source A payload to canonical event.
- [ ] Generate a correct `idempotency_key = source_type:source_event_id`.
- [ ] Keep connector code separate from store code.
- [ ] Reject malformed input with clear errors.
- [ ] Add tests covering valid normalization and invalid payload handling.

### 5. Harden Postgres ingestion and idempotency

- [ ] Update Postgres insert logic to store all required canonical event fields.
- [ ] Use `ON CONFLICT` on `idempotency_key` to make replay safe.
- [ ] Return a clean result that distinguishes inserted vs duplicate.
- [ ] Ensure duplicate replay is not treated as an error.
- [ ] Add an event lookup path that returns the stored canonical row accurately.
- [ ] Verify replaying the same event twice results in one row only.

### 6. Expose the minimal engine API

- [ ] Keep a health endpoint.
- [ ] Add or refine the ingestion endpoint so it accepts Source A raw input, not canonical events directly.
- [ ] Return appropriate status codes for inserted vs duplicate events.
- [ ] Ensure handler logging includes request correlation and stage markers.
- [ ] Keep API shape minimal; do not expand to later-phase endpoints unless needed.

### 7. Add structured logging from day one

- [ ] Configure zerolog for structured output suitable for local development and later benchmarking.
- [ ] Generate or propagate a `correlation_id` per request/loadgen event.
- [ ] Include `source_type`, `event_id` where available, and processing stage.
- [ ] Log at least these stages for Phase 1:
  `received`, `normalized`, `inserted`, `duplicate`, `rejected`.
- [ ] Verify logs are useful for tracing a single event end to end.

### 8. Build the basic single-source load generator

- [ ] Create a dedicated load generator command or runnable package.
- [ ] Support configurable TPS.
- [ ] Support configurable duration.
- [ ] Generate only Source A events for Phase 1.
- [ ] Make event generation deterministic enough to replay duplicates intentionally.
- [ ] Allow a run mode that replays the same batch to test idempotency.
- [ ] Verify it can sustain 100 events/sec locally against the engine.

### 9. Add verification and tests

- [ ] Add unit tests for canonical event validation and normalization.
- [ ] Add unit tests for idempotency key generation.
- [ ] Add store tests or integration tests for inserted-vs-duplicate behavior.
- [ ] Add an API-level test for Source A ingestion.
- [ ] Add a minimal end-to-end Phase 1 verification path:
  start dependencies, run migrations, start engine, send events, confirm row counts.
- [ ] Wire tests into `make test`.

### 10. Run the Phase 1 exit check

- [ ] From a clean local environment, run `docker compose up -d`.
- [ ] Run `make migrate`.
- [ ] Run `make run`.
- [ ] Run the load generator at 100 events/sec.
- [ ] Verify rows appear in `canonical_events`.
- [ ] Replay the same events.
- [ ] Verify duplicate count stays at zero and row count does not grow incorrectly.
- [ ] Record the exact verification commands and results in `Session Log`.

## Suggested Execution Order

Use this order unless a concrete dependency forces a change:

1. Skeleton and `Makefile`
2. Migrations
3. Canonical event contract
4. Source A normalization
5. Postgres ingestion and idempotency
6. Minimal API route
7. Logging
8. Load generator
9. Tests
10. Exit-check run and cleanup

## Decisions / Constraints

- Phase 1 should ignore or defer matching logic unless it blocks compilation or local boot.
- Existing Redis candidate logic appears to be early Phase 2 work and should not drive the Phase 1 plan.
- The current `/events` handler shape is not sufficient for the spec because it bypasses source-specific normalization.

## Open Questions / Blockers

- [ ] Decide whether to keep the current flat layout around `main.go` for Phase 1 or move immediately to `cmd/engine` and `cmd/loadgen`.
- [ ] Decide whether Phase 1 should create all section 5 tables now or only the subset strictly needed for the exit check while preserving forward compatibility.
- [ ] Decide whether to keep the current Redis-dependent startup path in the engine during Phase 1 or remove it from the hot path until Phase 2.

## Session Log

### 2026-04-09

- Created this tracker from `spec.md` Phase 1 requirements and a repo review.
- Assessed current implementation against the Phase 1 contract.
- Identified major gaps: no `Makefile`, no migrations, no Source A raw connector, incomplete canonical event contract, no basic load generator, incomplete idempotency handling.
- No code behavior was changed yet as part of this checklist creation.
- Added `migrations/0001_create_canonical_events.sql` with the spec-aligned `canonical_events` schema and indexes; verified by manual comparison against spec section 5.1; still need migration tooling and fresh-database execution verification.

## Next Recommended Task

Start with checklist section 1 and section 2 together:

- create the `Makefile`
- decide the Phase 1 command layout
- add the first migration for `canonical_events`

That gives the project a clean execution path before touching ingestion details.
