## 0) Overview

Build a **two-tier financial graph system**:

- **CORE** — a correctness-first Go service that reconciles raw transaction observations, resolves them to real counterparties, and materializes a counterparty graph in Postgres.
- **PRODUCT** — a multi-tenant Next.js app that lets an operator navigate that graph through an agent and a dynamic visualization.

The live artifact is not a benchmark report or a blog post. The primary artifact is a deployed sandbox at `tally.kaizhang.ca` where someone can sign in, land in a seeded business, and ask questions against a believable financial graph.

---

## 1) Thesis

> "Tally is a ledger that treats every transaction as an edge in a business's counterparty graph, and an agent that lets you navigate that graph by asking questions. Reconciliation isn't a back-office cleanup task — it's how a business builds a living model of its financial relationships. Agents are the right interface for exploring that model."

Implications:

- Reconciliation is still the technical foundation, but it is no longer the top-level pitch.
- The counterparty graph is the product model.
- The agent is the primary interface. Dashboards exist to support the agent, not replace it.
- The quality of the product is downstream of the quality of the ledger-integrity layer.

---

## 2) Motivation

A business does not just need rows to match. It needs to know who it pays, who pays it, how those relationships are changing, and which relationships deserve attention.

That requires two things at the same time:

- A **correctness layer** that can ingest noisy multi-source transaction data, reconcile it, preserve idempotency, survive crashes, and explain why it believes two observations describe the same real-world event.
- An **interpretation layer** that can turn that reconciled history into a navigable graph of customers, vendors, processors, tax authorities, employees, subsidiaries, and pass-through intermediaries.

Tally is intentionally both:

- The **Go core** is the Stripe signal: systems depth, correctness, transactions, crash recovery, and schema design.
- The **product surface + agent** is the Slash signal: taste, AI-native product framing, graph UX, and shipping velocity.
- The whole is stronger than either half alone.

---

## 3) Hard Rules (Non-Negotiable)

### Correctness

- **Every confirmed match is correct**: a matched pair must reference the same real-world transaction. False positives are worse than unmatched events.
- **No event is silently lost**: every ingested event either matches, remains pending, or becomes an explicit discrepancy. Crash recovery must preserve this invariant.
- **Idempotent processing**: replaying the same event produces no duplicate matches, no duplicate graph events, and no state corruption.
- **Every graph event is explainable**: a node assignment or edge attachment must be traceable to source observations and stored resolution evidence.

### Boundary Discipline

- **CORE owns ledger integrity**: reconciliation decisions, discrepancies, entity resolution, graph truth, and graph query semantics.
- **PRODUCT owns interpretation artifacts**: annotations, saved views, notes, tags, pinned insights, sandbox controls, and UI state.
- **PRODUCT never reads CORE tables directly**: the gRPC schema is the contract.
- **The boundary is deliberate**: API taste matters here. This is not glue code.

### Permissions

- **Operator agent**: read-only on CORE-layer data; read/write on PRODUCT-layer data.
- **Sandbox agent**: full read/write inside sandbox tenant data only.
- Framing: **"the agent operates at the interpretation layer, never at the ledger-integrity layer."**

### Observability

- **Every metric claimed on the resume is measured by the benchmark harness**, not estimated.
- **Structured logging and tracing on every critical path**: ingestion, matching, entity resolution, graph materialization, gRPC queries, agent tool execution.

---

## 4) High-Level Architecture

```
               ┌───────────────────────────────────────────────┐
               │        Sources / Sandbox Data Generator       │
               │ ledger | processor | bank | preset histories  │
               └───────────────────────┬───────────────────────┘
                                       │
                            Canonical transaction events
                                       │
        ┌──────────────────────────────▼──────────────────────────────┐
        │                         CORE (Go)                           │
        │  ingestion → reconciliation → entity resolution → graph     │
        │  Redis candidate window + Postgres durable state            │
        │  gRPC GraphQuery service + health/metrics endpoints         │
        └──────────────────────────────┬──────────────────────────────┘
                                       │
                          Read-only graph/query boundary
                                       │
        ┌──────────────────────────────▼──────────────────────────────┐
        │                   PRODUCT (Next.js + TS)                    │
        │  Google sign-in · multi-tenant app · tRPC backend          │
        │  React Flow graph · operator agent (Claude)                │
        │  sandbox controls · sandbox agent · saved views/notes      │
        └──────────────────────────────┬──────────────────────────────┘
                                       │
                                  Operators / Demo users
```

### Service split

