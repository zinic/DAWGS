# V2 performance work 1: close the live V1/V2 benchmark gaps

## Status and authority

This document is the implementation plan for the performance gaps measured in
the retained V1/V2 live benchmark at
`.bench/runs/20260808T225900Z-v1-v2/`. It is subordinate to:

- `edge_kind_partitioning.md`;
- `edge_kind_partitioning_plan.md`;
- `gaps_plan.md`;
- `docs/edge_kind_partitioning_traceability.md`;
- `docs/adr/0004-database-incarnation-and-identity-epoch.md`;
- `docs/adr/0005-migration-role-model.md`;
- `docs/adr/0007-edge-identity-and-routing.md`;
- `docs/adr/0009-canonical-namespace-and-privileges.md`;
- `docs/adr/0011-supported-postgresql-majors.md`;
- `docs/adr/0012-operational-budgets.md`;
- `docs/adr/0013-rollback-restore-cleanup-and-retirement.md`; and
- the repository `AGENTS.md`.

The companion [`v2_perf_work_1_runbook.md`](v2_perf_work_1_runbook.md)
defines the temporary-PostgreSQL layout recipes, checkpoint rerun procedure,
retained artifact contract, and final performance gate for this plan.

This plan does not authorize V2 activation, relax a runtime-readiness or role
boundary, change the V1 retirement contract, or define a production rollout.
The existing `activationReady` interlock remains closed until the parent plan's
correctness, operational, evidence, and budget gates are complete. A completed
performance investigation may end in either a pass or a well-supported fail; a
fail leaves activation blocked.

The primary comparison is the frozen V1 runtime and layout at
`bd64d910ef1bfe01b886fa584796275e5c533c8e` versus a frozen V2 candidate. The
secondary comparisons isolate runtime guarding, client-side translation,
PostgreSQL planning/execution, partition topology, registry enforcement, and
migration cost. The primary comparison intentionally includes V2's required
security and correctness machinery; an optimization is not valid if it wins by
removing that machinery.

## Outcome sought

The current V2 result is a release blocker. The work should:

1. Remove the approximately 2 ms fixed floor from accepted, warm operations
   without allowing a stale or wrong connection to execute application SQL.
2. Preserve the server-side wins already demonstrated by typed partition
   pruning.
3. Recover exact typed-path pruning during relationship hydration.
4. Characterize and then reduce the topology-sensitive cost of untyped
   relationship counts and untyped traversal.
5. Measure write, registry, provisioning, storage, lock, WAL, and migration
   costs that the read-heavy smoke corpus does not cover.
6. Replace the disclosed benchmark-only shims with supported, tested rehearsal
   behavior so the final comparison runs the real candidate.
7. Produce reproducible PostgreSQL 16 and 18 evidence from which the numeric
   ADR 0012 budgets can be approved.

The implementation order is deliberate: fix measurement and the common runtime
floor first, remeasure, and only then tune individual SQL or physical indexes.
Otherwise a fixed boundary cost can hide whether a query optimization helped,
and tiny fixtures can encourage the wrong index design.

## Measured baseline

The retained run used PostgreSQL 18.4, five fresh-cluster rounds per layout, ten
timed iterations per case per round, and a counterbalanced V1/V2 schedule. Its
V2 input was an immutable archive of the dirty worktree, with original-source,
benchmark-source, fixture, binary, patch, and output checksums in
`manifest.json` and `SHA256SUMS`.

| Signal | Retained result | Meaning for this plan |
|---|---:|---|
| `cmd/benchmark` geometric mean | 4.855864x V2/V1 | All 25 cases regressed end to end. |
| GraphBench wall geometric mean | 4.448961x V2/V1 | All 11 comparable cases regressed end to end. |
| GraphBench planning geometric mean | 1.179238x V2/V1 | Planning is a secondary, shape-specific gap. |
| GraphBench execution geometric mean | 1.071059x V2/V1 | PostgreSQL execution does not explain the general wall regression. |
| Reported result-shape parity | All 25 command cases matched row/distinct/duplicate counts; all 11 comparable GraphBench cases matched row counts | Useful smoke evidence, but not exact value/path equivalence. |
| Symmetric corpus error | `length()` unsupported in both layouts | This is benchmark hygiene, not a V2 regression. |
| Typed pruning | 1/12 leaves for typed count and one hop; 4/12 for ADCS endpoints | Static kind predicates are reaching the planner. |
| Path-observed/untyped pruning | 12/12 leaves | Some all-leaf work is necessary; typed hydration is losing useful kind facts. |

The most diagnostic cases are:

| Case | V1 median | V2 median | V2/V1 | PostgreSQL evidence |
|---|---:|---:|---:|---|
| GraphBench all-node count | 0.172 ms | 2.538 ms | 14.79x | Plan/execution changed only 0.022/0.005 to 0.032/0.008 ms. |
| GraphBench typed-node count | 0.168 ms | 2.462 ms | 14.68x | Execution remained 0.012 ms. |
| GraphBench typed-edge count | 0.217 ms | 2.233 ms | 10.29x | One leaf; plan/execution changed only 0.184/0.020 to 0.190/0.025 ms. |
| `cmd/benchmark` base relationship count | 0.120 ms | 6.074 ms | 50.41x | No retained direct-API EXPLAIN; this is a diagnostic gap. |
| `cmd/benchmark` traversal relationship count | 0.159 ms | 5.820 ms | 36.58x | Untyped count must consider every kind leaf and both endpoints. |
| GraphBench ADCS endpoint IDs | 5.865 ms | 7.000 ms | 1.19x | V2 plan and execution were faster and used 4/12 leaves. |
| GraphBench ADCS observed path | 5.658 ms | 8.459 ms | 1.50x | Final path hydration expanded the plan to 12/12 leaves. |

The result has important limits:

- The fixtures contain only tens of rows and at most 12 measured V2 edge
  leaves. They are smoke workloads, not topology or capacity evidence.
- `scripts/test-postgres` ran with `fsync=off`, `synchronous_commit=off`, and
  `full_page_writes=off`. That is useful for disposable read attribution but
  cannot establish durable-write, WAL, crash, or migration budgets.
- V2 needed separately checksummed benchmark-only changes for activation,
  accepted-target schema setup, target `TEMPORARY`, graph provisioning, and
  GraphBench analyze normalization. None changed the shared worktree, but the
  final release comparison must not depend on ad hoc patches.
- The current comparison has PostgreSQL 18 evidence only. ADR 0012 and the
  parent plan require PostgreSQL 16 and 18.
