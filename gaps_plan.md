# Edge Kind Partitioning Gap Closure Plan

> ADR 0014 supersedes per-operation and next-operation guard requirements in
> this plan. Managed layout changes now require drain, restart, and reassertion.

## Status and authority

This document sequences the remediation required after review of the current
edge-kind partitioning changeset. It is a companion to
`edge_kind_partitioning_plan.md`, not a replacement for it. The locked
contracts, work-package requirements, and final definition of done in that
document remain authoritative.

The current implementation is unreleased transition scaffolding. A passing
semantic integration suite or a successful disposable-database migration does
not authorize activation. No phase below may be skipped merely because the
current happy path works.

This plan has four objectives:

1. Close every correctness and isolation defect found in review.
2. Replace the current migration prototype with a durable, recoverable, and
   independently fenced migration protocol.
3. Produce PostgreSQL 16 and 18 evidence for runtime, physical, migration,
   recovery, role, and performance contracts.
4. Preserve the distinction between code completion, fleet transition, V1
   runtime retirement, and late-upgrade retirement.

## Immediate containment rules

These rules apply until the activation gate in Phase 10 passes:

1. Do not ship the current V2 bootstrap or migrator for production use.
2. Do not let fresh bootstrap create an application-usable
   `v2_accepted` state.
3. Disable or refuse `accept` and `cleanup` unless the durable identity,
   receipt, confirmation, role-handoff, and smoke protocols described below
   are present.
4. Run migration development only against identity-guarded disposable
   databases.
5. Preserve the current live failures as regression tests before repairing
   them:
   - Relationship identity-property upsert has no valid V2 conflict arbiter.
   - String values containing `from node ` or similar text are rewritten.
   - Graph-unsafe path helpers return rows from more than one graph when IDs
     overlap.
   - Deleting a managed graph catalog row leaves physical partition drift.
6. Do not describe the transition as complete in README or release material
   until the applicable completion gate has actually passed.

## Gap register

Every implementation item and exit gate below uses these identifiers.

| ID | Gap | Primary risk | Closed by |
|---|---|---|---|
| G01 | Fresh bootstrap and migration acceptance trust a marker instead of target identity, handoff, and smoke | Wrong-role or invalid-layout runtime becomes operational | Phases 2, 5, 7 |
| G02 | Runtime guarding is absent or inconsistent across constructors, pool sessions, promoted manager methods, maintenance, deletes, and Raw | State or role gate bypass | Phases 2, 5 |
| G03 | Classifier and health do not verify physical topology, registries, triggers, indexes, ownership, or ACLs | Drift is reported as healthy | Phase 2 |
| G04 | Translator regex/reflection pass rewrites ordinary literal and parameter strings | User data corruption | Phase 3 |
| G05 | Path, endpoint, swap, and fallback helpers are not uniformly graph-aware | Cross-graph reads and mutations | Phase 3 |
| G06 | Managed SQL and names are not uniformly qualified, quoted, bounded, or canonicalized | Search-path redirection, invalid identifiers, schema ambiguity | Phases 1, 3 |
| G07 | Graph IDs are not constrained to positive int32 and managed graph rows are deletable | Scan overflow and orphaned physical topology | Phases 3, 4 |
| G08 | Graph, kind, default-graph, and leaf caches are published before transaction commit | Rolled-back objects survive in memory | Phase 4 |
| G09 | Relationship identity-property upsert still uses an expression `ON CONFLICT` with no V2 arbiter | Valid batch operations fail | Phase 4 |
| G10 | Edge property uniqueness is not fully registry-backed, migrated, collated, reconciled, or removable | Duplicate acceptance or constraints that never deactivate | Phases 1, 4, 7 |
| G11 | Edge identity, routing immutability, explicit-ID behavior, claim lock ordering, and all write paths lack complete proof | Duplicate IDs, deadlocks, or routing corruption | Phases 1, 4 |
| G12 | Migration has no durable, independently verified writer fence | Concurrent V1 writes invalidate copy and rollback | Phase 6 |
| G13 | Preflight is not a coherent catalog/data/role/capacity/backup snapshot and its token is reusable | Migration starts from stale or wrong facts | Phase 6 |
| G14 | Copy runs in one transaction; `resume` is a new migration; checkpoints and ambiguous-commit recovery are absent | Unrecoverable interruption or duplicate work | Phases 6, 7 |
| G15 | Validation is count-only and acceptance performs no final revalidation | Corrupted or structurally invalid V2 can be accepted | Phase 7 |
| G16 | Operator, owner, target, old writer, and rollback-preparation identities and grants are not separated | Overprivileged runtime and unsafe handoff | Phases 1, 5, 6, 7 |
| G17 | Rollback does not reconstruct the frozen V1 contract or retain auditable fenced handoff state | False V1 recovery and data/metadata loss | Phase 8 |
| G18 | Cleanup drops by name without OID/map, smoke, soak, backup, fleet, or restore/rebind gates | Destructive drop of the wrong or still-needed object | Phase 8 |
| G19 | Confirmation, receipt, report regeneration, redaction, and idempotent retry protocols are incomplete | Replay, wrong-database action, or ambiguous result | Phase 6 |
| G20 | GraphBench, benchmark, plan corpus, Retriever, export, and dawgrun do not uniformly gate readiness or reject mutation | Tool-triggered schema/data mutation | Phase 9 |
| G21 | Frozen V1 fixtures, engine/CLI/physical tests, role-aware CI, failure injection, and required Make targets are absent | No reproducible safety evidence | Phases 1, 2, 9 |
| G22 | Temporary PostgreSQL harness can leak partial clusters or destroy a pre-existing named instance | Test resource loss and unreliable parallelism | Phases 0-2 |
| G23 | Structured pruning, performance budgets, reproducible artifact, documentation, and runbooks are incomplete | Unbounded production cost and unusable recovery process | Phase 9 |
| G24 | Fleet ledger, rollout evidence, V1 EOL, late-upgrade horizon, and immutable archive are absent | Premature declaration of completion | Phases 10-12 |

## Dependency sequence

The execution order is intentionally strict:

```text
Phase 0  pin V1 source, contain activation, safe launcher, red regressions
   |
Phase 1  frozen contracts, V1 evidence, ADRs, prototypes
   |
Phase 2  test infrastructure, catalog facts, classifier,
         common layout/identity/receipt foundation
   |
Phase 3  canonical SQL, structural translation, graph helpers
   |
Phase 4  topology, caches, identity, constraints, write-path integration
   |
Phase 5  identity-bound runtime gate and bootstrap handoff
             |
Phase 6  durable migration control plane, preflight, writer fence
             |
Phase 7  resumable copy, exhaustive validation, acceptance, smoke
             |
Phase 8  rollback, V1 handoff, restore/rebind, cleanup
             |
Phase 9  operational tools, full CI, artifacts, performance, docs
             |
Phase 10 activation release and rehearsal
             |
Phase 11 fleet transition and V1 runtime retirement
             |
Phase 12 late-upgrade retirement and immutable archive
```

Phase 4 depends on Phase 3 canonical SQL and structural graph-scoping work.
Migration data movement must not begin before the Phase 4 enforcement model and
Phase 6 writer fence are both proven.

## Cross-cutting implementation rules

1. Add the failing test before each behavioral fix.
2. Use typed `pg_catalog` facts. Do not infer topology or constraints by
   regex-parsing `pg_indexes` output or by relation name alone.
3. Treat object identity as namespace plus OID plus relkind plus expected
   structural facts while operating on a live database. Persist an
   OID-independent fingerprint for restore/rebind.
4. Qualify managed objects with `public` and built-ins with `pg_catalog`.
   Identifier components must use trusted identifier formatting; property
   names remain data, not SQL fragments.
5. Runtime cache changes are transaction-local until commit succeeds. An
   unknown commit outcome forces refetch rather than optimistic publication.