**CORE (Go, handwrite-heavy)**

- Reconciliation engine
- Entity resolution built on top of the fuzzy matcher
- Counterparty graph data model in Postgres
- gRPC API exposing graph queries to PRODUCT
- SERIALIZABLE transaction semantics, idempotency, crash recovery

**PRODUCT (Next.js + TypeScript + tRPC, vibe-coded-heavy)**

- Multi-tenant web app with Google sign-in
- React Flow graph visualization
- Operator-facing agent (Claude API)
- Sandbox controls, sandbox lifecycle, sandbox agent

**Handwrite**: reconciliation engine, event scoring function, entity resolution, graph schema, gRPC schema, prompts/tool definitions, permission model.
**Generate**: Next.js scaffolding, tRPC plumbing, auth wiring, React Flow plumbing, multi-tenant infra plumbing, agent SDK integration, seed-data generator wiring.

---

## 5) Boundary Between CORE and PRODUCT

This boundary is part of the design, not an implementation detail.

### Ownership

- **CORE schema**: canonical events, matches, discrepancies, nodes, edges, graph events, metrics snapshots.
- **PRODUCT schema**: tenants, memberships, saved views, annotations, notes, tags, pinned insights, sandbox configs.
- Shared infrastructure is acceptable; shared table ownership is not.

### Request flow

1. Browser talks to PRODUCT via tRPC.
2. PRODUCT server authenticates user, resolves tenant, authorizes requested action.
3. PRODUCT server calls CORE gRPC with explicit tenant context.
4. CORE returns graph-shaped read models with provenance and ledger-integrity metadata.
5. PRODUCT renders graph state and stores interpretation-layer artifacts locally in PRODUCT-owned tables.

### API design principles

- Graph-shaped responses, not raw reconciliation rows.
- Stable IDs for nodes, edges, graph events, discrepancies.
- Provenance included by default: source count, match score, resolution confidence, discrepancy state.
- No operator-facing write RPCs that mutate reconciliation or entity-resolution decisions.
- Tenant context explicit on every request; CORE rejects cross-tenant reads.

### Boundary consequence

If the PRODUCT app disappears, the CORE still owns a correct ledger-integrity and graph layer. If the CORE is wrong, the PRODUCT should have no way to paper over it.

**Handwrite**: service boundary, gRPC request/response shape, tenancy contract, provenance semantics.
**Generate**: client stubs, server adapters, auth-to-tenant middleware.

---

## 6) Repo Structure

```
tally/
  cmd/
    core/                # main entry point — ingestion + matcher + gRPC + ops HTTP
    loadgen/             # source simulator for reconciliation benches
    bench/               # benchmark harness binary
  internal/
    canonical/           # canonical event types and normalization rules
    ingestion/
      ledger/            # source A connector
      processor/         # source B connector
      bank/              # source C connector
    matcher/
      scorer.go          # event-level scoring function (HANDWRITE)
      window.go          # Redis candidate window
      engine.go          # orchestration loop
    entity/
      resolve.go         # entity resolution (HANDWRITE)
      aliases.go         # alias extraction + candidate retrieval
    graph/
      materialize.go     # node/edge/graph-event upserts
      aggregates.go      # edge rollups
    grpcapi/
      graph.proto        # GraphQuery schema (HANDWRITE)
      server.go          # gRPC server
    store/
      postgres.go        # pgx queries, migrations
      redis.go           # sorted set operations
    observe/
      metrics.go         # metric collection
      tracing.go         # OpenTelemetry setup
  product/
    app/                 # Next.js app router pages
    components/          # graph canvas, panels, controls
    server/              # tRPC routers, auth, tenant resolution
    agents/
      operator/          # prompts + tool definitions (HANDWRITE)
      sandbox/           # prompts + tool definitions (HANDWRITE)
    lib/
      grpc/              # CORE client wrappers
      sandbox/           # generator + scenario mutation plumbing
  migrations/            # Postgres schema (core + product schemas)
  infra/
    cdk/
      lib/
        network-stack.ts
        data-stack.ts
        cluster-stack.ts
        core-stack.ts
        product-stack.ts
        observability-stack.ts
  docs/
    ARCHITECTURE.md
    DECISIONS.md
    BENCHMARKS.md
```

**Handwrite**: module boundaries, proto layout, migrations for core graph schema.
**Generate**: product app scaffolding, tRPC boilerplate, CDK and deployment manifests.

---

## 7) Canonical Event Format

This remains the internal contract. Connectors normalize to this. Matching and entity resolution start here.