- The retained comparison proves reported cardinality and duplicate-shape
  parity, not exact value/path equality for every case. Every optimization
  checkpoint therefore adds exact-result and logical-checksum validation before
  and after its timed blocks.

## Root-cause model and decision order

The work should test the following hypotheses in order. Each hypothesis has an
explicit falsification step; no later optimization should be justified only by
the current end-to-end ratio.

### H1: the accepted runtime guard dominates trivial operations

Confidence is high. An accepted V2 transaction currently performs five SQL
round trips before application work:

1. Base graph/node/edge and marker classification.
2. Required V2-object presence.
3. Full hierarchy, registry, trigger, and default-partition validation.
4. Marker, ownership, ACL, receipt, cleanup, and smoke coherence.
5. Live database, cluster, role, membership, and session identity comparison.

The implementation is split between `drivers/pg/internal/layout/state.go` and
`drivers/pg/layout.go`, and is repeated by read, write, batch, maintenance, and
direct-delete entry points. `requireOperational` is an in-memory check and is
not itself material. The GraphBench wall-versus-EXPLAIN gap is consistent with
the guard being the largest fixed component, but instrumentation must separate
server work, round-trip time, pool acquisition, translation, and row decoding
before assigning the entire residual to it.

Decision: first consolidate an accepted hot path to one exact, topology-bounded
statement backed by the authoritative runtime-contract token, and remeasure.
Keep exhaustive migrator validation off the application hot path. Do not begin
with a TTL, `sync.Once`, pool-global success bit, or a cache keyed only by
`identity_epoch`.

### H2: client-side parse/translate/scope/render work is a remaining fixed cost

Confidence is medium. `transaction.Query` parses and translates on every call.
V2 then deep-copies the PostgreSQL AST reflectively in
`scopePersistentRelations`, and shortest-path lowering scopes several nested
statements as well as the final statement. GraphBench EXPLAIN excludes this
work. Across the comparable cases, the captured V1 and V2 application SQL is
structurally equivalent after normalizing the stable V1 roots to V2's
graph-scoped parents, making client-side attribution more important than broad
SQL rewrites for the simple cases.

Decision: add phase microbenchmarks and allocation profiles. Replace reflective
scoping or add a compiled-query cache only if the phase evidence shows a
material gap after the guard is fixed.

### H3: typed path hydration discards known edge kinds

Confidence is high. Endpoint-only ADCS plans use the four referenced kinds,
while returning the same path uses all 12 leaves. `EdgeArrayFromPathIDs`
hydrates edges by ID from the graph-scoped edge parent without a kind predicate.
The final rejoin therefore loses kind facts that were present during expansion.

Decision: carry sound known-kind metadata into hydration and require the final
plan to touch the exact union of statically known kinds. Untyped expansion must
remain unfiltered; where `(edge_id, kind_id)` can be carried safely, test
execution-time routing rather than inventing a static predicate.

### H4: untyped relationship count has a separate topology-sensitive cost

Confidence is high that it is exceptional, but its breakdown is currently
missing. The V2 graph parent expands over every kind leaf, and the count fast
path joins both endpoint nodes. The endpoint checks are required: the existing
PostgreSQL integration case deliberately creates a dangling relationship and
requires `MATCH ()-[r]->() RETURN count(r)` to return zero. Removing the joins
would be a semantic regression.

Decision: add the missing plan capture, compare endpoint-preserving SQL shapes,
and measure at 1, 12, 30, 64, and the approved maximum leaf count. Consider a
narrow exact registry read model only if ordinary SQL cannot meet the approved
budget and its write/migration costs are acceptable.

### H5: topology, registry, and index costs are under-measured

Confidence is high. The current suite demonstrates pruning on 12 leaves but
does not establish planning growth, relcache/catalog pressure, lock headroom,
autovacuum behavior, registry contention, write amplification, or storage at
the required topology matrix.

Decision: preserve the current indexes until the scaled plan and write evidence
exists. Index changes are a later, separately attributable work package.

## Non-negotiable invariants

Every work package must preserve these rules:

- The first application statement on an acquired physical connection cannot
  execute unless that same connection satisfies the current layout, database
  incarnation, target identity, direct-role, smoke, cleanup, and activation
  contract.
- New or replaced physical backends, restore/rebind, pool reset, role change,
  identity-epoch change, topology drift, and uncertain commit cannot inherit a
  stale success.
- A refusal preserves the existing typed `LayoutError`, stable code, and
  recovery guidance. Diagnostic fallback is allowed; fail-open behavior is not.
- Public runtime `Readiness`/`LayoutHealth` remain fresh, bounded operational
  inspections and are never answered from a cached success. Exhaustive physical
  validation remains a separate migrator/operator workflow and never runs on
  every application open or transaction boundary.
- A one-kind static predicate touches exactly its leaf; a multi-kind predicate
  touches only its exact leaf union; an untyped predicate is never given an
  unsound filter.
- Graph isolation, relationship endpoint semantics, path order, duplicates,
  properties, direction, and shortest-path semantics remain exact.
- Counts remain transactionally exact. `reltuples`, sampled statistics, or
  eventually consistent counters cannot back the public exact count APIs.
- Graph, kind, default-graph, leaf, and compiled-query caches publish only after
  successful commit and invalidate on unknown commit outcomes.
- Shared integration cases remain backend-equivalent. PostgreSQL-only physical
  capability tests stay in clearly PostgreSQL-scoped suites.
- No benchmark may wipe an application, shared semantic-test, or otherwise
  unowned database.
- The production activation interlock is not flipped merely to run this work.

## Work-package dependency map

```text
WP0 supported rehearsal + immutable protocol
                |
WP1 phase attribution and missing cases
                |
WP2 one-statement runtime guard
                |
       checkpoint and full rerun
          /             \
WP3 client pipeline   WP4 query/pruning work
          \             /
       checkpoint and topology rerun
                |
WP5 topology/storage + WP6 writes/lifecycle/migration
                |
WP7 PG16/18 confirmation and ADR 0012 budgets
                |
WP8 CI, documentation, and G23 handoff
                |
       activation review outside this plan
```

WP3 and WP4 may be developed independently after WP2's attribution rerun, but
their benchmark evidence must be reported separately before combining them.

## WP0: make the comparison supported and reproducible

### WP0.1 Freeze the inputs

For every optimization checkpoint:

- Name one decision owner for contract interpretations and performance-budget
  decisions across the entire effort, plus a separate operator approver for
  acceptance, rollback, restore, and cleanup procedures. Also name the
  runtime/security reviewer and retained-evidence owner. Record each required
  operator approval in the transition evidence, and record unresolved choices
  as blockers rather than silently supplying defaults.
- Keep the V1 source fixed at
  `bd64d910ef1bfe01b886fa584796275e5c533c8e` and retain its frozen schema
  checksums from the traceability ledger.
- Archive the exact V2 source, including untracked files, before building it.
  Record the archive SHA-256 and a sanitized source-status inventory.
- Record binary SHA-256 values and `go version -m` for the V1 runner, V2
  runner, GraphBench runner, migrator, and any rehearsal helper.
- Version and hash the case manifest, fixtures, V1 adapter, V2 adapter,
  analyze protocol, statistics method, and allowed comparison differences.
- Freeze the hardware/provider classes, approved maximum topology, published
  row counts, skew distributions, constraint cardinalities, concurrency
  levels, and whether the Retriever live-scan suite is in scope.
- Reject comparisons when case IDs, logical operations, fixtures, parameters,
  seeds, units, warmup, repetition, schedule, timeouts, or relevant environment
  facts differ.
- Permit layout-specific adapter hashes to differ only through an explicit,
  versioned mapping. The current `cmd/benchdiff` rule that simply requires an
  identical adapter tree cannot compare the frozen V1 artifact with the V2
  adapter safely.

### WP0.2 Implement the real transition harness

Replace the current `benchmark_transition` package-benchmark alias with an
orchestrator that:

1. Uses only invocation-owned instances from `scripts/test-postgres`.
2. Creates independently named V1 comparator and V2 migration/rehearsal
   clusters per round, records their non-secret settings, and installs cleanup
   traps that call the script's exact `down` operation.
3. Loads the same deterministic frozen V1 logical fixture into both sides and
   verifies pre-migration row and logical checksums.
4. Leaves one side on the frozen V1 runtime/layout.
5. Takes the other through the complete role-aware transition choreography:
   preliminary preflight and one-use confirmation; fence installation;
   independent verify-fence; external-writer inventory, credential retirement,
   and probe; authoritative fenced-report review; renewed resume confirmation;
   bounded resume/copy to pending; exhaustive validation and accept
   confirmation; acceptance with the required owner/target handoff and
   coordinator retirement; then direct target-role smoke to accepted V2. When
   measuring only steady-state layout cost, use the complete canonical
   empty-V2 bootstrap/confirmation/handoff/smoke choreography instead. The
   report must identify which estimand ran and retain every confirmation and
   receipt.
6. Uses distinct comparator writer, migration writer, operator, target,
   installation owner, and preparation identities. Retiring or fencing the V2
   clone's writer cannot affect the V1 comparator.
7. Closes operator resources before the V2 application runner starts and gives
   that runner only the accepted target DSN.
8. Runs counterbalanced V1/V2 rounds without overlapping timed workloads.
9. Copies retained outputs outside the ephemeral cluster directories before
   teardown and emits a final checksum manifest.
10. Fails if teardown leaves a script-owned instance or randomized role behind.

Add an explicit harness profile for fast disposable read attribution and a
separate durability-on profile for writes, WAL, disk, migration, and crash
tests. Both profiles retain the same safe instance ownership and teardown; the
report must never describe the fast profile as durable or crash representative.
PostgreSQL 16 is selected through the script's supported `--bin-dir` mechanism
or a corresponding CI image, and every run asserts `server_version_num`.

### WP0.3 Remove the reasons for benchmark-only runtime shims

Implement and test supported behavior for these currently disclosed deltas:

- Split schema installation from accepted-layout reconciliation.
  `AssertSchema` on an accepted restricted target must not replay the full
  owner-only schema script or require `CREATE` on `public`; it should read and
  reconcile graphs/kinds/leaves only through the accepted target contract and
  canonical security-definer helpers. Uninitialized installation remains an
  explicit owner/operator path.
- Decide the shortest-path temporary-table contract without treating the
  benchmark's out-of-band `TEMPORARY` grant as authority. Prefer removing or
  safely encapsulating the temporary-table dependency within the locked role
  model. If the recorded target truly must receive database `TEMPORARY`, handle
  it as an explicit role-contract change with ADR/threat-model review, role and
  ACL matrix updates, fast-guard/classifier checks, migration/bootstrap/restore
  grants, frozen rollback reconstruction, runbook changes, and hostile
  `pg_temp` tests. Do not grant it only in the benchmark.
- Provision benchmark graphs through `dawgs_provision_graph`, as the real target
  must, rather than allowing direct schema creation.
- Make GraphBench's analyze normalization canonical and symmetric: analyze the
  physical graph node partition and every active edge leaf after each load, and
  record the analyze state/timestamp.
- Treat the shared `length(path)` case as either a tested expected-error case
  excluded from timing or implement the missing backend-equivalent function in
  a separate semantic change. Do not count a symmetric unsupported error as a
  performance pass.
- Provide a non-shipping, unexported rehearsal activation seam if performance
  must be measured before the release interlock opens. Its build tag/delta and
  binary are checksummed, release builds prove that it is absent, and the final
  confirmation reruns the exact promoted artifact after the legitimate
  activation decision.

### WP0 exit gate

- A smoke transition comparison runs without hand-edited source patches.
- The normal production build remains activation-blocked.
- The V1 and V2 fixtures have equal logical checksums before timing.
- The harness tears down both temporary PostgreSQL instances on success,
  failure, timeout, and interrupt.
- A retained manifest is sufficient to reproduce the build, database profile,
  cases, order, and comparison.

## WP1: measure each layer before changing it

### WP1.1 Add fixed-cost microbenchmarks

Add accepted-target live cases for:

- pool acquire and release only;
- an empty `ReadTransaction` delegate;
- `ReadTransaction` plus `SELECT 1`;
- an empty write transaction and commit;
- `Raw("select 1")` through each supported public entry path;
- first operation on a newly established backend;
- warm operation on a reused backend;
- operation after pool reset/backend replacement;
- bounded public `Readiness`/`LayoutHealth`, reported separately from the
  transaction hot path; and
- explicit exhaustive migrator validate/health, reported as an operator action
  rather than an application-boundary cost.

Record statement count as well as time. Tag internal guard statements with
stable, bounded comments so `pg_stat_statements` or the pgx tracer can separate
them without exposing DSNs, graph names, kind names, or application values.

### WP1.2 Add client-pipeline microbenchmarks