6. Every destructive command is action-specific, state-bound,
   database-identity-bound, nonce-bearing, expiring, and durably receipted.
7. A file report is a projection of durable database facts. Failure to write
   the file never causes database mutation to repeat.
8. PostgreSQL physical, role, migration, failpoint, and recovery tests use
   randomized disposable databases and roles created from an admin coordinator.
   They never target the ordinary application `CONNECTION_STRING`.
9. All new state transitions and enforcement behavior must pass on PostgreSQL
   16 and 18 before activation.
10. No application pool or operational tool may use an owner/operator
    connection after handoff.

## Phase 0: Contain activation and capture failures

### Dependencies

None.

### Work

1. Before changing bootstrap behavior, identify and record the authoritative
   reviewed V1 source revision. Copy its raw schema/source archive to a
   protected staging location and record its SHA-256 so Phase 1 cannot
   accidentally freeze a containment-modified schema.
2. Introduce a temporary activation interlock so the current self-accepted
   bootstrap shape cannot be treated as production-ready.
3. Refuse fresh initialization with a stable activation-not-ready error until
   Phase 5 implements the final pending-handoff state. Phase 0 must not persist
   a provisional layout shape that later phases would have to reinterpret.
4. Make current migration `accept` and `cleanup` fail closed until the durable
   protocols in Phases 6-8 are available.
5. Correct README language to identify the implementation as an unreleased
   transition.
6. Create `docs/edge_kind_partitioning_traceability.md` and seed it with
   G01-G24, the locked contracts they affect, and placeholders for code, tests,
   reports, and retained evidence.
7. Before running any live red test, make the launcher minimally safe:
   - Cleanup traps precede filesystem/server mutation.
   - Invocation ownership is nonce-guarded.
   - Existing named instances are preserved.
   - Explicit PG16/PG18 binary selection is supported.
   - Each run uses a randomized database plus the exact owner/operator/target/
     writer role subset needed by the test and asserts the actual
     `server_version_num`.
8. Convert the four live failures listed under immediate containment into an
   explicit red-test characterization target that first proves each defect.
   Move each unchanged assertion into the default green suite with its fix; do
   not check in a failing default suite or invert an assertion.

### Exit gate

- No supported path can create an application-usable partial V2.
- The authoritative pre-containment V1 source revision and checksum are
  recorded.
- Live repros run only through an invocation-owned failure-safe launcher.
- Each known live correctness failure has a deterministic test.
- Traceability accounts for G01-G24.

## Phase 1: Freeze contracts, V1 evidence, and prototypes

### Dependencies

Phase 0.

### Work

Phase 1 is internally ordered: approve the foundational ADRs, freeze the V1
source, make the disposable launcher failure-safe, characterize/prototype the
contracts, and then freeze the evidence schemas and baselines.

1. Add numbered ADRs under `docs/adr/` for:
   - V2-only runtime lineage and V1 retirement.
   - Layout state machine, legal transitions, and authoritative pointers.
   - Database incarnation and identity epoch.
   - Owner, operator, target, old-writer, and rollback-preparation role model.
   - Writer fence and credential/provider retirement.
   - Graph-local edge identity and immutable routing columns.
   - Graph-wide edge property uniqueness, collation, and upsert semantics.
   - Canonical namespace, SECURITY DEFINER policy, default ACLs, and hostile
     search paths.
   - Canonical application-schema normalization and duplicate/conflict errors.
   - Supported PostgreSQL majors, including the PostgreSQL 17 disposition.
   - Topology, lock, WAL, disk, planning, registry-contention, and performance
     budgets.
   - Rollback cutoff, restore/rebind, cleanup, fleet, and late-upgrade policy.
2. Freeze the exact authoritative V1 artifacts pinned in Phase 0 under
   `drivers/pg/migrate/testdata/`:
   - `v1_schema.sql`.
   - Deterministic seed data.
   - Expected catalog, owner, ACL, default-ACL, sequence, index, trigger, and
     dependency facts.
   - The actual version-pinned V1-compatible client binary/source archive and
     module metadata used by rollback and compatibility tests, with checksums.
   - Real SHA-256 values in the manifest; remove the zero placeholder.
3. Before any live prototype, verify and complete the minimum disposable
   PostgreSQL lifecycle begun in Phase 0:
   - Install cleanup traps before initialization or server start.
   - Track whether this invocation created the instance.
   - Refuse to destroy a pre-existing named instance.
   - Clean partial initialization/start/database-creation failures and signals.
   - Validate deletion targets against an invocation-owned nonce/state file.
   - Select and verify the requested PostgreSQL binary/version explicitly.
   Phase 2 completes the role-aware, parallel, multi-version harness and its
   full test suite.
4. Characterize V1 behavior for:
   - Edge ID uniqueness and explicit IDs.
   - Endpoint identity and zero/one/multiple identity-property update shapes.
   - Property uniqueness across kinds.
   - Missing, JSON null, empty, scalar, object, array, oversized, quoted, and
     collated property values.
   - Constraint add, backfill, conflict, remove, and re-add.
   - Kind mutation, endpoint mutation, ID-only updates/deletes, cascade, wipe,
     helpers, hydration, and colliding IDs across graphs.
5. Prototype on PostgreSQL 16 and 18:
   - Identity and property registries under single-row, multi-row,
     direct-leaf, root, COPY, rollback, and reversed lock order.
   - Deterministic statement-level sorted claiming and stable retry/SQLSTATE
     mapping.
   - Root-key conversion while an identity authority remains continuously
     effective.
   - Direct partition creation versus staged `ATTACH PARTITION`.
   - Graph-safe cascade and reset designs.
   - Restricted runtime provisioning and future-object default ACL behavior.
6. Define versioned JSON schemas and canonical serialization for:
   - Application manifests and exclusions.
   - Connection and current-identity envelopes.
   - Capacity, backup, drain, writer-probe, retirement, external-writer,
     V1-handoff, cleanup, and restore evidence.
   - Preflight, progress, validation, acceptance, smoke, rollback, handoff,
     rebind, cleanup, health, and fleet records.
   - Confirmations, receipts, checkpoints, stable error codes, and exit codes.
7. Require strict parsing: one JSON value followed by EOF, valid UTF-8,
   supported schema version, valid digests/times, no duplicate keys or logical
   entries, graph/exclusion disjointness, canonical sort order, and no secrets
   in reports or digests.
8. Capture reproducible V1 semantic, GraphBench, plan, relation-size, catalog,
   migration-cost, and write-throughput baselines with published fixtures and
   seeds.

### Tests and evidence

- Frozen checksum verification.
- PostgreSQL 16/18 V1 characterization.
- Registry concurrency and rollback tests.
- Hostile search-path and privilege prototype results.
- Approved topology and performance budgets.
- Catalog snapshots containing owners, ACLs, comments, sequences, dependencies,
  and unsupported-object examples.

### Exit gate

- Frozen V1 semantics and every selected database mechanism have executable
  characterizations, and later evidence formats have executable validators.
- The launcher used by prototypes cannot leak or remove an instance it did not
  create.
- The exact V1 fixture is reproducible and checksummed.
- Identity, property, privilege, cascade, and reset mechanisms are selected
  based on PostgreSQL 16/18 evidence.
- No placeholder digest or undecided destructive protocol remains.

## Phase 2: Test infrastructure, catalog facts, common state, and fail-closed classification

### Dependencies

Phase 1 schemas and role model.

### Work