```go
type CanonicalEvent struct {
    TenantID         string
    EventID          string    // globally unique, assigned by connector
    SourceType       string    // "ledger" | "processor" | "bank"
    SourceEventID    string    // original ID from the source system
    AmountMinor      int64
    Currency         string    // ISO 4217 when fiat
    AssetCode        string    // optional: "USD" | "USDC" | etc. for sandbox/demo cases
    Timestamp        time.Time // source-reported transaction time
    IngestedAt       time.Time
    Direction        string    // "debit" | "credit"
    AccountRef       string    // normalized account or wallet reference
    CounterpartyRef  string    // raw payee / customer / merchant descriptor
    Metadata         map[string]string
    IdempotencyKey   string    // tenant_id + source_type + source_event_id
}
```

### Normalization rules

- Amounts are in minor units. No floats.
- Timestamps are normalized to UTC.
- `CounterpartyRef` preserves the noisy real-world string that entity resolution must learn from.
- `AssetCode` is optional. V1 still assumes same-asset reconciliation within a candidate window; the crypto preset demonstrates multi-asset handling without turning CORE into a generalized FX engine.
- `TenantID` is mandatory on every event and every downstream row.

**Handwrite**: canonical contract, normalization rules, tenant and asset semantics.
**Generate**: connector parsers, field mappers, validation glue.

---

## 8) Counterparty Graph Abstraction

The graph is the central product model.

### 8.1 Graph primitives

**Nodes** — counterparties that accumulate profile data over time.

- Customers
- Vendors
- Processors
- Tax authorities
- Subsidiaries
- Employees

Each node stores identity hints, aliases, first/last seen timestamps, and profile metadata discovered over time.

**Edges** — financial relationships between a tenant-owned business entity and a counterparty.

- Aggregate payment history
- Typical amount
- Cadence
- Variance
- Reliability score
- Concentration share

**Graph events** — resolved business transactions.

- Derived from one or more canonical events after reconciliation
- Attach to exactly one edge
- Carry provenance back to the underlying source observations

**Compound edges** — pass-through relationships.

- Example: `Business -> Stripe` is the aggregate processor edge
- Underneath it, `Business -> Customer X` sub-edges can be linked with `intermediary_node_id = Stripe`
- This preserves both the processor view and the underlying customer relationship

### 8.2 Core Postgres schema

The original reconciliation tables stay in place and remain CORE-owned:

- `canonical_events`
- `matches`
- `match_events`
- `discrepancies`
- `metric_snapshots`

The graph layer extends that schema; it does not replace it.

```sql
CREATE TABLE counterparty_nodes (
    node_id        TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    node_type      TEXT NOT NULL,
    display_name   TEXT NOT NULL,
    legal_name     TEXT,
    profile        JSONB NOT NULL DEFAULT '{}',
    first_seen_at  TIMESTAMPTZ NOT NULL,
    last_seen_at   TIMESTAMPTZ NOT NULL,
    status         TEXT NOT NULL DEFAULT 'ACTIVE'
);

CREATE INDEX idx_nodes_tenant_type ON counterparty_nodes (tenant_id, node_type);
```

```sql
CREATE TABLE counterparty_aliases (
    alias_id          BIGSERIAL PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    node_id           TEXT NOT NULL REFERENCES counterparty_nodes(node_id),
    alias_text        TEXT NOT NULL,
    alias_normalized  TEXT NOT NULL,
    alias_type        TEXT NOT NULL,
    strength          REAL NOT NULL,
    first_seen_at     TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, node_id, alias_normalized, alias_type)
);

CREATE INDEX idx_alias_lookup ON counterparty_aliases (tenant_id, alias_normalized);
```

```sql
CREATE TABLE counterparty_edges (
    edge_id               TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL,
    src_node_id           TEXT NOT NULL REFERENCES counterparty_nodes(node_id),
    dst_node_id           TEXT NOT NULL REFERENCES counterparty_nodes(node_id),
    intermediary_node_id  TEXT REFERENCES counterparty_nodes(node_id),
    parent_edge_id        TEXT REFERENCES counterparty_edges(edge_id),
    relation_type         TEXT NOT NULL,
    first_seen_at         TIMESTAMPTZ NOT NULL,
    last_seen_at          TIMESTAMPTZ NOT NULL,
    payment_count         BIGINT NOT NULL DEFAULT 0,
    total_volume_minor    BIGINT NOT NULL DEFAULT 0,
    typical_amount_minor  BIGINT,
    cadence_days          REAL,
    variance_score        REAL,
    reliability_score     REAL,
    concentration_share   REAL,
    metadata              JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_edges_tenant_dst ON counterparty_edges (tenant_id, dst_node_id);
```