For node count, typed edge count, untyped edge count, one hop, typed and untyped
variable path, ADCS endpoint, ADCS path, and shortest path, measure:

1. Cypher parse.
2. Optimizer planning.
3. Translation before relation scoping.
4. Persistent-relation scoping.
5. SQL formatting and parameter materialization.
6. pgx statement-cache lookup/prepare.
7. Server planning.
8. Server execution.
9. Row scan/hydration.
10. Commit or release.

Report ns/op, bytes/op, allocations/op, p50, p95, and the number of emitted SQL
statements. Profiles must distinguish a cold parse/prepare from a warm repeated
query.

### WP1.3 Close the missing query evidence

- Add GraphBench cases for untyped edge count, direct node/relationship count
  API equivalents, multi-kind edge count, and typed path hydration.
- Capture structured JSON EXPLAIN for direct query-builder cases, not only cases
  with a stored Cypher string.
- Record planned, removed, and executed leaves, relation/index inventory,
  planning/execution time, buffers, and generated SQL.
- Define a canonical, order-aware or order-insensitive result digest per case as
  its semantics require. Compare exact scalar values, IDs, paths, edge-kind
  sequences, properties, duplicates, and errors before and after each timed
  block; row count alone is insufficient.
- Add a no-op and a simple node query to `cmd/benchmark` so the same report can
  display boundary versus application time.
- Make the report show `wall - measured client phases - PostgreSQL plan/exec` as
  an unattributed residual. A large residual is an instrumentation bug or a new
  hypothesis, not automatically "network".

### WP1 exit gate

- The sum of recorded phases accounts for the end-to-end sample within a
  documented clock/instrumentation tolerance.
- The current five guard statements are visible separately from application
  SQL.
- Untyped relationship count and typed path hydration have retained plans.
- Cold backend, warm backend, cold statement, and warm statement are separate
  populations.

## WP2: reduce the accepted runtime gate to one bounded exact statement

This is the highest-priority implementation package.

### WP2.1 Centralize guarded acquisition

Introduce one internal guarded-connection abstraction used by:

- read and write transactions;
- batch operations;
- `Run` and `Raw` entry paths;
- schema/default-graph/kind operations;
- direct set-based deletes;
- optimize/wipe/maintenance paths; and
- tools that acquire a physical connection directly.

Remove copy-pasted `classifyConnection`/`requireOperational` sequences. Preserve
caller-provided pgx pool hooks and support externally constructed pools; a
correctness guarantee cannot depend only on `pg.NewPool` having installed a
hook. Add a query-recorder test proving every promoted database-touching entry
point passes through the guard and that a rejected entry never invokes its
delegate.

Fold composite-type readiness into the same state machine. Prebootstrap/raw
operator connections must not issue managed `LoadType` queries or churn in the
release hook. After the guard accepts an exact target connection, lazily load
the required schema-qualified composite types once for that physical backend
and mark them against the current runtime fingerprint. Reconnect, restore,
fingerprint change, or pool reset invalidates the mark. Compose caller hooks;
the guarded acquisition remains authoritative for external pools that never
used `pg.NewPool`.

### WP2.2 Implement an accepted-V2 fast guard

First define the authority for the bounded runtime-contract token. If this
requires new layout generation/digest columns or a provider-supplied
incarnation, treat it as a versioned schema/contract change: bind it into
preflight, acceptance and smoke receipts; advance it only in the same
transaction as every supported mutator; and update bootstrap, migration,
rollback, restore/rebind, cleanup, classifier, schemas, fixtures, and reports.
A client-computed process-local generation is not authoritative.

Build one fixed, schema-qualified SQL statement that returns all facts required
to accept a healthy V2 operation. It should:

- combine the bounded runtime-contract token/sentinels, marker/coherence,
  database/cluster, role, membership, ACL, smoke, cleanup, rebind, and target
  checks required at the application boundary;
- obtain repeated facts such as `pg_control_system()` and current role/database
  identity once in CTEs;
- return a fixed number of rows and use a topology-independent bound on catalog
  lookups, rows visited, and buffers; it must not scan every graph, leaf,
  identity claim, property claim, cleanup object, or receipt on the accepted
  hot path;
- compare an authoritative runtime-contract/topology generation and digest
  established by acceptance/deep validation, rather than recomputing the full
  catalog fingerprint on every operation;
- be safe for pgx statement caching;
- run on the exact acquired physical connection before application SQL; and
- preserve the existing in-memory `requireOperational` decision and typed
  errors.

On accepted, healthy V2 the fast statement is the only guard round trip. Every
supported mutator of the runtime contract must update the authoritative token
transactionally. If direct owner/operator DDL remains supported, it must also
advance that token through a proven database/provider mechanism; otherwise the
runbook and privileges must prohibit it. If the parent next-operation drift
contract cannot be satisfied with a topology-independent token, this design is
blocked and the contract/architecture must be resolved explicitly rather than
silently replacing exact checks with stale acceptance.

If the statement cannot parse because V2 objects are absent, returns an unknown
or incoherent state, or fails an exact check, run the existing exhaustive
classifier only to produce the detailed typed diagnosis. No application SQL
may execute between fast refusal and diagnostic fallback. Full migrator
validation remains the authority for exhaustive physical topology, claim, and
receipt inspection; it is not moved into the accepted hot statement.

Do not pipeline an independent guard and application mutation unless a single
transactional protocol proves that guard failure prevents the mutation. Also
measure and resolve the current check/use ordering: writes validate before
`BeginTx`, while reads issue several autocommit statements. The optimized path
must not enlarge that race to save a round trip.

### WP2.3 Remeasure before caching

After query consolidation, rerun WP1 and the full Tier A suites. The next design
depends on the result:

- If one bounded exact round trip meets the approved guard budget, stop. Prefer
  the simpler check.
- If immutable per-backend calls dominate, cache only physical-backend-stable
  facts such as database OID/name, system identifier, backend identity, and
  initial `session_user`. Still query all mutable authorization/readiness facts
  at every boundary.
- If an authoritative generation cannot cover every supported mutation source,
  do not add a TTL or topology cache. Resolve the support/validation contract
  before continuing.
- Consider a server guard function or a guard CTE fused into application SQL
  only as a final, separately reviewed step. It must cover reads, writes, raw
  SQL, EXPLAIN, prepared statements, and stable SQLSTATE-to-`LayoutError`
  mapping.

### WP2.4 Conditional cache contract