1. Complete and test the `scripts/test-postgres` hardening begun in Phase 1:
   - Install cleanup traps before the first filesystem or server mutation.
   - Track whether the invocation created the instance.
   - Refuse or preserve an already-running named instance.
   - Clean partial init, failed start, failed database creation, timeout, INT,
     TERM, and child-command failure.
   - Preserve the child exit status and signal.
   - Store a mode-0600 state file with a nonce, data directory, port, server
     version, and ownership facts.
   - Validate every deletion target against that state.
   - Support an explicit PostgreSQL binary directory for PG16/PG18 selection.
   - Resolve and validate a canonical non-symlink runtime root.
   - Lock each instance name across concurrent up/down/run operations.
   - Verify the selected port after server start and retry authoritative
     start-time collisions safely.
   - URL-encode configurable connection components and emit shell-safe `env`
     output.
2. Add a role-aware disposable database harness:
   - Random database and owner, operator, target, old-writer, and
     `NOLOGIN PASSWORD NULL` preparation roles.
   - Exact-resource cleanup guarded by recorded identity.
   - A separate independent cluster for restore/rebind tests.
3. Make `drivers/pg/internal/schema` the canonical owner of V2 and migration
   control DDL. Runtime and migrator import it; `drivers/pg/query` must not own
   a second schema definition.
4. Expand `drivers/pg/internal/catalog` into typed facts for:
   - Relations, OIDs, namespaces, relkind, ownership, comments, storage, and
     tablespaces.
   - Partition keys, parents, bounds, DEFAULT presence, duplicate bounds, and
     indexes.
   - Constraints, normalized expressions, triggers, routines, sequences,
     dependencies, RLS, rules, publications, and replica identity.
   - ACLs, default ACLs, memberships, database/role settings, and PUBLIC.
5. Replace the current layout classifier with exact recognition of:
   - Uninitialized.
   - Frozen recognized V1.
   - Each legal migration/fence/rollback/handoff state.
   - Pending bootstrap handoff/smoke.
   - Structurally accepted V2 only when marker, hierarchy, registries, triggers,
     indexes, ACLs, recorded target identity/epoch, and recorded smoke agree.
     This static classification is not runtime readiness; Phase 5 compares the
     same-session current identity with these recorded facts.
   - Repairable drift, hard drift, and unknown layouts.
6. Produce stable topology fingerprints that omit restore-variant OIDs but bind
   all structural facts.
7. Make health compare logical registries with physical graph parents/leaves
   and report explicit drift rather than registry row counts alone.
8. Implement the common durable control foundation required before bootstrap:
   - Final `dawgs_schema_layout` fields and legal bootstrap/migration pointers.
   - Database identity/incarnation and identity epoch.
   - Operation scopes, action nonces, and append-only receipt storage.
   - Canonical confirmation consumption and report-from-receipt primitives.
   - Bootstrap handoff/smoke attempt state and stable error codes.
   Migration-specific journals and evidence maps remain Phase 6 work.
9. Implement a `CurrentIdentityProvider` primitive that returns database and
   directly authenticated session/current-role facts without deciding
   readiness. Phase 5 integrates it with the static classifier on the same
   physical connection.
10. Establish PG16/PG18 CI jobs now and assert `server_version_num` in each.
11. Scaffold the test surfaces required by subsequent phases:
    - `prepare_test_pg` and the role-aware coordinator.
    - `pg_physical` build tag and `test_pg_physical` target.
    - `migration_integration` build tag and `test_migration` target.
    - Built-binary smoke entry point.
    These targets may initially contain only harness/self-tests, but each later
    phase adds its cases before claiming its own exit gate.

### Tests and evidence

- Unit fixtures for every catalog decoder and layout state.
- Marker-only V2, heap lookalikes, wrong parent/bound, DEFAULT leaf, missing
  trigger/index/registry, extra leaf, owner/ACL drift, and same-name replacement.
- Temporary harness parallelism, port collision, existing-instance, failure,
  signal, stale-state, symlink/root validation, same-name lock, URL/env output,
  and exact-cleanup tests.
- Classifier queries run in a bounded read-only envelope and cause no mutation.

### Exit gate

- No marker alone can make a database structurally eligible or operational.
- Physical and logical topology disagreements are visible and fail closed.
- Disposable tests remove every invocation-owned database, role, server, and
  data directory while preserving pre-existing and parallel resources
  byte-for-byte.
- Bootstrap has one final common layout/operation/receipt format before its
  runtime implementation begins.
- PG16 and PG18 are independently identified in CI.

## Phase 3: Canonical SQL, translator correctness, and graph isolation

### Dependencies

Phase 2 canonical schema and typed catalog facts.

### Work

1. Delete the regex/reflection rewrite of arbitrary literals and parameters.
   Persistent node/edge relations must be supplied structurally by the AST
   constructors that own each table reference or SQL-valued harness fragment.
2. Keep ordinary strings byte-for-byte unchanged, including values that contain
   `from node`, `join edge`, `update node`, or `delete from edge`.
3. Add graph ID to every helper that may resolve an ID:
   - `nodes_to_path`.
   - `edges_to_path`.
   - `ordered_edges_to_path`.
   - Endpoint, swap, cascade, and fallback helpers selected by the ADR.
4. Update all translator calls, direct statements, batch paths, and hydration
   code to supply the graph ID and graph-scoped parents.
   - Correct direct relationship property updates to bind
     `query.Relationship()`, never `query.Node()`.
5. Carry static edge kind through aliases, projections, multipart queries, and
   optimizer lowerings. Edge update/delete must include kind equality and a
   visible target predicate.
6. Qualify managed relations, types, routines, and sequences as `public.*` and
   built-ins/operators as `pg_catalog.*`. Prove hostile `search_path` cannot
   redirect DDL, DML, type loading, helper calls, or sequence allocation.
7. Replace raw field-derived index/constraint identifiers with deterministic
   bounded hashes plus trusted identifier rendering. Preserve logical names as
   data in the registry.
8. Canonicalize application schema before DDL:
   - Normalize default/full graph definitions.
   - Merge identical duplicates.
   - Reject conflicting duplicates deterministically before mutation.
   - Sort graphs, kinds, indexes, and constraints canonically.
9. Constrain graph IDs to positive int32, set sequence bounds, and test
   exhaustion. Preserve every catalog-backed signed int16 kind allowed by the
   locked contract.
10. Give table-reading helpers correct `STABLE`/`VOLATILE` classification.
    They must not be declared `IMMUTABLE` or permit stale constant folding.

### Tests and evidence

- Literal and parameter round-trip tests for SQL-looking strings.
- Translation tests for reads, joins, nested queries, fast paths, updates,
  deletes, multipart queries, helpers, and hydration at graph IDs 0 and nonzero.
- Two graphs with colliding node and edge IDs; every helper returns only the
  selected graph.
- Direct relationship property update proves the relationship binding is
  changed while the similarly named node binding is untouched.
- Prepared helper execution observes a row mutation between executions and
  never returns a constant-folded stale composite.
- Hostile search-path lookalikes for tables, types, functions, and built-ins.
- Long, quoted, Unicode, malicious, and colliding property/identifier tests.
- Duplicate schema definitions cause no partial DDL.
- Minimum int16, -1, 0, and maximum int16 kinds have canonical quoted leaf-name
  formatting and parsing; Phase 4 proves physical routing and pruning.

### Exit gate

- No user literal or parameter is modified by graph scoping.
- AST-generated relation references and the graph-aware helpers implemented in
  this phase cannot cross graphs. Phase 4 closes identity-backed ID-only and
  cross-leaf mutation/hydration paths.
- All generated executable SQL uses canonical qualified managed objects.
- Schema normalization completes before any catalog mutation.

## Phase 4: Topology lifecycle, commit-safe caches, and registry enforcement

### Dependencies

Phases 2 and 3; Phase 1 selected registry and lock protocols.

### Work