```sql
CREATE TABLE graph_events (
    graph_event_id     TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL,
    edge_id            TEXT NOT NULL REFERENCES counterparty_edges(edge_id),
    primary_match_id   TEXT REFERENCES matches(match_id),
    amount_minor       BIGINT NOT NULL,
    currency           TEXT NOT NULL,
    asset_code         TEXT,
    occurred_at        TIMESTAMPTZ NOT NULL,
    direction          TEXT NOT NULL,
    raw_descriptor     TEXT,
    provenance         JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_graph_events_edge_time ON graph_events (edge_id, occurred_at DESC);
```

### 8.3 Emergent product surface

The graph immediately makes these product surfaces possible:

- Duplicate vendor detection
- Cashflow forecasting per edge cadence
- Anomaly surfacing per edge distribution
- Counterparty concentration risk
- Payment reliability scoring
- New-counterparty alerts

These are outputs of the graph model. They are not all v1 build requirements.

**Handwrite**: graph schema, compound-edge representation, graph-event provenance model.
**Generate**: migration boilerplate, admin backfills, helper queries.

---

## 9) Reconciliation + Entity Resolution Engine

### 9.1 Candidate windowing (Redis)

When a canonical event is ingested and no immediate match is found, it enters the candidate window in Redis.

- Key: `candidates:{tenant_id}:{asset_code}:{amount_bucket}`
- Member: `event_id`
- Score: event timestamp (Unix millis)

Amount bucketing still uses exact amount and adjacent buckets (±1 minor unit) so fuzzy amount matching stays cheap.

### 9.2 Event-level scoring function (HANDWRITE zone)

The existing fuzzy matcher remains intact. It is the first half of the system's judgment.

```
match_score = w_amount * amount_score + w_time * time_score + w_account * account_score
```

- `amount_score`: 1.0 if exact, decays to 0.0 at `max_amount_tolerance`
- `time_score`: 1.0 within `min_time_delta`, decays to 0.0 at `max_time_delta`
- `account_score`: 1.0 exact account match, 0.5 substring, 0.0 otherwise

Default weights:

- `w_amount = 0.5`
- `w_time = 0.3`
- `w_account = 0.2`

Match confirms only if `match_score >= 0.85`.

### 9.3 Entity resolution (HANDWRITE zone, centerpiece)

This is now the core handwrite problem.

Goal: map a noisy real-world event string to the correct counterparty node.

Inputs:

- `CounterpartyRef`
- `AccountRef`
- Source metadata
- Recent edge history
- Prior aliases for the tenant

Candidate retrieval:

1. Exact alias hit from `counterparty_aliases`
2. Normalized-string fuzzy search against prior aliases
3. Recent nodes reached through the same account or intermediary
4. Recurring edge candidates with similar amount and cadence

Resolution score:

```
resolution_score =
    w_alias   * alias_similarity +
    w_account * account_hint +
    w_history * edge_history_fit +
    w_amount  * amount_profile_fit +
    w_recency * recent_activity_fit
```

Default weights:

- `w_alias = 0.45`
- `w_account = 0.20`
- `w_history = 0.20`
- `w_amount = 0.10`
- `w_recency = 0.05`

Resolution flow:

1. Normalize descriptor tokens
2. Retrieve candidate nodes
3. Score candidates
4. If top score clears threshold, attach event to that node
5. Otherwise create a new node, seed its first alias, and open a new edge
6. Persist resolution evidence into graph-event provenance and node alias state

This is where Tally turns noisy descriptors into a usable graph. The graph is only as good as this layer.

### 9.4 Match confirmation transaction (HANDWRITE zone)

When a match is confirmed:

1. Begin Postgres transaction (`SERIALIZABLE`)
2. Verify both events still have `match_status = 'PENDING'`
3. Insert into `matches` and `match_events`
4. Update both events to `MATCHED`
5. Run entity resolution for the reconciled business event
6. Upsert node / alias / edge / graph-event state
7. Remove matched candidates from Redis
8. Commit

If the transaction fails due to serialization conflict, retry once. The other transaction wins.

### 9.5 Late arrivals and discrepancies

Late arrivals still matter because real financial systems are asynchronous.

Flow:

1. Event arrives after counterpart aged out
2. CORE checks for plausible unresolved discrepancy
3. If a match is now valid, it resolves the discrepancy as `AUTO_RESOLVED`
4. Graph event provenance records that it was finalized through late-arrival reconciliation

Discrepancies are not terminal. They are explicit state in an eventually consistent system.

### 9.6 Idempotency and crash recovery (HANDWRITE zone)

The core invariants remain:

**Ingestion idempotency**

- `idempotency_key = tenant_id + source_type + source_event_id`
- `UNIQUE` constraint on canonical events

**Match idempotency**

- `SERIALIZABLE` transaction + `match_status = 'PENDING'` guard

**Crash recovery**

1. Rebuild Redis candidate window from Postgres pending events
2. Re-run expiry sweep for any events that crossed the window during downtime
3. Resume with Postgres as source of truth

Redis is rebuildable cache, not durable truth.

**Handwrite**: windowing/expiry strategy, event-level scorer, entity-resolution scorer, confirmation transaction, late-arrival semantics, crash recovery, idempotency guarantees.
**Generate**: Redis wrappers, connector glue, migration helpers, fixture generation for tests.

---

## 10) CORE gRPC API

The gRPC API is how PRODUCT sees the graph.

### 10.1 Service surface

```proto
service GraphQueryService {
  rpc SearchNodes(SearchNodesRequest) returns (SearchNodesResponse);
  rpc GetSubgraph(GetSubgraphRequest) returns (GetSubgraphResponse);
  rpc ExpandNode(ExpandNodeRequest) returns (ExpandNodeResponse);
  rpc GetNodeTimeline(GetNodeTimelineRequest) returns (GetNodeTimelineResponse);
  rpc CompareNodes(CompareNodesRequest) returns (CompareNodesResponse);
  rpc GetResolutionEvidence(GetResolutionEvidenceRequest) returns (GetResolutionEvidenceResponse);
  rpc GetLedgerIntegrityStatus(GetLedgerIntegrityStatusRequest) returns (GetLedgerIntegrityStatusResponse);
}
```

### 10.2 Required response traits

- Stable IDs for every node, edge, graph event, and discrepancy
- Summary metrics embedded where the UI needs them (`payment_count`, `cadence_days`, `variance_score`, `reliability_score`)
- Provenance fields on anything that came from reconciliation or entity resolution
- Tenant-scoped pagination and ranking support
- No write RPCs for operator-facing flows

### 10.3 Non-graph ops

CORE can still expose thin HTTP endpoints for:

- `GET /health`
- `GET /metrics/current`
- `GET /metrics/history`

Those are for operations and benchmarking, not the main product surface.

**Handwrite**: proto schema, response semantics, provenance fields, ranking/filter vocabulary.
**Generate**: server stubs, client wrappers, TypeScript adapters.

---

## 11) PRODUCT Surface: Operator Agent + Dynamic Graph Visualization

The product's primary UX is a conversational interface paired with a dynamic graph visualization. The agent is the primary interface; the dashboard is secondary.

### 11.1 Interaction loop

1. Agent surfaces an insight or the user asks a question
2. Agent emits a structured visualization operation
3. Graph animates to the new state
4. User points at a node or asks a follow-up
5. Agent investigates via graph queries, summarizes, and proposes actions

### 11.2 Visualization DSL (v1)

The agent does not mutate the graph canvas arbitrarily. It emits bounded operations.

```text
focus_subgraph(filter, rank_by, limit)
annotate_nodes(metric, threshold, style)
expand_node(node_id, view)
timeline(node_id, range)
compare(node_ids)
```

These five operations are enough for v1 and can expand later.

### 11.3 Operator-facing data the PRODUCT layer owns

- Annotations
- Saved views
- Operator notes
- Tags
- Pinned insights

These are not ledger truth. They are interpretations layered on top of it.

### 11.4 Permission boundary

- **Operator agent**
  - `READ-ONLY` on CORE-layer data: reconciliation decisions, ledger integrity, fuzzy matcher output, entity resolution, graph queries
  - `READ/WRITE` on PRODUCT-layer data: annotations, saved views, notes, tags, pinned insights
- Framing: **"the agent operates at the interpretation layer, never at the ledger-integrity layer."**

### 11.5 V1 deliverables

- Next.js + tRPC multi-tenant app shell
- Google sign-in
- React Flow graph canvas
- Claude-backed operator agent
- Structured DSL execution path
- Three hero demo flows

Hero flows:

- "Why did vendor concentration increase this quarter?"
- "Which customers are drifting off their usual cadence?"
- "Show me suspiciously duplicate vendors that might be the same counterparty."

**Handwrite**: agent prompt, tool definitions, visualization DSL, permission enforcement, hero demo flow design.
**Generate**: Next.js app wiring, React Flow wiring, tRPC scaffolding, auth plumbing, Claude SDK integration.

---

## 12) Multi-Tenant Sandbox With Presets

The deployed product is multi-tenant. Google sign-in should drop a user into a fresh sandbox tenant seeded with a realistic preset.

### 12.1 Tenant model

- Every CORE and PRODUCT row is tenant-scoped
- PRODUCT handles auth, membership, and sandbox lifecycle
- CORE enforces tenant scoping on every read path
- Sandbox tenants are cheap to create, reset, and destroy

### 12.2 Preset archetypes

**SaaS startup**

- Stripe payouts
- Infra costs
- Payroll
- VC wire

**E-commerce business**

- Shopify
- 3PL
- Ad spend
- Overseas suppliers
- Seasonal swings

**Freelance / agency**

- Lumpy client payments
- Contractor payouts
- Irregular operating rhythm

**Crypto-native business**

- USD + USDC flows
- On/off-ramp
- Exchange deposits

This preset demonstrates multi-asset handling in the graph without committing v1 CORE to a full generalized multi-currency engine.

### 12.3 Preset generation requirements

Each preset should generate **12–24 months** of believable history with story beats baked in:

- A churning customer
- A drifting vendor
- A duplicate counterparty
- A fraud-ish pattern

The generator is deliberately ambitious. This is where demo quality comes from.

### 12.4 Lifecycle deliverables

- Create sandbox tenant on first sign-in
- Seed preset automatically
- Reset sandbox from UI
- Switch presets
- Preserve PRODUCT-layer notes and views independently from regenerated CORE data when useful

**Handwrite**: tenant boundary rules, preset acceptance criteria, story-beat requirements.
**Generate**: auth, tenant plumbing, sandbox lifecycle, preset generator, reset flows.
**FULL VIBE-CODE ZONE**: data generator subsystem. No handwrite constraints beyond acceptance criteria.

---

## 13) Agentic Sandbox

This is separate from the operator agent — architecturally, in the UI, and conceptually.

### 13.1 Purpose

The sandbox agent is a dev/demo tool for manipulating synthetic businesses. It is not a production interpretation agent.

### 13.2 Capabilities

- Generate sandbox data on command
- Perturb existing sandbox state
  - "simulate customer churning"
  - "inject vendor fraud pattern"
  - "fast-forward 6 months respecting cadences"
- Scope adversarial scenarios for testing

### 13.3 Initial surface

- Landing page pre-sign-in demo feature
- Clearly marked sandbox mode
- Open question in spec: if v1 lands well, promote this into a full product feature for user-owned synthetic business experiments

### 13.4 Permission model

- Full read/write within the sandbox tenant's data
- Sandbox-mode only
- Broader than the operator agent by design because this is a demo and experimentation surface, not a production ledger interface

### 13.5 Separation requirements

- Separate prompt and tool definitions from the operator agent
- Separate UI entry point
- Separate permission guard
- No accidental escalation path from operator mode into sandbox mutation mode

**Handwrite**: sandbox-agent prompt, tool surface, sandbox-only guardrails, mode separation rules.
**Generate**: mutation plumbing, landing-page demo UI, scenario execution wiring.

---

## 14) Observability, Benchmarking, and Deployment

### 14.1 Observability

Keep the existing observability stance:

- Structured logging on all critical paths
- OpenTelemetry tracing
- CloudWatch alerting
- CloudWatch dashboard

Core metrics remain first-class:

- `events_ingested_total`
- `matches_confirmed_total`
- `discrepancies_opened_total`
- `match_latency_ms`
- `pending_window_size`
- `match_rate`
- `late_arrival_resolution_total`
- `entity_resolution_precision`
- `subgraph_query_latency_ms`

CloudWatch alarms should cover:

- Match rate degradation
- p99 match latency breach
- Discrepancy spike
- Pending window overflow
- Ingestion stall
- gRPC query latency regression

### 14.2 Benchmark harness

The benchmark harness is still a first-class deliverable. It now proves both reconciliation correctness and graph quality.

What it measures:

|Metric|How measured|
|---|---|
|Sustained throughput|Load generator events / duration|
|Match latency p50/p95/p99|From metric snapshots|
|Match rate|Confirmed matches / ground-truth pairs|
|False positive rate|Confirmed matches not present in ground truth|
|Discrepancy detection time|Window-expiry to discrepancy creation|
|Crash recovery time|Engine restart to Redis rebuild completion|
|Entity resolution precision|Resolved nodes vs seeded ground truth aliases|
|Graph query latency|gRPC query timings for `GetSubgraph`, `ExpandNode`, `CompareNodes`|

How it runs:

```bash
make bench
make bench TPS=5000 DUR=600
make bench-crash
```

Report sections:

- Throughput
- Match quality
- Resolution quality
- Latency
- Discrepancy detection
- Window behavior
- Crash recovery
- Graph query latency

### 14.3 Deployment

Keep **EKS Fargate**. The previous ECS reversal stays reversed.

Deployment shape:

- RDS Postgres
- ElastiCache Redis
- EKS Fargate for CORE and PRODUCT workloads
- CloudWatch for logs, metrics, alarms, dashboard
- AWS CDK for infra

Production artifact for v1:

- `tally.kaizhang.ca` landing page
- Live sandbox CTA
- Multi-tenant PRODUCT app
- CORE and PRODUCT deployed behind clean service boundaries

**Handwrite**: benchmark harness design, alarm thresholds, deployment acceptance criteria, dashboard contents.
**Generate**: OTel plumbing, CloudWatch definitions, CDK code, Kubernetes manifests.

---

## 15) Project Timeline (16 Weeks, May–End of August 2026)

Estimated structure: summer build concurrent with Super.com W-1.

### Phase 1: CORE foundations (Weeks 1–6, May–mid-June)

**Goal**: Finish the Go correctness layer and graph foundation.

Deliverables:

- Reconciliation engine per existing spec
- Entity resolution on top of fuzzy matcher
- Counterparty graph schema
- gRPC API exposing graph queries
- Preserved SERIALIZABLE semantics, idempotency, crash recovery

**Handwrite**: scoring function, match confirmation transaction, windowing/expiry, crash recovery, idempotency, entity resolution, graph schema, gRPC schema, benchmark harness design.
**Generate**: boilerplate connectors, Redis wrappers, migration runners.

**Exit check**: CORE can ingest correlated events, reconcile them correctly, materialize graph events and edges, and answer basic gRPC graph queries with provenance.

### Phase 2: Multi-tenant sandbox foundation (Weeks 7–9, mid-June–early July)

**Goal**: PRODUCT can create isolated tenants and seed believable businesses.

Deliverables:

- Google sign-in
- Tenant lifecycle
- Sandbox creation/reset flows
- Data generator
- Four preset archetypes

**Handwrite**: tenant isolation rules, preset acceptance criteria.
**Generate**: auth, tenancy plumbing, generator, preset wiring.

**Exit check**: a new user can sign in and land in a seeded sandbox tenant with 12–24 months of believable history.

### Phase 3: Product surface (Weeks 10–12, July)

**Goal**: The graph becomes explorable through an agent-first UI.

Deliverables:

- Next.js + tRPC app surface
- React Flow visualization
- Operator agent with 5 primitive operations
- Three hero demo flows

**Handwrite**: operator prompt, DSL, tool definitions, permission boundary.
**Generate**: product app scaffolding, graph wiring, Claude integration, auth UI.

**Exit check**: operator can ask a graph question, watch the graph animate, drill into nodes, and save interpretation-layer artifacts.

### Phase 4: Agentic sandbox (Weeks 13–14, August)

**Goal**: Demo and experimentation surface is real, separate, and useful.

Deliverables:

- Sandbox agent
- Sandbox Controls panel
- Landing-page pre-sign-in demo surface

**Handwrite**: sandbox prompt, guardrails, separation from operator mode.
**Generate**: scenario mutation plumbing, landing-page integration, controls UI.

**Exit check**: demo user can generate or perturb a sandbox business without touching production-style operator flows.

### Phase 5: Deployment + distribution (Weeks 15–16, mid–late August)

**Goal**: Ship the live artifact and the surrounding signal.

Deliverables:

- Deploy CORE and PRODUCT to EKS Fargate
- Production observability
- `tally.kaizhang.ca` landing page with live demo CTA
- LinkedIn post
- Coffee chat with CFM student

**Handwrite**: deployment acceptance criteria, benchmark interpretation, landing-page messaging.
**Generate**: infra code, deployment manifests, landing-page implementation.

**Exit check**: live multi-tenant sandbox is deployed, demoable, observable, and shareable.

---