Any cache introduced by this work must be bounded and keyed by physical
connection/backend identity, database incarnation, and the complete accepted
runtime contract. It is not keyed only by DSN, role name, layout version,
`identity_epoch`, marker `xmin`, or elapsed time.

Required eviction/invalidation events include:

- new or replaced backend and backend PID change;
- pool reset/close and connection error;
- cancellation or timeout during validation;
- transaction commit failure or unknown outcome;
- schema assertion, graph/leaf provisioning, migration, restore, or rebind;
- state, accepted scope, identity epoch, target, smoke, cleanup, or receipt
  change;
- role rename/replacement, membership, ownership, superuser, session/current
  user, or required ACL change; and
- graph parent, leaf registry, trigger, partition bound/default partition, or
  required object change.

`identity_epoch`, a marker-row `xmin`, a TTL, and process-local invalidation
each miss external catalog or authorization changes. A full structural cache is
therefore allowed only if an authoritative database generation/digest advances
transactionally for every relevant layout, registry, DDL, ownership, ACL,
receipt, cleanup, restore, and target-role change. Direct managed-object DDL
must either be prohibited or proven to advance it. Without that proof, retain
the current exact structural check, do not introduce the cache, and treat WP2's
performance/architecture gate as failed until the conflict is resolved.

### WP2.5 Tests

Unit and query-shape tests:

- Healthy accepted V2 issues exactly one hot guard statement and no diagnostic
  statements.
- Every refusal retains its current `errors.Is` identity and stable error code.
- Raw session-authorization and role-changing statements are rejected before
  they can invalidate the direct-target identity invariant.
- Missing functions/relations, malformed rows, timeouts, and cancellations
  never cache success.
- Concurrent cold misses are race-free and do not hold a process mutex during
  database I/O.
- Cache equality and bounded eviction are deterministic if a cache is added.
- Public readiness always performs a fresh bounded inspection; the migrator's
  explicit validate/health path performs fresh exhaustive inspection.

Live mutation tests must alter one fact between two operations and require the
very next operation to reject before delegate/application SQL. Cover:

- state, accepted scope, smoke status/digest, cleanup status/digest, identity
  epoch, database identity, and restore/rebind;
- dropped/detached/unregistered/extra leaves, changed bounds, default
  partition, registry row, required trigger, owner, and ACL;
- target rename/replacement, membership grant, superuser, database owner, and
  startup/runtime `SET ROLE`;
- backend replacement, pool reset, separate-process pools, failed commit, and
  ambiguous commit; and
- all exported database-touching paths listed in WP2.1.

### WP2 exit gate

- The healthy warm path performs one guard statement with no classifier
  fallback.
- Guard EXPLAIN rows, catalog lookups, and buffers stay within the same approved
  bound at 1, 30, 64, and maximum leaf topology; statement count alone is not a
  pass.
- No stale-acceptance test permits one application statement.
- As a local optimization checkpoint, the measured non-plan fixed floor is at
  least 80% lower than the retained approximately 2 ms V2 floor. This is a
  triage target, not an activation budget.
- All 25 command cases and 11 comparable GraphBench cases retain their reported
  shape/cardinality parity, and the new exact value/path/checksum comparisons
  pass before and after every timed block, with no new error.
- Final absolute and V1-ratio acceptance remains governed by the numeric
  budgets approved in WP7/ADR 0012.

## WP3: remove avoidable client-side query cost

### WP3.1 Replace reflective persistent-relation scoping if it is material

Prefer making graph-scoped persistent relations explicit translator
configuration so table references are emitted correctly at construction time.
If that would create excessive churn, implement a typed, exhaustive rewriter
that avoids reflection and repeated whole-tree copies. In either design:

- persistent node and edge references become `public.node_<graph_id>` and
  `public.edge_<graph_id>` structurally, never through string substitution;
- nested statements and every `EdgeArrayFromPathIDs` receive the same scope;
- source ASTs remain reusable or have an explicit ownership contract;
- parameters, literals, comments, SQL-looking strings, temporary relations,
  and nonpersistent CTE bindings are not rewritten; and
- graph IDs retain their positive-int32 validation and injection-safe format.

Benchmark and test simple counts as well as the repeated scoping performed by
shortest-path primer, recursive, backward, pair-filter, root-filter, terminal-
filter, and final statements.

### WP3.2 Evaluate compiled translation caching only after scoping work

The candidate cache unit is an immutable parsed/optimized/translated SQL
template with per-invocation parameters. Its key must include at least:

- exact Cypher text or canonical AST digest;
- graph ID;
- kind-map generation/digest;
- translator/lowering version and options;
- literal-stripping/materialization mode; and
- any layout/topology fact that changes emitted physical targets.

Do not share parameter maps or mutable AST nodes between calls. A kind refresh,
default-graph change, graph recreation, restore/rebind, translator change, or
uncertain commit must miss or invalidate. Bound the cache and expose only
low-cardinality hit/miss/eviction metrics. Compare this with pgx's existing
prepared-statement cache; do not add a second cache if parse/translation is not
material after WP3.1.

### WP3.3 Tests and exit gate

- Add Go microbenchmarks and allocation assertions for every WP1.2 shape.
- Test the same query across two graphs, across a kind refresh, and after pool
  reset/restore so graph-specific SQL cannot leak.
- Mutation and template suites cover create/update/delete flags, required edge
  leaf IDs, graph isolation, and generated artifacts.
- Race tests cover concurrent cache hit/miss/eviction if caching is implemented.
- As a local checkpoint, V2 parse/optimize/translate/scope/render time and
  allocations should be within 10% of the frozen V1 path for the same logical
  shape, or the retained report must justify a larger absolute cost. This is not
  a substitute for ADR 0012 end-to-end budgets.

## WP4: close query- and partition-specific gaps

### WP4.1 Preserve already-good simple and typed plans

Do not rewrite node counts or typed edge counts merely because their wall ratios
are large. Their PostgreSQL plans are already close to V1 and typed edge count
prunes to one leaf. Treat any change to them as a regression unless plan,
execution, or scaled-topology evidence says otherwise.

For every query change, test literal kinds, named parameters, forced custom
plans, and forced generic plans. A planner-visible constant may help a literal
case while a generic parameter silently visits all leaves.

### WP4.2 Restore typed path-hydration pruning

Extend the path lowering model so each fixed or variable relationship segment
can carry a sound optional set of known kind IDs into final edge hydration.
Render the final `EdgeArrayFromPathIDs` join with a planner-visible
`kind_id = ANY(...)` predicate for the exact union of known kinds.