1. Implement exact graph-parent and graph/kind-leaf lifecycle:
   - One graph parent partitioned by kind.
   - No DEFAULT leaf.
   - Typed validation of parent, bound, owner, ACL, indexes, and triggers.
   - Canonical-name collision with the wrong object is drift, never success.
   - Additive declared/stored/dynamic/migrated/retained provenance.
   - A narrowly owned `SECURITY DEFINER` provisioning routine with a fixed safe
     search path, owner/OID validation, exact EXECUTE grant, and current/future
     leaf ACL propagation. The target may provision approved kinds while
     lacking `CREATE` on `public` and lacking direct registry DML.
2. Establish one deterministic lock hierarchy for schema assertion,
   graph/kind provisioning, constraint lifecycle, reset, and migration.
3. Stage kind, graph, default-graph, and leaf cache deltas on the transaction.
   Publish only after confirmed commit; discard after rollback/close. Refetch
   after unknown commit outcome.
4. Add a managed graph-catalog deletion guard and remove target-role DELETE
   privilege. Owner/operator bypass must be detected as drift.
5. Implement graph-wide edge identity enforcement:
   - Uniqueness of `(graph_id, edge_id)` across all kind leaves.
   - Immutable `id`, `graph_id`, and `kind_id`.
   - Root, graph parent, direct leaf, COPY, function, and future-leaf coverage.
   - Deterministic statement-level claim order and stable conflict mapping.
   - ID-only update/delete/hydration scoped through graph identity when kind is
     not statically available.
   - Hydration preserves input ordinality and duplicates.
   - Explicit relationship IDs preserved in direct and batch insert paths.
   - Relationship ID/graph/kind routing mutation rejected explicitly rather
     than ignored.
6. Complete logical edge property constraint lifecycle:
   - Registry-backed discovery rather than physical-index regex.
   - Create as pending, lock, backfill, validate, then publish active.
   - Explicit compatible collation and extraction semantics.
   - Validate all existing registry semantics before reactivation; a mismatched
     conflict update must be an error, not a zero-row success.
   - Execute lock, backfill, validation, and publication as separate
     parameterized statements in one transaction; never send a multi-command
     prepared statement through ordinary Raw execution.
   - Remove by deactivate, drain/delete claims, and tombstone/history.
   - Re-add and reconcile after failure or reconnect.
7. Replace expression `ON CONFLICT` relationship upsert with the selected
   registry-aware routine:
   - Zero identity fields preserve endpoint/kind identity behavior.
   - One field preserves V1 property identity behavior across kinds.
   - Unsupported multi-field shapes fail before mutation unless Phase 1
     explicitly approves and characterizes them.
8. Make constraint migration metadata consumable by Phase 7.
9. Add one metadata-preserving `executeTranslated` application path:
   - Sort and deduplicate `RequiredEdgeKindIDs`.
   - Provision every required leaf before sending mutation SQL.
   - Preserve `GraphID`, mutation state, required kinds, and optimization
     metadata through `Transaction.Query` and live-query adapters.
   - Mapper/provisioning failure sends no application statement.
   - Batch failure leaves buffers retryable.
10. Implement graph-safe reset through a narrowly owned routine that clears
   nodes, edges, identity claims, and property claims while retaining graph,
   leaves, constraints, and provenance. Same-transaction retain callbacks may
   add kinds safely.
11. Make vacuum, analyze, and logical-size helpers understand the nested
    partition hierarchy.

### Tests and evidence

- Concurrent same/different-kind provisioning and rollback.
- Cache rollback, failed DDL, failed commit, reconnect, and graph-switch tests.
- Same edge ID across leaves conflicts; same ID across graphs is allowed.
- Routing-key updates fail on root and direct leaves.
- Explicit-ID and immutable-kind behavior matches across direct, batch, and
  Cypher paths.
- Hydrating `[idA,idB,idA]` returns A, B, A from the selected graph even when
  IDs are interleaved across kind leaves.
- Minimum int16, -1, 0, and maximum int16 kinds route to and prune their exact
  physical leaves on PG16/18.
- Property conflicts across leaves for direct, batch, Cypher, raw, and COPY.
- Missing, JSON-null, empty, scalar, collation, oversized, drop, and re-add.
- Constraint add over existing duplicates; concurrent raw INSERT/UPDATE during
  add/drop; semantic/collation mismatch; absent-claim concurrent upsert;
  property A-to-B update and rollback; ordinary delete and node-cascade claim
  cleanup.
- Zero-field endpoint upsert and same-endpoint/different-kind separation.
- Reversed multi-row claim order has no unbounded deadlock behavior.
- Wipe/reset with three or more leaves leaves no claims or orphan rows.
- A retain callback introducing an unknown kind neither self-deadlocks nor
  opens a nested external transaction. Retain failure rolls back truncation,
  retained data, kind row/cache, leaf registry/provenance, and physical leaf
  together.
- Multipart CREATE metadata provisions every dynamic leaf once; mapper or
  provisioning failure executes zero application SQL and a failed batch can be
  retried.
- Exact survivor tests cover direct UpdateRelationship with graph/kind, batch
  ID update/delete through identity, and typed Cypher update/delete after
  WITH/alias/multipart against colliding graph IDs. Visible kind predicates
  prune to the permitted leaves.
- Duplicate identity/property, retry/deadlock, immutable routing, and missing
  leaf errors have identical documented SQLSTATE/typed-driver classification
  across direct, batch, Cypher, raw root, raw leaf, and COPY paths.
- Target dynamic provisioning succeeds through only the approved routine;
  arbitrary DDL and direct registry DML are denied.

### Exit gate

- Catalog, registry, physical topology, and caches agree after commit,
  rollback, failure, concurrency, and reconnect.
- Identity and property uniqueness are database-enforced on every write path.
- The live identity-property upsert failure no longer reproduces on PG16/18.

## Phase 5: Identity-bound runtime gate and fresh bootstrap handoff

### Dependencies

Phases 2-4.

### Work

1. Use the final common layout, identity, operation-scope, confirmation, and
   receipt foundation from Phase 2, adding no bootstrap-only alternative. Bind:
   - Database identity/incarnation and identity epoch.
   - Current bootstrap or migration operation pointer.
   - Exact target role OID/name/session identity.
   - Handoff and smoke status plus current receipt digest.
   - Topology fingerprint and source revision.
2. Fresh operator bootstrap must:
   - Install canonical V2 and control objects.
   - Record pending target handoff/smoke.
   - Apply no target grants before a target has been directly authenticated.
   - Return nonoperational pending status without inferring a target from the
     operator pool.
3. Implement two-connection bootstrap continuation:
   - Operator control connection.
   - Directly authenticated non-owner target connection with
     `session_user = current_user`.
   - Operator consumes the handoff confirmation, records the exact target, and
     applies current/future target grants in the same durable handoff
     transaction.
   - Sentinel insert/read/tableoid/identity/property/delete smoke in one target
     transaction, or the approved empty-install fallback.
   - Operator atomically records the attempt and activates only a passed or
     explicitly approved fallback receipt. Fresh bootstrap records a canonical
     empty cleanup map and is never eligible for legacy cleanup.
4. Centralize the ADR 0014 service-start lifecycle gate. Apply its local
   operation admission and once-per-new-backend identity/type validation to:
   - Registered and direct constructors.
   - Pool establishment/reconnect and type registration.
   - Read/write transactions, BatchOperation, Raw, Run, maintenance, wipe,
     `Delete*ByKinds`, schema assertion, promoted manager methods, and external
     pools.
5. Require exact accepted topology, current database identity/epoch, exact
   target role, and current smoke receipt during startup assertion; ordinary
   application SQL requires the published local lifecycle state and current
   physical-backend identity mark.
6. Reject operator, owner, old-writer, preparation, inherited-role,
   startup-`SET ROLE`, and runtime role/session-authorization changes.
7. Load `public.*` composite types only after readiness succeeds on that same
   session. Preserve caller pool hooks and avoid connection churn.