## 16) Distribution and Career Targeting

### 16.1 Distribution

Replace the old blog-post-first plan.

- **Primary artifact**: the live deployed sandbox at `tally.kaizhang.ca`
- **Secondary artifact**: LinkedIn post at the end of the build
- **No blog post planned for v1**

### 16.2 Career signal

Updated framing:

- The **Go core** is the Stripe signal: depth, correctness, systems fundamentals
- The **product surface + agent** is the Slash signal: taste, AI-native product thinking, shipping velocity
- The **cohesive whole** is stronger than either half

Primary W-2 targets:

- Stripe
- Slash

Secondary targets:

- Intuit
- Wealthsimple
- Shopify
- Capital One
- Amazon
- Robinhood
- Ramp
- Coinbase
- Bloomberg

---

## 17) Tech Stack Summary

|Layer|Choice|Why|
|---|---|---|
|CORE language|Go|Correctness, concurrency, systems signal|
|CORE transport|gRPC|Clean service boundary, graph-shaped API contract|
|Ops HTTP|chi|Thin health/metrics surface without framework sprawl|
|Postgres driver|pgx|Raw SQL, SERIALIZABLE transactions, no ORM|
|Primary datastore|Postgres 16+|Ledger truth + graph schema + strong transactional semantics|
|Cache/window|Redis 7+|Sorted sets for candidate windowing, rebuildable from Postgres|
|Logging|zerolog|Structured JSON logs|
|Tracing|OpenTelemetry|Vendor-neutral tracing story|
|PRODUCT|Next.js + TypeScript|Fast product iteration and deploy ergonomics|
|App API|tRPC|Typed server/client contract inside PRODUCT|
|Graph UI|React Flow|Fast path to graph interaction and animation|
|Operator model|Claude API|Agentic UX for graph exploration|
|Infra|AWS CDK + EKS Fargate|Keep Kubernetes signal without node management|
|Metrics/alerts|CloudWatch|Operational story for live deployment|

---

## 18) Key Design Decisions Log (Seed Entries)

These belong in `docs/DECISIONS.md`.

**D001: Postgres over DynamoDB.** Tally needs strong relational semantics for reconciliation state, graph materialization, and serializable matching transactions. Tradeoff: less elastic than DynamoDB, but a better fit for correctness reasoning.

**D002: Redis as rebuildable cache, not source of truth.** If Redis dies, recovery is a cold-start performance hit, not a correctness failure.

**D003: Store the counterparty graph in the CORE Postgres schema.** The graph is not a presentation-layer cache. It is derived ledger truth with provenance and needs transactional coupling to reconciliation.

**D004: PRODUCT reads CORE via gRPC instead of shared SQL.** The boundary is explicit, reviewable, and easier to reason about than ad hoc cross-schema queries.

**D005: Keep scoring-based event matching.** The original fuzzy matcher remains the simplest correct primitive for reconciling multi-source observations.

**D006: Entity resolution is built on top of the fuzzy matcher, not as a separate black box.** This keeps the graph explainable and makes the fintech-fundamentals learning concentrated in one place.

**D007: EKS Fargate over ECS Fargate.** Keep the Kubernetes signal for Shopify / Wealthsimple / Intuit style environments without taking on EC2 node management.

**D008: Operator agent is read-only on CORE.** The interpretation layer should never be able to mutate ledger integrity.

**D009: Sandbox agent gets broader permissions, but only in sandbox mode.** This is acceptable because it is explicitly a synthetic-data tool, not a production operator.

**D010: CloudWatch embedded metrics + OTel over extra sidecars where possible.** Keep the deployment simple while preserving enough observability for a portfolio project and live demo.

---

## 19) Expansion Paths (Post-V1)

Ordered by leverage after the live sandbox ships.

### 19.1 Real data source integration

Replace one simulated source with a real connector (for example Stripe test-mode webhooks) while keeping the graph model and reconciliation semantics unchanged.

### 19.2 Backfill and replay

Re-run reconciliation and graph materialization over historical data when scoring or entity-resolution logic changes.

### 19.3 Generalized multi-asset core

Move from demo-grade `AssetCode` handling to a fully generalized multi-asset reconciliation model.

### 19.4 Human-in-the-loop corrections

Allow an operator to suggest alias merges or node annotations that feed future resolution while still preserving CORE ownership and auditability.

### 19.5 User-owned simulation mode

If the agentic sandbox lands, promote it into a real product surface where users can run controlled experiments on synthetic versions of their own businesses.