For untyped paths, evaluate carrying `(edge_id, kind_id)` alongside path IDs or
joining through the existing `edge_identity` mapping to enable execution-time
partition routing during hydration. The initial untyped expansion must still
visit all candidate leaves. Do not infer kind from an edge ID alone.

Required cases include:

- single- and multi-kind fixed length;
- single- and multi-kind variable length;
- mixed fixed and variable segments;
- optional, inbound, outbound, and directionless patterns;
- shortest and all-shortest paths;
- endpoint-only versus full path/entity/property observation; and
- paths with repeated edges/nodes and duplicate result rows.

Exit criteria:

- ADCS path-observed plans and executes no more than the four statically known
  kind leaves used by its expansion.
- In every typed case, the final hydration leaf set equals the exact union of
  referenced kinds.
- Untyped cases receive no unsound static filter.
- Path order, kind sequence, duplicate behavior, properties, and backend-
  equivalent results remain unchanged.

### WP4.3 Diagnose and optimize untyped relationship counts

First add the missing GraphBench/direct-API plan evidence at 1, 12, 30, 64,
and approved-maximum leaves. Keep fixtures with both valid and dangling
relationships. Compare at least:

- the current two endpoint joins;
- equivalent `EXISTS` semijoins;
- pre-aggregation per edge leaf followed by exact endpoint filtering where
  semantics allow it;
- partition-wise join/aggregate settings and custom/generic plans; and
- registry-assisted formulations that remain exact.

Do not remove endpoint existence checks. If normal endpoint-preserving SQL
cannot meet the approved budget, evaluate extending a narrow nonpartitioned
identity/read registry with the facts needed to count only relationships whose
endpoints exist. That is a schema and write-path change, so it requires:

- root, direct-leaf, `COPY`, update, delete, cascade, rollback, and concurrent
  trigger consistency;
- migration, validation, restore/rebind, cleanup, and frozen rollback
  equivalence;
- exact MVCC and transaction visibility;
- bounded registry bloat and autovacuum behavior; and
- proof that write amplification and hot-key contention remain within their
  budgets.

Do not introduce an approximate count or a hot per-graph counter as a shortcut.

Exit criteria:

- Typed count still plans exactly its requested leaf set.
- Untyped count matches V1 for valid, dangling-start, dangling-end, duplicate,
  empty, and concurrent fixtures.
- Planning growth versus leaf count is recorded and remains within the WP7
  approved topology budget.
- Any registry-based win includes its write, WAL, contention, migration, and
  storage cost in the same decision record.

### WP4.4 Characterize unavoidable untyped traversal fanout

An untyped variable-length expansion has no sound static kind constraint and
must initially consider all graph leaves. Measure rather than conceal that cost:

- depth 1, 3, 10, and the supported maximum;
- chain, fanout, cycle, disconnected, uniform, skewed, and severe-skew data;
- ID-only, endpoint-only, path, entity, and property-observing projections;
- one, 12, 30, 64, and approved-maximum leaf topologies; and
- forced custom/generic plans with cold and warm relcache/statement states.

If planning or execution exceeds the approved budget after WP2 and path
hydration work, prototype an auxiliary adjacency/read structure only as a
separate architectural decision. It must include write, mutation, migration,
rollback, recovery, and consistency costs; a read-only benchmark win is
insufficient.

## WP5: topology, physical design, and maintenance

Run the parent plan's minimum topology matrix and add intermediate diagnostic
points:

- 1 graph x 1, 10, 12, 30, and 64 kinds;
- several graphs x 30 kinds;
- the approved maximum graph/kind/leaf topology;
- fixed published row counts at small, representative, and capacity scales;
- uniform, skewed, and severe-skew edge distributions; and
- zero, one, and many property constraints.

For each point record:

- total relations, partitioned relations, leaf tables, indexes, sequences, and
  toast relations;
- relation and index bytes by logical component;
- planning/execution time and buffers by query class;
- catalog and relcache warmup, connection startup, and prepared-statement
  invalidation;
- `max_locks_per_transaction` demand and verified safety headroom;
- autovacuum/analyze state, dead tuples, freeze age, and bloat;
- maintenance elapsed time per graph and per leaf; and
- planned, removed, and executed leaf counts.

Only after this evidence, compare leaf-aware index candidates against the
correctness-release index set. Candidate questions include whether a
kind-leading index is redundant inside a single-kind leaf and whether traversal
covering indexes should be attached once at the partitioned parent. Any removal
must retain typed counts, endpoint joins, directionality, uniqueness, migration
validation, and rollback reconstruction. Measure read improvement together with
insert/update/delete, storage, WAL, vacuum, and build cost.

WP5 exits only with a selected index set supported on both PostgreSQL 16 and 18,
or an explicit decision to keep the correctness-release set because no
candidate clears the combined read/write/storage budgets.

## WP6: writes, provisioning, registries, and migration

### WP6.1 Separate lookup from provisioning

Audit every `EnsureEdgeKindPartitions` call and classify it as read lookup,
known-existing write routing, or actual provisioning. A read must not perform
DDL, `GRANT`, registry writes, or provenance updates. A missing kind on a read
normally means an empty leaf result; creation belongs to schema assertion or a
write that needs the leaf.

The current provisioning function performs catalog reads, an `INSERT ... ON
CONFLICT DO UPDATE`, topology validation, and `GRANT` even when the leaf already
exists. Add an idempotent fast path that returns an already active, canonical,
properly authorized leaf without rewriting its registry row or repeating DDL/
ACL work. Update provenance, generation, or grants only when a real state
change requires it. Publish any process cache only after the creating
transaction commits.

Measure uncontended existing-leaf lookup, new-leaf creation, concurrent same-
leaf creation, concurrent different-leaf creation, schema-lock wait, failure,
rollback, and unknown commit.

### WP6.2 Measure write and registry enforcement

Cover direct, batch, and Cypher paths with automatic and explicit IDs, batch
sizes from one through the supported production range, and zero/one/many
property constraints. Measure:

- nodes and edges per second, median/p95 latency, allocations, and round trips;
- `edge_identity` and property-claim trigger time;
- hot edge-ID and hot unique-property conflicts, retries, lock waits, and
  deadlocks;
- root inserts, canonical direct-leaf inserts, and `COPY` where supported;
- update, kind-routing refusal, delete, cascade, wipe, mass mutation, and
  constraint backfill;
- registry bloat, autovacuum, and vacuum/reindex maintenance; and
- WAL, durable bytes, temporary bytes, and index growth under the durability-on
  harness profile.