8. Define typed, stable errors for uninitialized, V1 migration-required,
   migrating, pending, smoke-pending/failed, wrong role, restored/rebind
   required, drift, and unknown layout.

### Tests and evidence

- Enumerate every exported/promoted database-touching entry point and prove it
  invokes the common guard.
- Operator/wrong-role/preparation/old-writer/SET ROLE refusal.
- Reconnect, pool replacement, fingerprint change, and restore identity change.
- Pending, failed, passed, and fallback smoke.
- Bootstrap crash before/after DDL, grants, target smoke, and receipt commit.
- Rejected sessions issue only bounded classifier/identity SQL.
- Pool statistics and hook counters prove checked post-bootstrap connections
  are reused; qualified type registration does not cause release-time churn.

### Exit gate

- Only exact identity-bound, accepted, smoke-passed V2 can execute application
  SQL.
- No entry point bypasses the gate.
- Fresh bootstrap is idempotent and never self-accepts.

## Phase 6: Durable migration control plane, preflight, and writer fence

### Dependencies

Phases 1, 2, 4, and 5.

### Work

1. Add migration-specific control DDL to the final canonical common state from
   Phase 2, independently from full runtime V2 DDL:
   - Reuse `dawgs_schema_layout` with its already-locked authoritative
     pointers and identity/smoke facts; the migrator may install the control
     subset idempotently but may not define a divergent table.
   - `dawgs_schema_migration` with source fingerprints, evidence digests,
     inverse catalog/ACL/sequence snapshot, phase, fence, validation,
     rollback, handoff, and cleanup state.
   - `dawgs_schema_migration_graph` with strategy, source/staging/legacy object
     facts, high water, counts, per-kind counts, digests, and phase.
   - Append-only `dawgs_schema_receipt` and required object/writer/evidence
     maps.
2. Enforce legal transitions and mutually exclusive current pointers in the
   database. Rollback never drops the journal.
3. Extend the common Phase 2 confirmation/receipt primitives for every
   migration action:
   - Random nonce and expiry.
   - Action, database identity/epoch, operation scope, state, manifest,
     preflight, catalog, validation, cleanup-map, and supplied-evidence digests.
   - Same-transaction nonce consumption and receipt append.
   - Replay returns the durable prior result; changed facts invalidate it.
4. Make reports projections of durable facts. Implement `receipt` regeneration
   after file-write failure or ambiguous client outcome. File output is
   secret-free, mode 0600, fsync-aware, and atomically renamed.
5. Refactor the CLI around an injectable command runtime and implement the
   complete command surface:
   - `status`, `preflight`, `confirm`, `migrate`, `verify-fence`, `resume`,
     `validate`, `accept`, `smoke`, `rollback`,
     `complete-v1-handoff`, `rebind-restore`, `cleanup`, `receipt`, `health`,
     and `version`.
   - `status` and `health` expose authoritative phase, per-graph/chunk progress,
     high water, heartbeat, copied/total rows, rate/ETA where meaningful, fence
     state, target/smoke/receipt pointers, topology fingerprint, drift, and
     exact recovery guidance.
6. Replace preflight with one coherent, bounded, read-only inspection on a
   dedicated connection and layout session lock using the approved repeatable
   snapshot mode.
   Every later command reacquires and holds the same scoped session lock for
   its invocation; durable state and the database writer fence, not a
   cross-command advisory lock, bridge invocations.
7. Preflight must verify and digest:
   - PostgreSQL version and exact frozen V1 lineage.
   - Topology, data integrity, graph/kind rows, duplicate IDs, endpoints,
     constraints, indexes, sequences, triggers, routines, dependencies, owners,
     ACL/default ACL, settings, and unsupported objects.
   - Canonical manifest completeness; missing kinds are a V1 remediation error,
     never allocated during migration.
   - Capacity and recent backup/restore evidence.
   - Distinct operator, target, old-writer, and dormant preparation identities.
   - Complete writer paths, roles, memberships, sessions, COPY/function/
     sequence routes, replication/external controls, probes, and retirement
     plans.
8. Make `migrate` initially install only the durable writer fence:
   - Revalidate preflight/evidence under the session lock.
   - Journal `fence_installing` and the inverse object/ACL/default-ACL/sequence
     snapshot before changing access.
   - Revoke/guard database-local write paths and commit
     `fence_installed_unverified`.
   - Drain/terminate old sessions and directly authenticate negative probes for
     root, parent, leaf, COPY, update/delete, truncate/DDL, routines, and
     sequences.
   - Persist `fence_database_verified_retirement_pending`.
9. Make `verify-fence` prove exact credential/provider retirement, external
   writer closure, session drain, and unchanged authoritative source facts
   before transitioning to `fence_verified`.
10. No root-key change, registry backfill, partition detach, DDL cutover, or
    data copy may be reachable before `fence_verified`.
11. Populate the Phase 2 `test_migration` and built-binary scaffolds with
    command parsing, strict envelope, confirmation, receipt, report,
    preflight, fence, and failpoint cases before Phase 6 can pass.

### Tests and evidence

- Strict schema/parser, redaction, stable error/exit-code, and CLI tests.
- Concurrent activity cannot create a mixed preflight snapshot.
- Same manifest on another database cannot reuse a confirmation.
- Changed catalog, evidence, role, epoch, or state invalidates confirmation.
- Failure before/after each journal, ACL, probe, retirement, receipt, report,
  and COMMIT boundary.
- Direct-auth old-writer probes remain negative after fence verification.

### Exit gate

- Preflight is mutation-free, coherent, and sufficient to compute an inverse
  plan.
- Every mutator is idempotently receipted and recoverable after ambiguous
  COMMIT or report failure.
- No managed data or topology mutation occurs before an independently proven
  durable fence.

## Phase 7: Resumable preparation, graph cutover, validation, acceptance, smoke

### Dependencies

Phase 6 `fence_verified` and Phase 4 final enforcement design.

### Work

1. Implement a shared checkpoint protocol:
   - Stable migration and attempt IDs.
   - Idempotent phase transitions.
   - Per-graph and per-chunk high water.
   - Source/target counts, typed versioned SHA-256 digests, heartbeat,
     timestamps, rate, and recovery guidance.
   - Checkpoint and associated data changes commit together.
   - Data chunks use `(graph_id, id)` keyset bounds, never `OFFSET`; each has an
     idempotent target key, encoder version, and atomic DML/high-water/count/
     digest checkpoint.
2. Perform resumable global preparation:
   - Create empty identity/property registries without prematurely enabling an
     incompatible final cleanup dependency.
   - Convert every managed V1 property expression-unique index into logical
     constraint registry metadata before copying data. Root-key and endpoint
     identity choreography remain separate recorded operations.
   - Backfill identity and property claims in deterministic keyset chunks.
   - Validate bidirectional row-to-claim ownership.
   - Swap the root key only while either the V1 key or the selected registry
     authority remains continuously effective.
3. Stage and cut over graphs in deterministic graph-ID order:
   - Create collision-free staging parents and leaves for
     stored union declared kinds.
   - Preserve owners, ACLs, comments, storage, tablespaces, graph check, index
     hierarchy, and additive provenance.
   - Copy in bounded deterministic chunks using the approved trigger strategy.
   - Validate before cutover.
   - Atomically detach/rename legacy, attach/rename staging, restore metadata,
     restrict legacy access, and checkpoint exact object maps.
4. Resume skips a chunk or graph only after revalidating the complete durable
   checkpoint. Successful-but-unknown commits reconcile from database facts.
5. After all graph cutovers, install and validate the final
   `edge_identity`-to-edge cleanup foreign key or the Phase 1 approved
   equivalent, then remove migration-only cleanup triggers before pending
   acceptance.
6. Replace count-only validation with:
   - Total and per-kind counts.
   - Min/max and endpoint/kind aggregates.
   - Exact bidirectional typed row equality using `EXCEPT`. A replacement is
     permitted only through a separately approved ADR with PG16/18 proof.
   - Versioned typed row digests including IDs, endpoints, kinds, and
     properties.
   - Identity/property claim ownership and orphan detection.
   - Hierarchy, bounds, DEFAULT absence, indexes, triggers, routines,
     constraints, sequence behavior, owners, ACLs, and dependencies.
   - Graph-isolation direct, Cypher, path, and hydration smoke.
7. Persist each validation attempt and digest atomically with
   `v2_pending_acceptance`. Historical or non-active migrations cannot be
   accepted.
8. Acceptance must:
   - Authenticate the exact preflighted non-owner target.
   - Revalidate fast topology, registry, sequence, fence, manifest, current
     pointer, identity epoch, and validation digest.
   - Apply final current/future grants.
   - Persist and point to the immutable OID-independent cleanup map, including
     current object facts and fingerprints, in the acceptance transaction.
   - Record rollback cutoff and acceptance receipt.
   - Set `post_acceptance_smoke_status=pending`.
9. `smoke` performs the explicit target sentinel transaction and records the
   result through the operator connection. Runtime stays closed until the
   current receipt passes or the contractually approved fallback is recorded.

### Tests and evidence

- Empty, populated, skewed, multi-graph/multi-kind, declared-empty-kind, dynamic
  kind, explicit-ID, and property-constraint fixtures.
- Failure before/after every chunk, global key swap, graph cutover, validation,
  acceptance, smoke, receipt, and report commit.
- One-row substitutions, property changes, endpoint/kind changes, missing
  claims, orphan claims, wrong bounds/indexes/triggers, and equal-count
  corruption all fail validation.
- Concurrent old-writer attempts remain blocked throughout.
- PG16/PG18 API and built-binary end-to-end migration.

### Exit gate

- `resume` continues the same migration without skips, duplicates, or new IDs.
- Frozen V1 logical data and constraints equal pending V2.
- Acceptance cannot occur without exhaustive current validation and the exact
  target identity.
- Runtime cannot operate until the current target smoke passes.

## Phase 8: Pre-acceptance rollback, V1 handoff, restore/rebind, and cleanup

### Dependencies

Phase 7 durable checkpoints and object maps.

### Work

1. Permit in-place rollback only before acceptance and only when:
   - The exact active migration is selected.
   - The writer fence is intact.
   - The target was never granted or accepted.
   - No acceptance or target-smoke receipt exists.
   - The fence/ACL digest remains exact.
   - Target, staging, and registry data match their checkpoint digests.
   - Source, staging, legacy, and registry object maps still match.
2. Roll back graph cutovers in reverse order with per-graph checkpoints.
3. Restore the exact frozen V1 contract:
   - Root keys and indexes.
   - One-argument and other V1 helper signatures.
   - Triggers, property uniqueness, owners, grants/default ACLs, comments,
     dependencies, storage, tablespaces, and sequence effective-next values
     without unsafe rewind.
4. Remove only recorded V2 objects. Preserve the control journal, receipts,
   validation history, and rollback evidence.
5. Finish structural rollback with one atomic pointer transition that:
   - Clears `active_migration_id`.
   - Sets `current_rollback_migration_id`.
   - Records the migration as `rolled_back`.
   - Sets V1 handoff pending and clears the current handoff pointer.
   - Retains the writer fence and inverse-state receipts.
6. `complete-v1-handoff` directly authenticates the distinct preparation role
   and a new sole-active credential, applies exact V1 grants, records the
   receipt, and only then removes the database fence.
7. Implement `rebind-restore` before cleanup:
   - Same-cluster, detectably restored bootstrap/in-progress/rolled-back/
     accepted states follow their state-specific rebind and smoke/handoff
     reset.
   - Independent-cluster rebind is supported only for accepted V2.
     In-progress, pending, and rolled-back cross-cluster restores return stable
     unsupported-state errors.
   - Validate historical scope and new database identity.
   - Rebind OID-independent maps to current catalog facts.
   - Revoke copied stale grants where required.
   - Advance identity epoch.
   - Reset target smoke or V1 handoff to pending.
   - Reject stale pre-restore receipts and confirmations.
8. Consume the immutable cleanup map created by Phase 7 acceptance. A migrated
   scope has recorded legacy targets; fresh bootstrap has a canonical empty map
   and must refuse cleanup as not applicable.
9. Cleanup requires:
   - Current accepted identity epoch and smoke receipt.
   - Configured soak duration.
   - Fresh backup and successful restore rehearsal evidence.
   - Explicit operator approval and current fleet-record receipt.
   - Action-specific confirmation bound to the cleanup map.
10. Revalidate every cleanup target by current OID, name, relkind, dependency,
    and fingerprint. Never drop by generated name or pattern alone.
11. Commit the exact drops, canonical empty cleanup-map facts/digest, migration
    cleanup state, layout pointer to that empty map, nonce consumption, and
    cleanup receipt atomically. Regenerate a lost report from the receipt.
12. After acceptance, in-place rollback remains forbidden; recovery is forward
    repair or restore/PITR plus rebind.

### Tests and evidence

- Migration then rollback then preparation handoff with the frozen V1 client.
- Crash/ambiguous commit at every reverse-cutover and handoff boundary.
- Missing V1 helper/index/ACL/comment/sequence restoration fails the gate.
- Cleanup before smoke/soak/backup/fleet approval is rejected.
- Same-name replacement and OID reuse are rejected during cleanup.
- Same-cluster PITR for each supported state; independent-cluster accepted-V2
  rebind succeeds while other cross-cluster states return their stable
  unsupported errors.
- Stale smoke, role, epoch, object map, receipt, and confirmation rejection.

### Exit gate

- Pre-acceptance rollback recreates the characterized V1 shape and remains
  fenced until a durable preparation-role handoff.
- Cleanup can delete only the exact recorded legacy object set.
- Restored databases cannot operate or clean up until identity is rebound and
  the state-appropriate smoke/handoff is repeated.

## Phase 9: Operational callers, test matrix, artifacts, performance, and docs

### Dependencies

All Phase 0-8 exit gates.

### Work

1. Provide one public read-only readiness API returning typed layout, target,
   identity, smoke, restore, and drift errors.
2. Update Retriever, GraphBench, plan corpus, benchmark, export, and dawgrun:
   - Readiness before schema assertion or query translation.
   - Application connections are accepted non-owner targets only.
   - No implicit bootstrap through an application DSN.
   - Complete topology validation instead of `to_regclass` existence.
   - Retriever keyset scans preserve strict global ID order with no duplicates
     or omissions when IDs are interleaved across leaves.
   - Dump/benchmark/corpus metadata includes layout version, topology
     fingerprint, graph/kind/leaf counts, and PostgreSQL facts.
   - GraphBench sums heap/index/TOAST size across leaves, performs deterministic
     per-leaf `VACUUM ANALYZE`, records analyze freshness, and proves reset/
     registry consistency.
   - Plan corpus reports one-, multi-, all-, planned-, removed-, and
     executed-leaf summaries.
   - Preserve and checksum the final V1-compatible Retriever/export artifacts;
     document transition Retriever/export as V2-only.
3. Add two mutation defenses to every Raw/EXPLAIN tool:
   - Pure frontend mutation rejection before mapper/translation.
   - Reject `translation.HasMutations` immediately before execution.
   - Format-only tools use a read-only existing-kind mapper.
   - Mutation plans, where deliberately tested, run only in rollback-only
     fixtures.
   - Parse/validate once and share the validated query with both measurement
     and EXPLAIN so GraphBench cannot execute a mutation before rejecting its
     plan.
4. Use structured
   `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` and record planned, removed, and
   executed leaf sets plus layout fingerprint and server facts.