Require zero duplicate identities, orphan claims, mutable routing keys,
cross-graph effects, unexpected deadlocks, or unbounded retry loops.

### WP6.3 Measure migration and operational costs

From identical frozen V1 snapshots on PostgreSQL 16 and 18, record:

- preflight and fence elapsed time;
- bounded-copy throughput and checkpoint overhead;
- atomic cutover lock duration;
- validation time and resource use;
- WAL, temporary disk, durable target/index disk, archive pressure, and peak
  capacity;
- relationship registry/index build time;
- post-acceptance smoke and readiness time;
- pre-acceptance rollback duration and exact frozen-V1 reconstruction;
- restore/rebind validation and cache invalidation; and
- cleanup-map and retained-V1 storage through the mandatory post-acceptance
  soak and explicit identity-mapped cleanup.

In-place rollback ends at acceptance. Post-acceptance failure handling is a
forward fix or an approved restore/rebind workflow; destroying an accepted
disposable benchmark database is cleanup, not evidence of production rollback.
Retain legacy storage through the required soak/cleanup evidence even though it
is no longer an in-place rollback target.

Before each migration/capacity run, calculate free-space requirements for the
source, V2 target, all indexes/registries, WAL/archive, temporary work, retained
legacy storage, validation, and the applicable rollback or restore reserve.
Freeze automatic pause and abort thresholds before starting. Exercise them with
fault injection and require the migrator to stop before the approved reserve is
consumed.

These measurements use durability-on PostgreSQL settings. The existing fast
disposable profile cannot supply their activation budgets.

## WP7: confirmation matrix and numeric budgets

### WP7.1 Freeze budgets before the confirmatory run

Use exploratory PostgreSQL 16/18 results, production SLO requirements, and
capacity constraints to replace ADR 0012's proposed placeholders with approved
numbers. Do not choose thresholds after examining the independent confirmatory
run.

At minimum approve values for:

- accepted hot-guard median and p95;
- end-to-end median and p95 V2/V1 ratio by query class;
- absolute read and write latency/throughput;
- planning time versus graph/kind/leaf topology;
- leaf-provision latency and schema-lock wait;
- registry contention/retry/deadlock rates;
- relation/index/storage growth and relcache/catalog startup;
- `max_locks_per_transaction` safety margin;
- migration copy rate, cutover lock duration, validation time, WAL, temporary
  disk, durable disk, and rollback duration; and
- autovacuum/bloat and maintenance limits; and
- predeclared capacity, lock-wait, WAL/archive, disk, heartbeat/stall, and error
  pause/abort thresholds, including the reserve that must remain after a pause.

Until those numbers are approved, performance gates are expressed as:

| Metric | Required decision rule |
|---|---|
| Median latency regression | Upper 95% confidence bound of V2/V1 is at or below the approved class ratio. |
| P95 latency regression | Upper 95% confidence bound of V2/V1 is at or below the approved tail ratio. |
| Absolute latency/planning | Upper confidence bound is at or below the approved SLO. |
| Throughput | Lower confidence bound and V2/V1 ratio are at or above the approved floor. |
| Lock/WAL/disk/relations | Maximum observed plus approved safety margin fits the operational budget. |
| Correctness and pruning | Zero-tolerance exact invariant; no confidence interval permits a failure. |

The local 80% fixed-floor reduction and 10% translation checkpoints in earlier
work packages are engineering triage goals only. They cannot authorize
activation.

### WP7.2 Run independent confirmation

For every PostgreSQL major and required topology:

- use at least five independently provisioned/reloaded rounds;
- retain cold-new-backend observations separately;
- collect at least 30 warm observations per case/layout/round after fixed
  warmup;
- counterbalance V1/V2 order;
- fix physical-session, pool-size, query-exec-mode, plan-cache, analyze, batch,
  concurrency, timeout, and seed protocols;
- force custom and generic plans explicitly;
- retain raw samples and compute seeded 95% bootstrap intervals over matched
  round medians, with an explicitly documented p95 interval method;
- keep typed, multi-kind, and untyped classes separate; and
- investigate an outlier or error in an isolated rerun without deleting it from
  the confirmatory population.

### WP7.3 Hard evidence gates

- PostgreSQL 16 and 18 are both represented and their exact server versions are
  asserted.
- Every timed case has passing preflight/postflight logical checksums and exact
  result validation.
- There are zero row, path, duplicate, endpoint, graph-isolation, or leaf-set
  mismatches.
- There are zero unexplained errors, stale-acceptance operations, duplicate
  identities, orphan claims, or unexpected deadlocks.
- Every environment and workload field is compatible except an explicitly
  approved layout/adapter field.
- All raw samples, plans, SQL, phase timings, environment facts, source/binary/
  fixture hashes, and generated reports are retained and checksummed.
- Every approved absolute and regression budget passes independently on both
  PostgreSQL majors. A geometric mean cannot hide an individual hard-budget
  failure.

## WP8: CI, documentation, and traceability handoff

After the exact candidate passes WP7:

- Add PostgreSQL 16/18 transition-performance jobs that select the intended
  `scripts/test-postgres` profile and assert `server_version_num`.
- Keep the full capacity matrix scheduled/manual if it is too expensive for
  every pull request, but run a small deterministic guard/pruning regression
  subset in ordinary CI.
- Add thresholds only after ADR 0012 approval, with a documented owner and
  update process. Never silently widen a limit in the same change that regresses
  it.
- Update `README.md`, GraphBench and benchmark documentation, the migration
  runbook, ADR 0012, and the G23 traceability row together.
- Retain the promoted source archive, build metadata, binaries, case/fixture
  manifests, PostgreSQL settings, raw samples, plans, reports, and SHA-256
  manifest under the release evidence policy.
- Run formatter and focused unit/race tests after every implementation change.
  Run the relevant PostgreSQL physical/migration suites through temporary
  script-owned instances. For parser/translator/rendering changes, add mutation
  and template coverage and regenerate fixture-backed artifacts through the
  repository workflow.
- Before activation review, run the full repository validation required by the
  parent plan against the exact promoted candidate and separately supplied
  backend connections. Do not use a benchmark database as the semantic-test
  database.

G23 may move from pending only when the retained PostgreSQL 16/18 topology
matrix, approved ADR 0012 budgets, and exact-candidate pass are linked from
`docs/edge_kind_partitioning_traceability.md`. Activation remains a separate
decision that also depends on all other parent-plan gates.

## Implementation tranches and review boundaries