5. Complete the required Make targets scaffolded in earlier phases:
   - `prepare_test_pg`.
   - `test_migration`.
   - `test_pg_physical`.
   - `benchmark_transition`.
   - `migration_preflight_fixture`.
   - `migration_artifact`.
   - Retain `build_pg_migrate` and semantic test targets.
   - Declare targets in `.PHONY`, document them in help, and make cleanup use
     exact recorded paths.
6. Add test suites:
   - Unit: catalog, classifier, schema equality, names, caches, registries,
     translator, state machine, confirmations, checkpoints, receipts, CLI,
     pool hooks, and strict schemas.
   - Shared semantics: multi-kind create/read/update/delete and exact survivors
     without backend-specific expected values.
   - `pg_physical`: hierarchy, bounds, tableoid routing, drift, roles, identity,
     property constraints, dynamic leaves, reset, optimize, and pruning.
   - `migration_integration`: frozen V1, fence, copy, failpoints, rollback,
     handoff, acceptance, smoke, cleanup, and restore/rebind.
   - Race tests for caches, provisioning, claims, layout, migrator, and CLI.
   - Source compatibility for `DriverName`, `pg.NewDriver`, package paths,
     PostgreSQL URL schemes, DSN behavior, and exported typed errors.
   - Generated `relationship_mutation_shapes.json` with direction, property,
     and type decoys; run `make test_update` followed by
     `git diff --exit-code`.
   - An executable client/state compatibility matrix covering the frozen V1
     client, historical pre-marker client, transition runtime, and migrator
     against V1, every fence/copy state, pending acceptance, rolled-back
     handoff pending/passed, and accepted V2.
7. Establish CI:
   - PG16 and PG18 plus every major the ADR marks supported or
     compatibility-tested: semantic, physical, migration, built-binary, and
     performance jobs, each asserting `server_version_num`.
   - Separate operator/target roles for semantic tests. Bootstrap closes and
     removes operator credentials/resources before exposing only the mode-0600
     target envelope to semantic and tool steps.
   - Admin-coordinator disposable databases for physical/migration tests.
   - Independent second cluster for restore/rebind.
   - Frozen fixture checksum and generated-worktree verification.
   - Neo4j shared semantic suite remains green.
   - No overall coverage reduction plus meaningful branch/package coverage
     floors for the migration engine, CLI, catalog, classifier, schema,
     reports, confirmations, receipts, and checkpoints.
   - Explicit PR, release, and scheduled-suite cadence, timeouts, retained
     evidence uploads, and harness-lifecycle jobs.
8. Build a reproducible migrator artifact:
   - `CGO_ENABLED=0` passed explicitly.
   - `-trimpath` and link-time version/source revision.
   - Output at `dist/dawgs-pg-migrate` with binary and checksum-file
     verification.
   - Static-link and `go version -m` metadata verification.
   - Repeat-build byte reproducibility from a clean checkout.
   - Retained module files, source/fixture/schema metadata, publication owner,
     retention owner, and EOL policy.
   - Built binary, not only package APIs, executes the full command workflow.
9. Implement `cmd/benchdiff` and a strict versioned comparison envelope.
   Reject V1/V2 comparisons whose fixture checksum, cases, parameters, seeds,
   adapters, units, schedule, or environment facts differ.
10. Run the approved topology/performance matrix at minimum:
    - 1 graph by 1, 10, and 30 kinds.
    - Several graphs by 30 kinds.
    - Approved maximum topology.
    - Uniform, skewed, and severe-skew data.
    - Zero, one, and many property constraints.
    - Custom and generic plans.
    - Dedicated disposable databases only; `quality_bench` and transition
      benchmarks may never wipe semantic-test or application databases.
    - Counterbalanced V1/V2 execution with distinct comparator and migration
      writer roles so fencing the migration clone cannot alter the comparator.
    - Recorded hardware/provider class, PostgreSQL configuration/extensions,
      cold/warm cache protocol, analyze state, batch sizes, repetitions,
      medians/p95/confidence, concurrency, and timeouts.
11. Measure correctness and cost for one/multi/no-kind reads, hydration,
    direct/batch/Cypher writes, registry contention, explicit IDs, mass
    mutation, cascade, wipe, backfill, vacuum, startup, planning, migration
    locks/WAL/disk, relation count, relcache, lock headroom, sequence behavior,
    and autovacuum behavior.
12. Add bounded-cardinality observability for:
    - Structured layout/readiness errors and fingerprints.
    - Leaf-ensure latency/result and schema-lock waits.
    - Registry conflicts, retries, deadlocks, orphan checks, bloat, and
      autovacuum.
    - Migration heartbeat/rate/WAL/capacity and sequence health.
    - Planned/removed/executed leaves.
    Define pause/abort thresholds. Never emit DSNs, property values, graph/kind
    names, raw relation names, secrets, or high-cardinality labels.
13. Complete documentation:
    - Operator migration and recovery runbook.
    - Manifest/report/evidence schema reference.
    - Writer fence, target handoff, V1 handoff, and role procedures.
    - Restore/rebind matrix.
    - Rollback cutoff and cleanup receipts.
    - Supported versions, limits, and pruning model.
    - Development testing instructions.
    - Tool-specific readiness changes.
    - Wave checklist, fleet ledger, V1 EOL, and late-upgrade archive policy.
    - Pull-request evidence checklist.

### Required client/state compatibility matrix

| Client/tool | Frozen V1 | Fence/copy states | Pending V2 | Rolled-back V1 | Accepted V2 |
|---|---|---|---|---|---|
| Frozen V1 preparation client | Accept exact V1 | Refuse | Refuse | Accept only handoff-passed exact V1 | Refuse |
| Historical pre-marker client | Accept only before maintenance | Blocked by fence | Blocked | Blocked; only the frozen preparation client may run | Blocked |
| Transition runtime | Migration-required | Refuse with recovery state | Refuse with accept guidance | Refuse; direct operators to the preparation client | Exact target plus current smoke only |
| Migrator | Inspect/preflight/migrate | Status/resume/rollback as state permits | Validate/accept/rollback | Status/handoff/rebind | Validate/health/smoke/rebind/cleanup |

Every cell has a built-artifact test, not only a package-level assertion.

### Required CI matrix

| Suite | PG16 | PG18 | Identity model |
|---|:---:|:---:|---|
| Unit, race, coverage | N/A | N/A | No database or mocked catalog |
| Accepted-target semantics | Yes | Yes | Separate operator and target |
| Destructive physical | Yes | Yes | Coordinator plus random DB/roles |
| Frozen-V1 migration/failpoints | Yes | Yes | Full migration role set |
| Built-binary workflow | Yes | Yes | Operator then exact target |
| Independent restore/rebind | Yes | Yes | Two independent clusters |
| Transition performance | Yes | Yes | Isolated frozen V1 and V2 |
| Neo4j shared semantics | Existing | Existing | Existing backend |

Replicate every applicable PostgreSQL column for any additional major the
Phase 1 ADR labels supported or compatibility-tested.

### Exit gate

- G01-G23 and every code-activation locked contract map to passing automated
  evidence. Phase 9 must deliver G24 schemas/runbooks, but fleet and horizon
  outcomes close only in Phases 10-12.
- All required CI jobs and the built artifact pass on PG16, PG18, and every
  additional major declared supported or compatibility-tested.
- Pruning touches only permitted leaves.
- Approved absolute SLO and V1 regression budgets pass.
- Runbooks can execute every normal and recovery state without source-code
  interpretation.

## Phase 10: Activation release and full rehearsal

### Dependencies

All Phase 0-9 exit gates.

### Work

1. Run a production-scale rehearsal using the exact release candidate:
   - Frozen V1 installation and old writer.
   - Immutable final V1 backup/restore evidence before preflight and
     confirmation.
   - Preflight, fence, verification, resumable migration, validation,
     acceptance, target smoke, soak, verified fresh V2 backup/restore
     rehearsal, then cleanup.
   - Separate rollback rehearsal ending in preparation-role V1 handoff.
   - Accepted pre-cleanup restore/rebind, accepted post-cleanup restore/rebind,
     fresh-bootstrap restore/rebind, and rolled-back same-cluster handoff.
     Rolled-back cross-cluster restore must return its stable unsupported error.
   - Failpoint and unknown-commit injection at selected high-risk boundaries.
2. Review retained evidence against
   `docs/edge_kind_partitioning_traceability.md`.
3. Make the activation changeset intentionally small:
   - Remove the Phase 0 interlock.
   - Enable real pending bootstrap/handoff/smoke.
   - Ship the exact runtime and migrator artifact together.
   - Publish artifact/fixture/schema/runbook checksums.
4. Refuse promotion if any code, fixture, artifact, runbook, role, recovery,
   performance, or evidence gate differs from the rehearsed candidate.

### Exit gate

- The exact shipped artifacts complete both forward and rollback rehearsals.
- Fresh bootstrap activates only after exact target smoke.
- The release system makes the migrator available before installations can
  receive the V2-only runtime.

## Phase 11: Fleet transition and V1 runtime retirement

### Dependencies

Phase 10 release.

### Work

1. Maintain one authoritative fleet row per installation containing:
   - Installation/database identity and ownership.
   - Application, runtime, migrator, artifact, manifest, and PostgreSQL
     versions/checksums.
   - Inventory, capacity, backup, rehearsal, preflight, and blockers.
   - Writer probe, fence, drain, retirement, and external-writer evidence.
   - Migration, validation, acceptance, target smoke, soak, backup, cleanup,
     restore/rebind, and final health receipts.
   - Scope type: migrated installations require a cleanup receipt; fresh
     bootstrap installations require the canonical empty cleanup-map receipt
     and must never invoke cleanup.
   - A formal decommission record when applicable, using the versioned schema,
     required approver identity, reason, ownership proof, last-known state,
     writer-retirement proof, time, and supporting evidence digest.
2. Execute staged waves with explicit pause/abort thresholds and rollback
   readiness.
3. Treat unknown, unreachable, V1, migrating, pending, mixed, drifted, or
   unverified installations as open blockers. Silence is not decommissioning.
4. **Fleet-closure gate/release:** after every supported installation is
   accepted V2 or formally decommissioned, verify no old writer can reconnect,
   freeze and re-read the authoritative fleet ledger, and approve the
   fleet-transition milestone separately.
5. **V1-runtime-retirement gate/release:** only after the frozen ledger passes
   an independent audit, retire active V1 runtime behavior:
   - Remove V1 bootstrap, reconciliation, write, and operational branches.
   - Keep actionable V1 detection and the separately retained late-upgrade
     path.
   - Remove routine V1 runtime CI only in this separate retirement release.

### Exit gate

- Every fleet installation is accepted V2 or formally decommissioned.
- Required soak, backup, cleanup, and old-writer retirement are complete.
- The active runtime and operational tools have one V2 schema model.

## Phase 12: Late-upgrade retirement and immutable archive

### Dependencies

Phase 11 plus the published support horizon.

### Work

1. Retain throughout the horizon:
   - Exact migrator binary and SHA-256.
   - Source/module identity and build metadata.
   - Frozen V1 schema, seed, catalog fixtures, and checksums.
   - Versioned manifest/evidence/report/receipt schemas.
   - Supported PostgreSQL matrix.
   - Operator/recovery runbook and EOL dates.
2. Execute the retained artifact and frozen-fixture workflow at the published
   scheduled cadence on every supported major throughout the horizon. Retain
   reports and page an owner on failure.
3. At horizon end, verify no supported late-upgrade obligation remains.
4. Perform an archive retrieval drill in a clean environment: fetch by
   documented coordinates, verify every checksum/signature, execute the
   compatibility workflow, and reproduce the expected reports.
5. Remove active migration code and routine migration CI from the current
   branch only after the immutable archive drill is independently approved.

### Exit gate

- The transition archive is complete, immutable, retrievable, and verified.
- The clean-environment retrieval/execution drill passes on every retained
  supported major.
- Active V1 migrator code and fixture CI have left the current branch.
- The published late-upgrade obligation has ended.

## Mandatory phase reporting

Every phase report must contain:

1. Phase and gap IDs addressed.
2. Exact source revision and artifact/fixture digests.
3. PostgreSQL versions actually exercised.
4. Test commands and retained report locations.
5. Passed, failed, waived, and still-open gates.
6. New risks or contract changes; contract changes require an ADR update and
   re-entry at the earliest affected phase.
7. Rollback or containment state if the phase did not complete.

No phase may report “complete” with a failed or missing exit criterion.

## Traceability closure matrix

| Review finding | Required closure evidence |
|---|---|
| Acceptance/runtime marker gate | G01/G02 tests, identity-bound classifier, bootstrap and acceptance smoke receipts |
| Incomplete topology classifier/health | G03 typed catalog fixtures, physical/logical drift matrix, stable fingerprint |
| Unsafe qualification/naming/schema normalization | G06 hostile-search-path, identifier, and duplicate-schema tests |
| Missing writer fence | G12 direct-auth negative probes, retirement evidence, fence failpoints |
| Incoherent preflight | G13 repeatable snapshot, strict manifest, role/capacity/backup and stale-fact rejection |
| Fake resume/unbounded copy | G14 checkpoint, unknown-commit, and same-migration resume tests |
| Broken relationship identity upsert | G09 PG16/18 direct and batch property-identity tests |
| Lost/property constraints | G10 frozen V1 migration plus add/drop/re-add/concurrency tests |
| Incomplete edge identity/routing behavior | G11 root/leaf/COPY/explicit-ID/immutable-routing/deadlock tests |
| Count-only validation | G15 equal-count corruption, claim, topology, and digest rejection tests |
| Missing role separation | G16 direct-auth owner/operator/target/writer/preparation grant and handoff tests |
| User string rewriting | G04 byte-for-byte literal/parameter tests |
| Graph-unsafe helpers | G05 colliding-ID multigraph helper/path tests |
| Mutable graph catalog/drift | G03/G07 deletion guard, ACL, orphan, and topology tests |
| Commit-unsafe caches | G08 rollback, commit-failure, unknown-outcome, and reconnect tests |
| Unsafe rollback | G17 frozen-client compatibility and metadata round-trip |
| Unsafe cleanup/restore | G18 OID/map, soak/backup/fleet, OID-reuse, and rebind tests |
| Weak confirmations/reports | G19 replay, wrong-database, expiry, evidence-change, report-failure tests |
| Missing coverage/CI/fixture | G21 complete matrix and verified frozen checksums |
| Temporary PostgreSQL hazards | G22 lifecycle, signal, parallel, existing-instance, and cleanup tests |
| Unsafe operational callers | G20 readiness and two-stage mutation rejection for every named tool |
| Missing pruning/performance/artifact/docs | G23 structured plans, budgets, reproducible artifact, runbooks |
| Fleet/retirement incompleteness | G24 fleet ledger, EOL horizon, and immutable archive |

## Definition of done

Completion has three non-interchangeable milestones:

1. **Fleet transition complete:** every supported installation is accepted V2
   or formally decommissioned; required soak, backup, cleanup, and old-writer
   retirement are complete.
2. **V1 runtime retirement complete:** active runtime, operational tools,
   normal CI, and current documentation have one V2 schema model; only
   actionable V1 detection and the retained late-upgrade path remain.
3. **Late-upgrade retirement complete:** the support horizon has ended, active
   migration code and routine fixture CI have left the current branch, and the
   immutable transition archive has been verified.

The gap-closure program is complete only at milestone 3. Before then, status
must name the highest completed milestone and the remaining support obligation.