Keep changes attributable and reversible by landing them in this order:

1. **Harness/prerequisite tranche:** real `benchmark_transition`, supported
   accepted-target schema assertion, temporary-table privilege decision,
   canonical analyze, and versioned adapter mapping.
2. **Measurement tranche:** phase timers, statement counts, microbenchmarks,
   untyped-count and typed-hydration cases, and structured direct-API EXPLAIN.
3. **Guard tranche:** centralized guarded acquisition and one-statement accepted
   fast path, with no structural cache.
4. **Guard follow-up, only if needed:** connection-stable fact cache or an
   authoritative database generation contract, each with mutation tests.
5. **Translation tranche:** explicit/typed graph relation scoping, then an
   optional bounded compiled-query cache if profiles still justify it.
6. **Typed-path tranche:** carry kind facts through hydration and prove exact
   leaf unions.
7. **Untyped-count tranche:** endpoint-preserving SQL experiments; a registry
   schema change is a separate reviewed decision, not part of a query-only PR.
8. **Topology/index tranche:** scaled matrix and any evidence-backed physical
   index change.
9. **Write/migration tranche:** provisioning fast path, registry contention,
   durability, migration, and capacity evidence.
10. **Confirmation tranche:** freeze budgets, run PG16/18 confirmation, update
    ADR/docs/traceability, and present the activation review packet.

After each tranche, rerun the WP1 microbenchmarks and the Tier A live suites on
fresh temporary PostgreSQL instances. Retain a delta report against both the
original V2 baseline and the immediately previous tranche. If a tranche mixes
multiple optimizations, use an ablation build or split it before accepting its
performance claim.

## Risk register

| Risk | Consequence | Required mitigation |
|---|---|---|
| Guard caching misses external drift | Security/correctness bypass | One bounded exact statement first; cache only with complete cross-process invalidation proof and next-operation mutation tests. |
| Fast guard changes typed errors | Operators lose reliable recovery guidance | Exhaustive classifier remains diagnostic fallback; assert every `errors.Is`/code mapping. |
| Guard and operation are not atomic | State can change after validation | Preserve same-connection ordering, measure current race, and review transaction/fusion semantics before optimizing round trips further. |
| Query cache reuses graph/kind-specific SQL | Cross-graph reads or stale routing | Include graph and kind generation in immutable keys; invalidate on restore/schema/unknown commit; add two-graph tests. |
| Typed hydration filter is incomplete | Missing paths or edges | Derive exact union from all segments and preserve an untyped fallback; mutation/template and backend-equivalence tests. |
| Endpoint joins are removed from counts | Dangling relationships are counted | Preserve the existing dangling-endpoint contract; require equivalent predicates or exact registry facts. |
| Registry count acceleration adds hot writes | Lower read latency but worse ingest/contention | Treat as schema change and include write/WAL/lock/migration/storage budgets in the same decision. |
| Tiny fixtures drive index selection | Good smoke numbers, poor scale behavior | No index change before required topology/skew/row matrix evidence. |
| Fast temporary PostgreSQL profile is treated as durable | Invalid write/migration budgets | Label profiles in envelopes and use durability-on settings for operational evidence. |
| Benchmark-only `TEMPORARY` becomes an implicit target privilege | The locked role/threat model changes without review | Prefer removing/encapsulating the dependency; otherwise update the ADR, ACL matrix, classifier, rollback, runbook, and hostile `pg_temp` tests together. |
| Retained V1 storage is mistaken for post-acceptance rollback | An unsupported recovery path may destroy accepted state | State explicitly that in-place rollback ends at acceptance; retain V1 through soak/cleanup for evidence while using forward fix or restore/rebind for recovery. |
| Benchmark seam ships accidentally | Activation interlock bypass | Non-shipping build boundary, binary inspection/test, checksummed delta, exact promoted-artifact rerun. |
| Dirty V2 source cannot be reproduced | Evidence cannot support release | Immutable source archive including untracked files, hashes, binaries, and build metadata for every checkpoint. |
| Global geomean hides a severe case | A critical operation remains unusable | Apply class and per-case hard budgets in addition to summary statistics. |

## Completion outcomes

The work is `evidence_complete` when the supported harness and instrumentation
exist; all required PostgreSQL 16/18 read, write, topology, provisioning,
registry, storage, maintenance, migration, rollback, restore, and capacity runs
have completed or produced a documented hard failure; the budgets were frozen
before confirmation; and immutable raw and summarized evidence records the
result. Evidence completion does not imply that the candidate passed.

### `evidence_complete_pass`

The performance work is eligible for the parent activation review only when all
of the following are also true:

- `benchmark_transition` is the supported V1/V2 live workflow and uses
  `scripts/test-postgres` for owned setup and teardown.
- Accepted V2 benchmarks run without ad hoc schema, privilege, graph, analyze,
  or activation patches; any pre-activation rehearsal seam is formally
  non-shipping and the promoted artifact is rerun without it.
- The accepted warm runtime hot path uses one topology-bounded exact guard
  statement, or a separately approved stronger design, and every required
  next-operation drift test fails closed.
- The fixed client, guard, PostgreSQL plan, execution, scan, and commit phases
  are individually measured.
- Typed node/edge plans remain healthy; typed path hydration uses the exact
  known leaf union; untyped work receives no unsound pruning.
- Untyped relationship count preserves endpoint semantics and meets the
  approved topology budget with all read/write tradeoffs recorded.
- Every required matrix and predeclared pause/abort threshold passes on
  PostgreSQL 16 and 18.
- ADR 0012 contains approved numeric budgets derived before the confirmatory
  run, and every per-class/per-case gate passes independently on both majors.
- Correctness remains exact across command benchmarks, GraphBench, mutation and
  template cases, PostgreSQL physical tests, migration/rollback/restore tests,
  and backend-equivalent shared integration cases.
- Raw and summarized evidence is immutable, checksummed, reproducible, and
  linked from G23.

### `evidence_complete_fail`

If any required safety, correctness, reproducibility, topology, capacity, or
numeric performance gate fails after the prescribed work and confirmation, the
result is a valid `evidence_complete_fail`. Retain the failed samples and root-
cause report, leave ADR/G23 blockers explicit, and keep production activation
closed. Do not convert a fail to a pass by deleting outliers, narrowing the
required matrix, weakening a security invariant, or widening a budget after
seeing the confirmatory result.

The production activation decision remains outside this document even after an
`evidence_complete_pass`; it still depends on every other parent-plan gate.
