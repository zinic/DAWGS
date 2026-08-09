# PostgreSQL Edge Kind Partitioning Implementation Plan

> ADR 0014 supersedes this plan's per-operation runtime-guard requirements with
> exhaustive once-per-service-start assertion and once-per-backend identity checks.

## Status and authority

This is the execution plan for the architecture and retirement contract in
[edge_kind_partitioning.md](edge_kind_partitioning.md). That high-level
direction is committed:

- The existing pg driver advances to schema version 2.
- Version 2 partitions edges first by graph_id and then by kind_id.
- Every supported version 1 installation, including empty installations, must
  migrate or be formally decommissioned.
- Version 1 is a migration source, not a permanent runtime option.
- After fleet adoption, version 1 runtime code leaves the active driver. After
  the later published upgrade horizon, version 1 migration code and CI leave
  the active branch.

This document turns that contract into code changes, dependency ordering,
release gates, migration checkpoints, validation work, and fleet exit criteria.
It does not claim the implementation exists. Exact SQL mechanics may change
after the required prototypes, but only if the replacement preserves every
locked contract below. Changing a locked contract requires a separately
approved ADR and an update to edge_kind_partitioning.md; it is not an ordinary
implementation choice.

The implementation owner should maintain this document as the program
checklist. A work package is complete only when its exit gate is supported by
reviewed code and reproducible evidence.

## Implementation readiness verdict

Repository review on 2026-08-06 at revision `bd64d91` found no reason to reopen
the committed two-level partitioning direction, but it did find prerequisites
that the first draft treated as existing behavior. This revision closes those
specification gaps. The plan is ready to begin with WP0 and WP1; it is not
permission to skip their evidence gates or activate partially compatible V2 DDL.

The reviewed baseline is:

- The current translator and formatter still emit executable root `node` and
  `edge` references. Graph-scoped persistent-relation rendering is required
  implementation work, not behavior that can merely be retained.
- The current GitHub workflow runs PostgreSQL 18 only. PostgreSQL 16 and 18 are
  the required initial validation majors; WP0 must add that matrix and decide
  explicitly whether PostgreSQL 17 is supported, compatibility-tested, or
  outside the published support set.
- The current V1 schema has no durable layout marker, migration journal,
  identity registry, property-claim registry, or kind-leaf registry.
- `cmd/benchmark` and `tools/dawgrun`, as well as the named operational commands,
  enter the PostgreSQL driver and are in scope for runtime-gate compatibility.

Implementation proceeds only through these join gates:

| Gate | Evidence required before proceeding |
|---|---|
| R0: frozen source contract | Frozen V1 DDL/fixtures, current API behavior, canonical namespace and role model, supported-major ADR, and PostgreSQL 16/18 CI jobs |
| R1: database mechanism | Identity/property enforcement, direct-leaf/COPY behavior, trigger propagation, reset/cascade, low-lock leaf attachment, and reversed-order concurrency prototypes pass on both required majors |
| R2: SQL compatibility | Graph-scoped persistent-relation rendering, helper DDL and call sites, hydration, mutation scoping, and structural translation tests land atomically |
| R3: migration safety | Durable fence intent, directly authenticated writer probes, external-credential evidence, resumable global registry backfill, catalog/ACL rollback snapshot, capacity/backup attestations, failpoints, and frozen-fixture rehearsals pass |
| R4: accepted V2 | WP2-WP5 join tests pass before any normal runtime can create or accept `v2_accepted`; WP6-WP8 pass before release promotion |

No intermediate commit may create or recognize an application-usable
`v2_accepted` database while the active translator, helpers, direct/batch paths,
or enforcement schema are incompatible. Prototype failures are resolved in WP0
and recorded in the named ADRs before dependent implementation begins.

## Terms

- V1: the current edge root partitioned by graph_id, with edge_<graph_id> as a
  heap partition.
- V2: the destination hierarchy in which edge_<graph_id> is a partitioned
  parent and edge_<graph_id>_<kind_id> is a physical leaf.
- Preparation release: the final V1-compatible DAWGS module tag/source
  artifact plus a version-pinned compatibility client. It recognizes the V2
  marker and refuses to operate on V2, but continues to serve V1.
- Transition release: the first generally available runtime that initializes
  and operates only V2 and rejects V1 with migration guidance.
- Acceptance: the explicit, atomic state and privilege transition after
  migration validation. It is also the in-place rollback cutoff.
- Supported installation: any installation still covered by product support,
  regardless of whether it is currently reachable or actively writing.
- Canonical manifest: the application-supplied, canonically ordered list of
  materialized graphs and declared edge kinds, including kinds with no stored
  rows.

## Committed outcome

The steady-state topology is:

~~~text
edge                              LIST (graph_id)
└── edge_<graph_id>               LIST (kind_id)
    ├── edge_<graph_id>_<kind_id>
    └── edge_<graph_id>_<kind_id>
~~~

The graph parent remains the stable SQL target. V2 read and write paths must map
only persistent `node` and `edge` relations and mutation targets to
`node_<graph_id>` and `edge_<graph_id>`; the current formatter does not yet do
this. PostgreSQL then routes rows and prunes physical kind leaves from usable
kind_id predicates. There is no DEFAULT leaf.

The transition is complete only when:

1. Truly uninitialized databases atomically bootstrap complete V2 with an
   accepted/pending-smoke marker and become operational only after the durable
   target-role smoke/fallback passes.
2. Any initialized V1 database, whether empty or populated, cannot be opened by
   the target runtime.
3. The standalone migrator can inspect, preflight, fence, migrate, resume,
   validate, roll back before acceptance, accept, and clean up.
4. Direct, batch, and Cypher writes provision and route dynamic kinds safely.
5. Database enforcement preserves graph-local edge identity, immutable routing
   columns, and graph-wide edge property uniqueness across kinds.
6. Reads, mutations, path hydration, node cascades, graph data reset/wipe,
   maintenance, export, Retriever, GraphBench, and plan corpus are V2-aware.
7. Shared semantic tests stay backend-equivalent, PostgreSQL physical tests
   prove the hierarchy, and PostgreSQL 16 and 18 pass.
8. Every supported installation is accepted, soaked, backed up, and cleaned,
   or has a formal decommission record.
9. V1 runtime paths are removed after fleet retirement; the frozen migrator
   remains only for the published late-upgrade horizon and is then archived.

## Locked contracts

The following are not design questions for implementation:

1. Keep one PostgreSQL driver. Preserve DriverName pg, pg.NewDriver, package
   paths, URL handling, and the DSN contract.
2. Do not add a driver variant, a storage-layout flag, dual-layout runtime
   behavior, or a greenfield-only general release.
3. Use hierarchical LIST partitioning. PostgreSQL LIST does not accept a
   literal two-column partition key, so graph_id and kind_id form an effective
   composite through two routing levels.
4. Keep edge_<graph_id> as the graph-scoped parent used by translated SQL.
5. Do not create a DEFAULT kind partition.
6. Preserve database-enforced uniqueness of (graph_id, edge ID), even for raw
   explicit-ID writes into different kind leaves.
7. Treat id, graph_id, and kind_id as immutable after insertion.
8. Preserve graph-wide edge property uniqueness across kinds, including the
   current text extraction, collation, missing-property, JSON-null, SQL-null,
   and conflict/upsert behavior.
9. Treat relationship kind mutation as unsupported. A future kind change is a
   delete-and-create operation, not an ordinary update.
10. Require an offline maintenance window and an independently proven writer
    fence. There is no online dual-write migration in this program.
11. Require a canonical application manifest. Database inspection alone cannot
    recover declared-but-empty edge kinds.
12. Permit in-place rollback only before acceptance and before any V2
    application write. After acceptance, recovery is forward repair or restore
    and reconcile.
13. The driver recognizes uninitialized only as an operator-bootstrap
    candidate. Ordinary target runtime accepts only `v2_accepted` with
    smoke/fallback passed. It rejects V1,
    in-progress, pending-acceptance, mixed, drifted, and unrecognized states.
14. Fleet silence does not count as migration. Missing or unreachable supported
    installations block retirement until found or formally decommissioned.
15. Managed graph catalog rows are immutable in steady-state V2. The target role
    cannot delete them and a managed trigger rejects raw graph-row deletion;
    `WipeGraph` removes data while retaining graph rows, constraints, and
    topology. Only explicit full schema teardown drops graph rows after removing
    the guard. An owner bypass or missing graph row with retained partitions is
    hard drift, not a tombstoned graph lifecycle.

## Explicit non-goals

This work does not include node kind partitioning, automatic removal or merging
of unused leaves, hash subpartitioning, zero-downtime migration, public
relationship-kind mutation, public graph-partition teardown, a new path model
solely for pruning, or backend-specific expected semantics in the shared
integration corpus.

## Program dependency map

~~~text
WP0 contracts, fixture, characterization, prototypes, baselines
 │
 └── WP1 catalog facts, layout classifier, state/error contracts
      │
      ├── WP2 V2 DDL, registries, topology model, unreleased bootstrap path
      │    ├── WP3 graph/kind lifecycle, locks, privileges, caches
      │    │    └── WP4 direct/batch/Cypher writes and read correctness
      │    └── WP5 constraints, cascades, reset, maintenance
      │
      └── WP6a migration contracts, status, manifest, read-only preflight

WP2 + WP4 + WP5 + WP6a
      └── WP6b fence, copy, cutover, validation, rollback, acceptance

WP4 + WP5 + WP6b
      └── WP7 operational tools, CLI packaging, release artifacts

WP2 through WP7
      └── WP8 integration, migration, plan, performance, and CI gates
           └── WP9 transition release and fleet rollout
                └── WP10 V1 runtime and late-upgrade retirement
~~~

Only scaffolding and stable interfaces may be parallelized ahead of these
dependencies. WP3 requires WP2 topology, WP4 requires WP3 lifecycle, and the
mutating half of WP6 requires the final WP2/WP5 enforcement and teardown
behavior. WP6a can proceed after WP1 because it is read-only. WP8 is the join
gate over all implementation tracks.

The activation change is deliberately last: no generally available runtime
becomes V2-only until initialization, migration, resume, validation,
pre-acceptance rollback, operational tooling, packaging, and documentation are
ready together.

## Repository change map

The main implementation seams are:

| Area | Existing files | Required direction |
|---|---|---|
| Physical schema | `drivers/pg/internal/schema/sql/schema_up.sql` and `schema_down.sql` | Canonical V2 definitions are owned by internal/schema; query/bootstrap and migrator share V2 root/parents/leaves, marker, registries, triggers, and helpers |
| Names and model | drivers/pg/model/format.go, model.go | Kind-leaf naming and typed parent/leaf topology |
| Catalog discovery | drivers/pg/query/query.go, definitions.go, select_table_indexes.sql | Replace index-DDL regex parsing with typed PostgreSQL catalog facts |
| Embedded SQL | drivers/pg/query/sql.go | Embed catalog/layout SQL and correct asset bindings |
| Schema lifecycle | drivers/pg/manager.go, driver.go, pg.go | Runtime gate, canonical schemas, commit-safe caches, graph/kind reconciliation |
| Pool/type initialization | drivers/pg/pg.go | Preserve caller hooks; run no managed-type SQL before readiness; load only schema-qualified composites on an exact accepted target session |
| Direct writes | drivers/pg/transaction.go, relationship.go, statements.go | Leaf ensure, graph/kind scoping, registry-backed upsert |
| Batch writes | drivers/pg/batch.go | Per-graph buffering, distinct-kind ensure, safe retry semantics |
| Cypher | cypher/models/pgsql/translate, cypher/models/pgsql/format | Graph-scoped persistent-relation rendering, mutation kind metadata, metadata-preserving execution, immutable edge kinds, graph-safe helper calls |
| Integration | integration and integration/testdata | Shared semantic coverage plus PostgreSQL-only physical/concurrency tests |
| Migration | new drivers/pg/migrate and cmd/pgmigrate | Standalone raw-pgx migration engine and dawgs-pg-migrate binary |
| Operations | cmd/retriever, cmd/graphbench, cmd/plancorpus, cmd/export, cmd/benchmark, tools/dawgrun | V2 topology, sizing, pruning, comparison, and runtime-gate awareness |
| Maintenance | drivers/pg/optimize.go, table_sizes() | Nested-tree analyze/vacuum and logical size reporting |
| Build and CI | Makefile, .github/workflows, AGENTS.md | Migrator artifact, disposable migration suite, PostgreSQL 16/18 matrix |
| Documentation | README.md, docs, command READMEs, new runbook | V2-only runtime, upgrade, rollout, recovery, and EOL |

## Implementation architecture

### Package boundaries

Use these package seams unless WP0 demonstrates a concrete cycle or API
problem:

~~~text
drivers/pg/internal/catalog/
  catalog.go
  model.go
  sql/*.sql

drivers/pg/internal/layout/
  state.go
  marker.go
  classify.go
  topology.go
  locks.go
  errors.go

drivers/pg/internal/schema/
  definitions.go
  ddl.go
  sql/*.sql

drivers/pg/migrate/
  manifest.go
  report.go
  inspect.go
  preflight.go
  fence.go
  prepare.go
  graph.go
  validate.go
  rollback.go
  accept.go
  cleanup.go
  failpoint.go
  sql/*.sql
  testdata/v1_schema.sql
  testdata/manifests/*.json

cmd/pgmigrate/
  main.go
  config.go
  output.go
  main_test.go
  README.md
~~~

The shipped command is named dawgs-pg-migrate even though the Go source
directory is cmd/pgmigrate.

internal/catalog exposes typed facts, not DAWGS policy. internal/layout
classifies those facts and owns state, lock identifiers, and typed errors. The
steady-state driver and the migrator may both import these internal packages.
The migrator must not call dawgs.Open, pg.NewDriver, SchemaManager.AssertSchema,
or any bootstrap path because it must inspect V1 and mixed layouts safely.
`internal/schema` is the single canonical source for V2 object definitions and
typed DDL fragments. Move the V2 contents of
`drivers/pg/internal/schema/sql/schema_up.sql` and `schema_down.sql` into
`drivers/pg/internal/schema/sql`; remove their query-package embed bindings.
Query/bootstrap calls the internal/schema API, and migration uses the same
definitions with different orchestration. Only frozen V1 fixtures and
migration-specific staging/cutover SQL remain separate. Do not keep hand-edited
mirrors. CI compares post-bootstrap and post-migration fingerprints plus
function/trigger definitions.

Do not create a public StorageLayout abstraction. The only public driver remains
pg and only V2 is its target steady state.

Package `pg` exports stable sentinel errors such as `ErrMigrationRequired`, an
exported `LayoutErrorCode`, and an exported detail interface/type. Internal
classifier errors wrap that contract so `errors.Is` and code extraction survive
`dawgs.Open`, `graph.Result`, and command wrapping. Layout health is an optional
PostgreSQL contract (`pg.LayoutHealthProvider` plus a type-asserting helper), not
a new method on the backend-neutral `graph.Database` interface.

Operational commands outside drivers/pg cannot import Go internal packages.
Expose one narrow read-only V2 health DTO/method from the pg package with layout
version, topology fingerprint, graph-parent count, leaf count, and categorized
drift. Retriever, GraphBench, plan corpus, and export may consume that method
after the normal runtime gate. It does not select a layout or expose V1 mutation
behavior. The migrator continues to use the richer internal classifier through
drivers/pg/migrate.

Where runtime classification needs registry evidence, call a fixed
SECURITY DEFINER health function that returns only counts/digests and never
property values. Do not grant the target role broad claim-table SELECT merely
to implement startup checks.

Use one metadata-preserving translated-query pipeline. Add `GraphID int32`,
`HasMutations bool`, and `RequiredEdgeKindIDs []int16` to `translate.Result`,
accumulated across all query parts and sorted/deduplicated only after successful
translation. Add a
compatibility-preserving `FromCypherResult` that returns formatted SQL plus
translation metadata; retain the existing exported `FromCypher` behavior as a
wrapper. Route `Transaction.Query`, live-query execution, and any other
application execution through one internal
`executeTranslated` operation that formats, provisions every required leaf,
and only then sends the translated application statement. A provisioning or
translation failure sends no translated application statement; mapper refresh,
kind assertion, catalog, or leaf-provisioning SQL may already have run through
their explicitly scoped commit-safe paths and must not poison caches on failure.

Inventory every direct `Translate -> Translated -> Raw/EXPLAIN` caller.
`cmd/graphbench`, `cmd/plancorpus`, and `cmd/benchmark` may retain Raw only for
read-only EXPLAIN/capture. Validate every plan/corpus case with the pure
frontend-AST updating-clause check at ingestion, before opening a transaction,
warmup, or measurement; GraphBench must reject in `runCase` before
`measureCypher`. Recheck with that same validator immediately before any
mapper-backed `Translate` call. It rejects CREATE, SET/REMOVE,
DELETE, and DETACH DELETE without catalog SQL; this prevents translation-time
kind assertion from mutating a read-only tool. After translation, also reject
`HasMutations` before Raw as defense in depth (required-
leaf metadata alone is insufficient because update/delete need no new leaf).
Test an unknown-kind CREATE plus SET/REMOVE, DELETE, and DETACH DELETE and prove
zero SQL/catalog change, including EXPLAIN ANALYZE. `tools/dawgrun`'s translation
command is format-only, labels its output nonexecuting, and uses a read-only
existing-kind mapper; formatting an unknown mutation kind fails instead of
asserting it. Any tool that actually executes a mutation
uses normal `Transaction.Query` rather than bypassing provisioning.

Graph scoping is structural, not a text rewrite. The translator owns an explicit
`PersistentRelations` context containing schema-qualified node and edge
`CompoundIdentifier` targets derived from its graph ID. Every AST construction
that touches storage must use node/edge table-reference constructors from that
context; CTE/frame references continue to use ordinary `TableReference` values.
Pass the explicit edge target into format helpers such as
`EdgeArrayFromPathIDs`. Because the resolved targets live in the AST, every
nested formatter and SQL-valued parameter receives the same relations without
heuristic name matching. `translate.Result.GraphID` preserves/audits the
context across `Translated` and `FromCypherResult`. `DefaultGraphID == 0`
renders `public.node_0`/`public.edge_0`, never a root table. Root sequences remain
root-owned, but every allocation is fully qualified as
`pg_catalog.nextval(pg_catalog.pg_get_serial_sequence('public.node', 'id'))` or
the corresponding `public.edge` expression so search_path cannot redirect
relations or built-ins.

### Managed relation names

Retain:

- edge as the graph-partitioned root.
- edge_<graph_id> as the graph parent.
- node_<graph_id> as the node graph partition.

Add:

- edge_<graph_id>_<kind_id> as a kind leaf.
- EdgeKindPartitionTableName(graphID int32, kindID int16).
- A parser that recognizes only canonical managed leaf names and validates
  their catalog bounds; names alone never prove topology.

Supported materialized graph IDs are positive `int32`. Preflight rejects graph
rows, bounds, or effective future graph-sequence allocations outside that range,
and V2 constrains future allocation accordingly. Kind leaves support every
catalog-backed signed `int16` value, including zero and negative values. Naming
tests cover those bounds, maximum identifier length, collision resistance, and
safe PostgreSQL identifier quoting. Managed index and constraint names must use
bounded deterministic hashes rather than embedding arbitrary property text or
relying on PostgreSQL truncation.

### Managed namespace and role model

The supported V1/V2 managed namespace is `public`, recorded and validated by OID
and qualified name. Preflight rejects managed relations resolved from any other
schema with explicit remediation; it never migrates whichever relation wins a
caller `search_path`. Classifier, migrator, generated SQL, DDL, teardown,
Retriever, and catalog queries schema-qualify managed objects. SECURITY DEFINER
functions set a fixed path containing `pg_catalog` and the trusted managed
schema, exclude caller-writable persistent schemas, place `pg_temp` explicitly
last, fully qualify managed objects, and verify their expected owner/OIDs. DSN-supplied
`search_path` cannot redirect DAWGS objects.

Define and test this role matrix:

| Role | Required authority |
|---|---|
| Schema owner | Prefer NOLOGIN; owns managed objects/functions and is not a member of application roles |
| Bootstrap/migration operator | Creates or migrates schema, manages the fence, and hands off audited grants |
| Rollback preparation runtime | Migration only; distinct installation-dedicated `NOLOGIN PASSWORD NULL` role at preflight; receives one newly credentialed V1 access path only through `complete-v1-handoff`; never owns managed objects |
| Target runtime | A non-owner login role whose OID differs from the schema owner and bootstrap/migration operator session user; migration scope also requires it to differ from the preflighted preparation role and every V1 writer login; root/graph-parent DML, sequence use, read-only health/identity access, and EXECUTE on narrow provisioning/reset routines; no schema CREATE or registry DML |

Fresh bootstrap is therefore an operator-assisted two-connection workflow.
`AssertSchema`, invoked on a driver constructed with the operator pool, commits
the complete schema with a pending-handoff/smoke marker but cannot infer or
grant a target role; it does not close, replace, or change the caller-owned
pool. `dawgs-pg-migrate smoke --bootstrap` opens an operator control connection
and a separately authenticated target connection, records the target
session/current-user name and OID and requires it to differ from the schema
owner and operator/control session user. Bootstrap has no V1 writer or rollback-
preparation role and does not invent either merely to perform this check. Its
confirmation token binds the bootstrap operation/init-
receipt digest, database identity, topology, expected target identity and grant
digest, current identity epoch/rebind pointer, action, nonce, and expiry. One
operator transaction consumes the token,
applies grants, stores target identity, and appends `bootstrap_handoff`. It then
runs the explicit-sentinel insert/read/delete smoke in one target transaction
without sequence allocation, or the approved empty-schema fallback. A final
operator transaction appends the attempt's `smoke_result` and atomically updates
the layout status/current-receipt digest. Unknown operator commits are resolved
from marker, receipt, token, and grant digests; an ambiguous target transaction
is safely rerun because commit and rollback both leave no sentinel. A consumed
token can only resume/report the same operation/target/attempt. Repair after a
failed attempt requires a new nonce. Reports regenerate solely from durable
receipts, so file failure never repeats grants or DML.

Only after a passed/fallback receipt does the caller close the operator driver/
pool and construct the unchanged `pg.NewDriver` signature with the target pool.
On an uninitialized database `AssertSchema` is an operator bootstrap API, not a
method that can discover or switch credentials; on an accepted database the
target role reaches only the narrow reconciliation routines described below.
Ordinary runtime operations stay blocked until handoff. The runtime never owns
managed relations or the schema. Because
lazy `WithGraph`, `AssertGraph`, `AssertSchema`, dynamic kinds, logical
constraints, index repair, and reset remain supported, WP0 defines narrowly
scoped owner routines for each operation. If policy forbids that complete
restricted API, revise the lifecycle contract before implementation. Inventory
and control inherited membership, PUBLIC grants, `pg_default_acl`, object
ownership, and future-object defaults so a retired V1 writer cannot regain
access when a leaf is created later.

### Canonical application schema

Canonicalization is deterministic and rejects ambiguity:

- Group `Schema.Graphs` by exact graph name; duplicate definitions must be
  identical after kind sorting/deduplication or assertion fails.
- Resolve a name-only `DefaultGraph` to the matching full `Schema.Graphs`
  declaration. If both contain definitions, their normalized declarations must
  match. If no matching entry exists, the complete `DefaultGraph` declaration
  is canonical.
- Apply the same rule to name-only `WithGraph` values and never replace a known
  full declaration with an empty one.
- Sort and deduplicate kind declarations; conflicting graph or constraint
  declarations return a stable schema error before any DDL.

### Topology model

Replace the assumption that `GraphPartitions.Edge` is a heap without changing
the type of that exported field:

~~~go
type EdgeKindPartition struct {
    KindID int16
    Name   string
}

type GraphPartitions struct {
    Node               GraphPartition
    Edge               GraphPartition // stable graph parent
    EdgeKindPartitions map[int16]EdgeKindPartition
}
~~~

Changing `Edge` to another exported type would break assignments and composite
literals. WP0 inventories exported symbols in `drivers/pg`, `drivers/pg/model`,
`drivers/pg/query`, and PostgreSQL translation packages, checks unkeyed
downstream literals when adding fields, and preserves existing operations such
as `CreatePartitionTable` with deprecated wrappers where safe. Every accepted
source break has an explicit release/semver decision. Keep logical graph
property constraints separate from physical parent and attached-leaf indexes. A
registry-backed logical constraint is not represented as a unique edge index.

### Catalog inspection

Stop parsing complete CREATE INDEX text from pg_indexes. Add catalog queries
over pg_class, pg_namespace, pg_partitioned_table, pg_inherits, pg_index,
pg_attribute, pg_constraint, pg_collation, and pg_get_expr.

Catalog descriptors must capture:

- Relation OID, schema, owner, relkind, persistence, and parent OID.
- Partition strategy, key column and type, normalized bound, and DEFAULT status.
- Index parentage, validity, readiness, uniqueness, access method, key
  expressions, opclasses, included columns, predicate, and collation.
- Managed constraints, triggers, comments, grants, sequence ownership, and
  supported external dependencies required by migration.

The classifier and reconciliation code consume descriptors. They never infer
correctness from a relation name or marker alone.

### Durable layout state

Create a singleton layout record and per-migration/per-graph progress records.
Final SQL names and column widths are locked by the state-format ADR in WP0,
but the baseline is:

~~~text
dawgs_schema_layout
  singleton key
  layout_version
  state
  active_migration_id
  current_rollback_migration_id
  accepted_migration_id
  bootstrap_operation_id
  current_database_identity
  identity_epoch
  current_rebind_receipt_digest
  current_legacy_cleanup_map_digest
  manifest_sha256
  validation_digest
  started_at
  accepted_at
  rollback_cutoff_at
  post_acceptance_smoke_status
  post_acceptance_smoke_receipt_digest
  target_role_oid
  target_role_name
  source_revision

dawgs_schema_migration
  migration_id
  source_database_identity
  source_layout_fingerprint
  manifest_sha256
  preflight_digest
  fence_metadata
  rollback_catalog_snapshot
  legacy_cleanup_map_digest
  global_phase
  global_copy_strategy
  checksum_encoder_version
  global_high_water_graph_id
  global_high_water_edge_id
  global_rows_copied
  global_chunk_digest
  capacity_attestation_sha256
  pre_migration_backup_evidence_sha256
  rollback_handoff_status
  rollback_handoff_receipt_digest
  validation_digest
  created_at
  updated_at

dawgs_schema_migration_graph
  migration_id
  graph_id
  phase
  copy_strategy
  checksum_encoder_version
  high_water_edge_id
  rows_copied
  last_chunk_digest
  staging_relation
  legacy_relation
  source_count
  target_count
  per_kind_counts
  source_checksum
  target_checksum
  catalog_snapshot
  updated_at

dawgs_edge_kind_leaf
  graph_id
  kind_id
  provenance
  lifecycle_state
  created_at
  PRIMARY KEY (graph_id, kind_id)

dawgs_schema_receipt
  scope_kind
  scope_id
  receipt_type
  attempt_id
  receipt_version
  receipt_digest
  canonical_facts
  confirmation_token_digest
  created_at
  PRIMARY KEY (scope_kind, scope_id, receipt_type, attempt_id)
~~~

Receipts are append-only. `scope_kind` is exactly `bootstrap` or `migration`,
and `scope_id` is the corresponding bootstrap operation UUID or migration UUID;
never use a null/sentinel migration ID. WP0 assigns every receipt type an exact
attempt-ID rule: only fixed initialization uses its named no-token singleton;
every token-controlled mutation uses the consumed confirmation nonce; phase
receipts produced by that mutation use the same nonce under their distinct
receipt types; and validation generates its attempt UUID before connecting and
persists it with the result. Read-only status/health reports have generated
report IDs but do not masquerade as durable receipts. Thus acceptance,
rollback, cleanup, bootstrap handoff, smoke, V1 handoff, and repeatable
`rebind_restore` retries have unambiguous idempotency keys and failed/new
attempts remain auditable. A smoke result binds the exact handoff-receipt digest
and target facts for its attempt. The layout's current smoke-, rebind-, and
legacy-cleanup-map digests, its validation digest, and the migration row's
current rollback-handoff-receipt digest select authoritative results;
timestamps never choose a winner. Rebind atomically
increments `identity_epoch` and updates current database identity, target
binding, rebind pointer, and smoke pending/current-smoke pointer. A rolled-back
rebind also resets handoff to pending and clears its current handoff pointer;
the next handoff atomically records a nonce/epoch-bound receipt, grants access,
marks passed, and points to that receipt. `current_rollback_migration_id` is nonnull
only for the authoritative rolled-back V1 scope: structural rollback sets it;
a later migration start atomically replaces it with `active_migration_id`; and
acceptance clears both while setting `accepted_migration_id`. Exactly one of
bootstrap operation, active migration, current rollback migration, or accepted
migration identifies the authoritative operation scope for any non-plain-V1
state. Historical migration rows never compete by timestamp. The bootstrap ID
remains as provenance after smoke passes and is mutually exclusive with every
migration-scope pointer.

Fresh `AssertSchema` generates the bootstrap operation ID before its owner
transaction and atomically creates complete V2, records `v2_accepted` with
pending handoff/smoke and null target identity, stores the current database
identity at epoch zero with null rebind/current-rollback pointers, points the
legacy-cleanup map at a canonical empty map after proving no legacy object
exists, and inserts the `bootstrap_initialized` receipt. Canonical
initialization facts include stable and current database identity, normalized
application-schema digest, topology fingerprint, source/tool revision,
operator identity, empty cleanup-map digest, and pending status. The
classifier requires that exact marker/receipt/catalog shape; a pending bootstrap
without its initialization receipt is drift.

dawgs_edge_kind_leaf is the durable desired-topology registry. Bootstrap and
migration insert rows for declared-empty and stored kinds; dynamic ensure adds
its row in the same transaction that creates the leaf. Later schema omission
does not delete it. Graph data deletion marks entries retained rather than
cascading them away because the physical empty partitions also remain.
Provenance is an additive flag set, not a single mutually exclusive value: the
same leaf may be stored, declared, migrated, dynamic, and later retained.
Reassertion adds supported facts and omission never clears them. Provenance does
not change routing semantics.

State values are:

| State | Runtime behavior | Migrator behavior |
|---|---|---|
| uninitialized | Only AssertSchema may bootstrap V2 | Report no migration needed |
| v1 | Reject in target runtime | Inspect, preflight, migrate |
| v1_to_v2_in_progress | Reject with resume/recovery guidance | Resume or pre-acceptance rollback |
| v2_pending_acceptance | Reject all application writes | Validate, accept, or pre-acceptance rollback |
| v2_accepted | Normal operation only after post-acceptance smoke/fallback passed | Smoke, health, validate, recorded cleanup |
| drifted | Fail closed | Diagnose; never auto-repair hard drift |
| unrecognized | Fail closed | Diagnose unsupported state |

`v2_accepted` with smoke pending/failed is a recognized but nonoperational
substate. A null target-role identity is allowed only for an exact fresh-
bootstrap pending-handoff shape; migration acceptance records the authenticated
target identity atomically. Any other missing/mismatched role or smoke fact is
drift. Only passed/fallback is normal runtime.

Marker state is authoritative only when catalog topology agrees. An unmarked V2
database or any mixed hierarchy is drift, not inferred acceptance.

An accepted marker may be accompanied by repairable reconciliation findings
without becoming a new durable layout state. A leaf expected by
dawgs_edge_kind_leaf is repairable only when it is wholly absent, the identity
and property registries show no row or claim for that graph/kind, no detached
lookalike or dependent object exists, and every other managed invariant is
healthy. dawgs.Open may return the accepted driver in that case; explicit
AssertSchema and write-side ensure repair the absence under lock, while health
reports it. The same repairable-finding policy applies to a missing or invalid
exact canonical managed index with a known dependency set. Wrong parent/bound,
DEFAULT/duplicate bounds, unexpected leaves, missing enforcement, registry
evidence of lost rows, or unknown dependencies remain hard drift and fail
early. This makes empty and dynamic expected leaves discoverable without
requiring a manifest on every runtime open.

An unmarked V1 database is recognized only when its critical structural
fingerprint matches the exact frozen V1 contract. The classifier may inventory
documented V1 variations such as owners, ACLs, comments, managed property
indexes, and supported dependencies without rejecting them. A partial managed
schema or a collision with a managed name is drift. Unrelated database objects
do not prevent fresh initialization.

`v1_rolled_back` is the structural V1 shape plus an exact allowlist of inert
control state: the singleton in state `v1`, completed rollback history and
receipts, and no V2 data, enforcement, registry, staging, or attached V2
objects. While `rollback_handoff_status=pending`, the writer fence remains
active and the preparation client refuses operation with handoff guidance. It
accepts the shape only after the final grant/status transaction records
`passed`. The migrator classifies both substates deterministically. Any
additional V2-era object is fenced drift.

### Runtime gate

Keep the pg registration and pg.NewDriver signature stable.

The registered constructor in drivers/pg/pg.go should classify early so
dawgs.Open returns a typed and actionable migration error. Direct callers of
pg.NewDriver bypass registration, and `Driver` currently exposes promoted
SchemaManager/KindMapper paths, so layout enforcement lives at the lowest
shared PostgreSQL access layer as well as the constructor. No database-touching
value can bypass it through embedding, `KindMapper`, or a direct constructor.
Cover at least:

- ReadTransaction and WriteTransaction.
- BatchOperation.
- SetDefaultGraph and schema assertion methods.
- Run and raw query entry points.
- FetchKinds and RefreshKinds.
- AssertKinds and every exported/promoted SchemaManager or KindMapper method.
- DeleteNodesByKinds and DeleteRelationshipsByKinds.
- OptimizeStorage and WipeGraph.
- Export or tooling paths that still construct the driver directly.

Operational acceptance is both state- and identity-bound. Before any accepted-
V2 operation, the constructor/readiness helper and lowest shared acquisition
path must resolve the WP0 current-identity-provider contract and require both
its verified cluster/resource, restore-incarnation, and database OID/name tuple
equals the layout's current binding/identity epoch and
`session_user = current_user =` the recorded target role OID/name. A same-
cluster database clone, restored database, role name supplied as configuration,
inherited membership, startup `SET ROLE`, or operator/migration connection does
not pass merely because marker and grants look valid. Recheck every newly
acquired or reconnected physical session and at each transaction/raw-operation
boundary; do not let a pool-level cached success outlive backend replacement,
restore, identity-epoch change, or persistent role change. Constructor, health,
and tools return stable `restore_rebind_required` on a database-identity
mismatch before application SQL. For an externally supplied pool, wrap
acquisition and preserve caller hooks rather than assuming homogeneous
credentials. Reject raw session-authorization/role-changing statements that
could invalidate the identity invariant. The migrator remains raw-pgx and never
passes through this application gate.

Preserve the existing `NewDriver` signature by making it select only the built-
in self-managed identity provider or a provider explicitly bound to that pool
through a new additive pool/driver configuration API. The registered
constructor and externally supplied pools use the same binding. Managed
profiles configure a verifying provider before driver construction; an unbound
pool where built-in facts are unavailable fails with
`current_identity_unavailable`. Provider results are reverified on connection
establishment and at operation boundaries, with bounded caching keyed by the
external incarnation and identity epoch, never by a process-global success bit.

Today `pg.NewPool` installs `AfterPooledConnectionEstablished`, which calls
`LoadType` for managed composites before `dawgs.Open`, and its release hook
discards connections without those types. Refactor both hooks as part of the
gate. Pool construction and prebootstrap operator use run no DAWGS managed-type
query and may retain raw connections. After a guarded acquisition proves
operational state and exact target identity, lazily load and register every
composite by schema-qualified `public.<type>` on that same physical connection
before application SQL. Operational release retains only sessions marked as
identity-checked for the current layout fingerprint with all required types;
prebootstrap release does not churn raw operator sessions. Reconnect, restore,
or fingerprint change invalidates that mark and reloads types. Compose and
preserve caller-supplied pgx hooks rather than overwriting them. For externally
supplied pools that did not use `pg.NewPool`, the guarded acquisition path—not
the hook—is authoritative. After operator bootstrap, reset any bootstrap-local
type/cache state, but do not activate that pool as runtime.
Keep the exported hook function signatures for source compatibility; document
direct invocation as the strict operational-target behavior. `NewPool` composes
separate internal readiness-aware wrappers so its uninitialized operator path
does not misuse the strict release helper.

Use a refreshable guarded state machine, not `sync.Once`: transient connection,
timeout, and cancellation failures must be retryable, while an accepted
fingerprint change forces reclassification. Bootstrap uses a role-verified
operator capability scoped to `AssertSchema` on an uninitialized
database. Tests enumerate every exported database-touching method and prove no
method sends an application statement when its gate rejects layout or identity;
only the bounded classifier/identity queries may run.

An uninitialized database is accepted only as a bootstrap candidate: the
constructor may return a driver, but every operation except AssertSchema returns
schema_uninitialized. Operator-authenticated AssertSchema is the only driver API permitted
to move that database to v2_accepted. It creates the complete V2 schema and
accepted marker with smoke pending in one transaction and returns pending; the
two-connection bootstrap continuation completes the target-role handoff/smoke.
After that commit, the still-open operator driver is nonoperational even after
smoke passes because its session identity is not the bound target. It rejects
V1, in-progress, pending-acceptance, drifted, and unrecognized layouts. CREATE
TABLE IF NOT EXISTS is not a migration strategy and must not reshape V1.

Define typed errors that work with errors.Is and carry stable codes:

- migration_required.
- migration_in_progress.
- pending_acceptance.
- schema_uninitialized.
- layout_drift.
- unsupported_layout.
- leaf_provision_failed.
- retry_schema_operation.
- acceptance_smoke_pending.
- rollback_handoff_pending.
- wrong_runtime_role.
- restore_rebind_required.

Error text must name dawgs-pg-migrate and the relevant read-only status or
recovery command, but must not expose a DSN or credential.

### Layout and schema locks

Graph-name and kind-name definition are pre-ID phases. Canonically order exact
UTF-8 name bytes and serialize them in dedicated advisory-lock namespaces, or
use a WP0-proven atomic insert/reselect retry protocol. Do not use the current
`existing UNION inserted` CTE as a concurrency primitive: after waiting on a
concurrent insert, its statement snapshot can still return no row. Once IDs
exist, use this numeric acquisition order:

1. Migration or layout lock.
2. Graph schema lock.
3. Kind locks sorted by numeric kind ID.
4. Property-constraint claim locks sorted by constraint ID.

Every path re-reads the catalog after acquiring its lock. Cache presence is
never proof that an object still exists. Tests must run conflicting operations
from two independent pools and prove bounded completion without deadlock.

WP0 compares direct `CREATE TABLE ... PARTITION OF` with create-standalone plus
validated-check-and-`ATTACH PARTITION` on PostgreSQL 16 and 18 and adopts the
weakest-lock choreography that satisfies atomic visibility and trigger/index
inheritance. The lock budget includes the graph parent and every affected index,
not only the new leaf.

For dynamic leaf creation by a non-owner application role, prefer a narrowly
audited SECURITY DEFINER function:

- Owned by the schema owner.
- Fixed safe search_path.
- Numeric graph and kind arguments only.
- Catalog validation and advisory locking inside the function.
- Identifier construction with server-side quoting.
- Schema-qualified managed relation references; never resolve an attacker-owned
  object through the caller's search_path.
- EXECUTE revoked from PUBLIC and granted only to the runtime role.

WP0 must validate this privilege design on PostgreSQL 16 and 18. If policy
forbids SECURITY DEFINER, migration preflight must require and verify an
equally narrow operator-provisioned mechanism; do not silently grant general
CREATE privileges.

### Cache and transaction rules

Refactor SchemaManager before enabling dynamic leaves:

- Do not hold the cache mutex across database or DDL work.
- Do not publish graph, kind, or leaf entries before the creating transaction
  commits.
- On rollback or ambiguous DDL failure, discard pending entries and evict
  affected cached topology.
- Return copies of mutable maps. Do not return a live kind map after releasing
  the mutex.
- Clear both forward and reverse kind maps during refresh.
- Resolve name-only DefaultGraph and WithGraph values against canonical
  graph.Schema definitions so declared edge kinds are not lost.
- Keep an explicit reconciliation path that re-reads catalogs even when a
  graph is cached. AssertSchema always uses it.
- Keep parse/query caches independent from schema caches.

For an already committed graph, provision a new dynamic leaf in a short,
dedicated schema transaction before the caller transaction has accessed the
edge graph parent. For a graph being created in the caller's transaction,
create declared leaves in that same transaction. Track prior edge-parent
access, not only edge DML; a read can retain a conflicting lock, and prior
arbitrary `Raw` SQL is conservatively unsafe. If a missing leaf is discovered
after such access, return `retry_schema_operation` before starting external DDL.
The caller retries its whole delegate from a fresh transaction; the driver never
automatically replays arbitrary user transaction code. Existing leaves remain
usable.

### Edge identity registry

Prototype and, if it passes WP0, implement this baseline:

~~~text
edge_identity
  graph_id
  edge_id
  kind_id
  PRIMARY KEY (graph_id, edge_id)
  FOREIGN KEY (edge_id, graph_id, kind_id)
    REFERENCES edge (id, graph_id, kind_id)
    ON DELETE CASCADE
~~~

The V2 edge root primary key becomes (id, graph_id, kind_id), as required by
PostgreSQL partitioned uniqueness. The combined AFTER INSERT enforcement
trigger claims (graph_id, edge_id) before property values. A duplicate ID in
another kind fails the statement and rolls back. AFTER INSERT is preferred over
a pre-insert claim because endpoint ON CONFLICT DO UPDATE may divert an
attempted insert; WP0 must prove the exact behavior for single-row, multi-row,
batch, and concurrent statements.

A BEFORE UPDATE trigger rejects changes to id, graph_id, or kind_id. Registry
rows disappear transactionally through the edge foreign key and graph cascade.
Fresh V2 bootstrap may create this foreign key immediately. The V1 migrator
must defer it until all graph partitions have been replaced because a populated
reference to the root blocks DETACH PARTITION; migration uses the registry
primary key and claim/delete triggers in the interim.
TRUNCATE does not run ordinary DELETE triggers. The candidate foreign key can
also prevent truncating only an edge child while an unpartitioned registry
references the root. WP0 must choose and prove one safe reset design:

- Global WipeGraph truncates node, edge, identity claims, and property claims
  together in one statement while retaining schema/constraint metadata.
- A graph-scoped benchmark or corpus reset uses a managed row-delete routine.
- Or the registries use aligned graph partitions and a proven complete
  graph-scoped truncate set.

TRUNCATE CASCADE is not acceptable because it can erase unrelated graph or
schema state. The chosen design must preserve WipeGraph's atomic retain
delegate and rollback behavior.

The prototype must cover endpoint conflict updates, explicit IDs, COPY when
exposed by DAWGS, raw direct-leaf COPY regardless, deletes, cascades, rollback,
concurrent cross-kind claims, multi-statement transactions, sequence allocation,
and write amplification. Force reversed-order multi-row statements claiming the
same IDs/values. WP0 must select deterministic statement-level sorted claiming
or a bounded retryable SQLSTATE contract; row-trigger lock order that can
deadlock indefinitely is not acceptable.
If the baseline fails, the replacement still must enforce the same invariant
inside PostgreSQL; sequence-only enforcement is not acceptable.

### Edge property uniqueness registry

Prototype and implement graph-wide cross-kind uniqueness using:

~~~text
edge_property_constraint
  constraint_id
  graph_id
  logical_name
  property_name
  constraint_type
  collation
  lifecycle_state
  PRIMARY KEY (graph_id, constraint_id)
  UNIQUE (graph_id, property_name)
  FOREIGN KEY (graph_id) REFERENCES graph (id) ON DELETE CASCADE

edge_property_unique_value
  graph_id
  constraint_id
  property_value text
  edge_id
  PRIMARY KEY (graph_id, constraint_id, property_value)
  FOREIGN KEY (graph_id, constraint_id)
    REFERENCES edge_property_constraint (graph_id, constraint_id)
    ON DELETE CASCADE
  FOREIGN KEY (graph_id, edge_id)
    REFERENCES edge_identity (graph_id, edge_id)
    ON DELETE CASCADE
~~~

The database-default text collation must match the legacy expression-index
collation. Missing properties and JSON null become SQL NULL and create no
claim. Empty text creates a claim and conflicts. Do not replace the text key
with a hash-only key.

WP0 locks one V1-parity logical edge uniqueness constraint per
`(graph_id, property_name)`; `logical_name` is deterministic diagnostic metadata
because current physical naming derives from the field rather than reliably
using `Constraint.Name`. Reassertion reuses `constraint_id`, rejects
type/collation drift, and drop/re-add changes lifecycle instead of allocating a
second active constraint. Property identifiers are passed as data or quoted by
trusted server-side identifier functions, never concatenated into SQL.

Characterization covers JSON numbers, booleans, arrays, objects, oversized text,
missing, JSON null, SQL NULL, empty strings, and default/nondefault collations
under legacy `->>` extraction. The registry must reproduce the accepted V1
result or explicitly reject an already-rejected shape; it must not invent a new
normalization.

Resolve kind_id through edge_identity rather than duplicating an unconstrained
kind value in the property claim. Persist graph.Constraint.Type and collation
so reassertion detects semantic changes. Characterize non-B-tree constraint
types in WP0 and preserve their current accepted/rejected behavior; do not
silently reinterpret one as B-tree uniqueness.

Trigger behavior:

- One combined AFTER INSERT claim function creates identity first, then each
  non-null property claim in deterministic constraint-ID/value order.
- BEFORE UPDATE rejects routing-key changes and claims new property values
  before releasing obsolete values.
- DELETE and graph cascade remove claims transactionally.
- Constraint addition acquires the graph advisory lock and a PostgreSQL
  relation lock on the graph parent and descendants that conflicts with raw
  INSERT/UPDATE/DELETE. In one transaction it installs enforcement, backfills,
  validates, and publishes only after rows and claims agree. WP0 selects the
  weakest proven lock mode; advisory locking alone is insufficient because raw
  SQL does not honor it.
- Constraint removal takes the same recursive DML-excluding relation lock,
  marks the metadata row inactive, disables enforcement, and deletes only its
  claims atomically. Trigger lookup filters active rows. The tombstone preserves
  the natural key and `constraint_id`; re-add validates semantics and reactivates
  that row after a fresh backfill instead of allocating a duplicate.

Do not rely on trigger creation order: PostgreSQL orders same-event triggers by
name. Use the combined function or an equivalently explicit ordering contract,
and verify it on root inserts, direct-leaf inserts, newly created/attached
leaves, and raw direct-leaf COPY. Registry trigger/provisioning functions
run as narrowly owned SECURITY DEFINER functions with a fixed safe search_path.
The application role receives EXECUTE but no direct INSERT, UPDATE, DELETE, or
TRUNCATE privilege on identity, constraint, or claim registries.

The combined mechanism must also prevent reversed-order multi-row claim
deadlocks. Map uniqueness, deadlock/retry, immutable-key, and missing-leaf
failures to documented stable SQLSTATE/driver error codes, and test the mapping
through direct, batch, Cypher, raw-root, raw-leaf, and COPY paths.

Relationship identity-property upsert currently relies on ON CONFLICT inference
against a graph-level expression unique index. That cannot survive V2. Replace
it with one registry-aware SQL routine that:

1. Validates that requested identity properties match a declared logical
   constraint.
2. Locks and looks up the registry claim.
3. Updates the existing edge while preserving endpoints and kind when found.
4. Otherwise inserts, handles a concurrent claim race, then retries lookup and
   update as required.
5. Preserves the existing endpoint ON CONFLICT path when there are no property
   identity fields.
6. Reproduces V1 cross-kind conflicts, returned IDs, and update semantics.

A SELECT FOR UPDATE lookup does not lock an absent claim. The routine must use
the WP0-proven PL/pgSQL unique-violation subtransaction/retry, or a
transaction-level advisory value lock followed by a claim recheck, while the
registry primary key remains the authoritative arbiter. Tests must force the
absent-row race; a check-then-insert without retry is not acceptable.

graph.Constraint currently describes one property field, while
RelationshipUpdate accepts a list of identity fields. Characterize the exact V1
behavior for zero, one, and several identity fields before designing the
routine. Preserve valid one-field behavior and preserve existing rejection for
unsupported combinations; do not silently invent composite relationship
constraints as part of this migration.

No active V2 schema or migration may ship until characterization tests prove
equivalence for the current edge constraint API. The repository currently has
little or no edge-constraint integration coverage, so this is a critical-path
deliverable, not a cleanup task.

### Graph-safe SQL helpers

Fix correctness holes as part of the target schema:

- delete_node_edges() must join deleted nodes on graph_id and endpoint ID.
- A statement trigger on the root does not cover a statement issued directly
  against `node_<graph_id>`. WP0 must select and prove on PostgreSQL 16/18 either
  an equivalent reconciled statement trigger on every node partition or routing
  all generated deletes through the root with an explicit graph predicate.
- start_node(edgeComposite) and end_node(edgeComposite) cannot identify a graph.
  Change their signatures to accept target_graph_id and pass that value from
  the translator. Explicitly drop the obsolete one-argument overloads during
  V2 bootstrap/migration and in schema teardown so graph-unsafe helpers cannot
  survive CREATE OR REPLACE under another signature.
- Thread target_graph_id through `nodes_to_path` and `ordered_edges_to_path`;
  make `edges_to_path` graph-aware or retire it. Audit `swap_forward_front` and
  `swap_backward_front`: thread graph ID through their ASP harness overloads or
  remove and benchmark the unsafe dead-end-pruning optimization. There is no
  current `ordered_edge_ids_to_path` helper. Mark every read-only helper that
  reads persistent tables `STABLE`, not `IMMUTABLE`; swap/harness functions that
  mutate temporary tables remain `VOLATILE`. Enumerate every old/new signature
  explicitly in the canonical down definitions.
- ID-only hydration joins the input with ordinality to `edge_identity` on
  `(graph_id, edge_id)`, then joins `edge_<graph_id>` on both `edge_id` and
  `kind_id`. Preserve duplicates and ordinality. Grant narrowly scoped read-only
  access to the identity mapping or expose a fixed owner resolver; do not leave
  the privilege implicit.
- Every generated ID-only edge update or delete includes the transaction graph
  and, when available, kind_id. An identity-registry lookup supplies the kind
  when only an ID is available.
- Every direct or batch ID-only node delete also includes the transaction graph.
  Otherwise colliding graph-local node IDs can delete nodes in another graph
  before the corrected edge cascade even runs.

Do not add endpoint foreign keys as a substitute for graph-safe cascade.
Existing behavior intentionally permits some dangling endpoints and must stay
covered by the shared semantic suite.

## Work packages

### WP0: Contracts, frozen V1 evidence, prototypes, and baselines

Purpose: remove semantic and operational uncertainty before irreversible DDL is
activated.

Deliverables:

1. Create `docs/adr/README.md` and numbered ADRs for:
   - One-driver V2-only lineage and retirement.
   - Layout state machine and report/manifest format versions.
   - Graph-local edge identity and immutable routing keys.
   - Graph-wide edge property uniqueness and registry-backed upsert.
   - Writer fence, acceptance cutoff, and rollback policy.
   - Supported PostgreSQL versions, including the PostgreSQL 17 disposition,
     topology limits, and dependency policy.
   - Canonical `public` namespace, database identity, role/default-ACL model,
     SECURITY DEFINER threat model, bootstrap/runtime handoff, writer-probe
     credential continuity, and cluster-global role coordination.
   - Graph-scoped SQL rendering, translated-result metadata transport, helper
     signatures, and exported-API compatibility.
   - Performance/rehearsal protocol, capacity/backup attestations, and
     observability/privacy fields.
2. Copy the exact final V1 schema into
   drivers/pg/migrate/testdata/v1_schema.sql. Never regenerate it from mutable
   V2 DDL. Freeze deterministic seed/catalog metadata artifacts beside it and
   record expected SHA-256 values for all of them.
3. Record the final V1-compatible DAWGS module tag/source archive and checksums,
   plus a version-pinned compatibility client used for rehearsal. DAWGS is a Go
   library, so the fleet ledger separately records each consuming application's
   deployable binary/image.
4. Add V1 characterization tests for:
   - Edge property constraints and identity-property upsert.
   - Same endpoint and same kind conflict behavior.
   - Same endpoint across different kinds.
   - Missing, JSON-null, empty-string, and collation behavior.
   - Zero, one, and several RelationshipUpdate identity fields against the
     current single-field graph.Constraint model.
   - Explicit IDs, ID-only updates/deletes, path hydration, and graph isolation.
   - Constraint natural naming and property extraction for numeric, boolean,
     array, object, oversized, quoted-name, null, empty, and collated values.
5. Prototype the identity and property registries on PostgreSQL 16 and 18,
   including concurrent statements, rollback, the V1-to-V2 root key swap,
   populated graph-partition detach/attach while registry claims exist, and
   global and graph-scoped reset behavior. If the foreign-key baseline prevents
   atomic cutover or safe reset, select the trigger or aligned-partition
   equivalent before locking the V2 DDL.
   The root-key prototype must not assume ADD CONSTRAINT USING INDEX works on a
   partitioned table. Baseline the transactional DROP of the V1 primary key and
   ADD PRIMARY KEY (id, graph_id, kind_id), including recursive index build,
   lock time, temporary disk, and rollback; use another choreography only after
   it is proven on both versions.
6. Prototype direct partition creation versus standalone-create plus validated
   `ATTACH PARTITION`, node child statement-trigger propagation, and choose the
   bounded-lock graph-safe mechanisms.
7. Prototype the complete restricted runtime schema API: graph/kind definition,
   graph/node/edge parent and leaf creation, managed index repair, logical
   constraint add/remove, health, and reset. Test hostile search paths, PUBLIC,
   inherited/default grants, future leaves, and direct-leaf DML/COPY.
8. Capture V1 GraphBench, plan corpus, relation size, catalog, and Retriever
   baselines on representative graph/kind distributions. Before freezing the
   preparation release, build and checksum a V1-compatible benchmark artifact
   (or immutable read-only adapter around it) that emits the same versioned
   comparison envelope and workload contract required by target benchdiff:
   layout version/fingerprint, PostgreSQL facts, dataset/seed, graph/kind/row
   counts, constraint cardinality, benchmark protocol and case IDs, logical
   operation/query semantics, parameter distributions, warmup/sample schedule,
   concurrency/timeouts, metric units, and case-manifest digest. Where physical
   SQL must differ, freeze an explicit versioned V1/V2 adapter mapping to the
   same logical case. A legacy report missing or disagreeing on those fields is
   not an eligible baseline.
9. Define versioned JSON schemas for canonical manifests; capacity and pre-
   migration-backup evidence; preparation-credential plans; writer-probe files;
   drain, external-writer, native-role/provider-retirement, V1-handoff,
   restore/rebind, and cleanup evidence; preflight/progress reports;
   generalized initialization, validation, acceptance, smoke, rollback,
   handoff, rebind, and cleanup receipts; status/health reports; and fleet
   records.
10. Define initial supported limits and gates for graph count, kinds per graph,
    total leaves, row skew, planning time, DDL lock budget, migration disk/WAL,
    registry contention, and write overhead.
11. Inventory external dependencies that may reference DAWGS relations. The
    initial policy must explicitly preserve or reject views, inbound foreign
    keys, user triggers, RLS, rules, publications, replica identity changes,
    and extension-managed objects.
    Reject inbound logical-replication/subscription writers unless an
    `ENABLE ALWAYS` enforcement design is proven; the application role must not
    be able to bypass triggers through `session_replication_role`.
12. Establish the PostgreSQL 16/18 CI matrix before landing V2 DDL. Each job
    asserts SHOW server_version_num so an image/tag mistake cannot silently
    collapse the matrix to one server version.
13. Force concurrent pre-ID graph/kind creation and reversed-order multi-row
    identity/property claims; select the serialization/retry protocol and stable
    SQLSTATE mapping.
14. Inventory source compatibility of exported PostgreSQL symbols and lock the
    graph-schema canonicalization/error contract.

Tests and evidence:

- Race tests for registry claims.
- Exact V1 observable behavior snapshots.
- PostgreSQL 16/18 DDL and trigger results.
- Before/after prototype write-throughput and latency report.
- Catalog snapshot examples with owners, ACLs, comments, sequences, and
  supported/unsupported dependencies.

Exit gate:

- All locked contracts have an executable characterization.
- Identity and property mechanisms pass concurrency and rollback tests.
- The selected privilege and cascade designs work on PostgreSQL 16 and 18.
- Performance and topology budgets are approved.
- The frozen V1 fixture and baseline artifacts are checksummed.

If a prototype fails, revise the mechanism and repeat WP0. Do not weaken a
locked contract or activate V2 to keep the schedule.

### WP1: Catalog facts, layout classifier, and preparation release

Purpose: establish fail-closed state recognition before any V2 activation.

Implementation:

1. Add drivers/pg/internal/catalog with typed catalog queries and fixtures.
2. Add drivers/pg/internal/layout with state, topology, lock, and typed-error
   definitions.
3. Implement read-only classification for uninitialized, recognized V1,
   migration states, accepted V2, drifted, and unrecognized layouts.
4. Compare markers with structural facts. Never trust a marker alone.
5. Implement topology fingerprints from stable structural data, excluding OIDs
   and other values that legitimately differ after restore.
6. Replace or begin replacing pg_indexes DDL regex parsing in Query
   reconciliation. Comparing a normalized expression from pg_get_expr is
   acceptable; parsing whole DDL is not.
7. Correct the existing sqlSelectKindID embedded asset binding while this SQL
   registry is under test.
8. Add a read-only status API used by both the runtime and migrator.
9. Add V2-refusal logic to the preparation V1 release. It continues to support
   recognized V1, including the exact `v1_rolled_back` inert-control shape only
   when handoff passed; it refuses handoff-pending, V2, and mixed states.
10. Publish the V1 deprecation and migration deadline with the preparation
    artifact.
11. Expose the public pg sentinel/code/detail errors and optional health-provider
    contract without changing `graph.Database`.

Tests:

- Empty database with unrelated objects.
- Partial managed schema and managed-name collisions.
- Exact frozen V1 plus supported metadata variations.
- Exact rolled-back V1 plus allowlisted control history, and one test for each
  forbidden residual V2 object; handoff pending remains fenced/refused and
  handoff passed is accepted.
- V1 with unsupported dependencies.
- Correct V2, unmarked V2, marker/topology disagreement, missing leaf, wrong
  bound, DEFAULT leaf, duplicate bound, invalid attached index, and mixed V1/V2.
- Stable errors.Is behavior, status codes, and secret redaction.
- Preparation runtime accepts V1 and refuses V2.

Exit gate:

- Classification is mutation-free and deterministic on PostgreSQL 16 and 18.
- Every unsupported state fails closed with a recovery-oriented error.
- The preparation release and its exact artifact are available before V2
  activation.

### WP2: V2 physical schema and catalog-backed topology

Purpose: make the complete V2 schema constructible and inspectable without yet
flipping the generally available runtime.

Implementation:

1. Move and update the canonical V2 up definitions under internal/schema for:
   - edge partitioned by graph_id.
   - edge primary key (id, graph_id, kind_id).
   - Root endpoint uniqueness exactly
     `(start_id, end_id, kind_id, graph_id)` with its attached graph/leaf index
     hierarchy and catalog reconciliation.
   - V2 metadata and registry relations.
   - Durable graph/kind leaf expectations.
   - identity/property enforcement and routing-key immutability.
   - graph-catalog-row immutability and target-role DELETE denial.
   - graph-aware helpers and cascade behavior.
   - base partitioned indexes retained for the correctness release.
2. Move and update the canonical V2 down definitions under internal/schema to
   drop triggers/functions before registry relations
   and to remove V2 metadata only during explicit destructive teardown.
   Migration rollback must not use the generic canonical down routine.
3. Add typed DDL formatters for:
   - Node graph partition.
   - Edge graph parent partitioned by LIST(kind_id).
   - Edge kind leaf with an exact kind_id bound.
4. Do not rely on IF NOT EXISTS for topology. Recheck the catalog under the
   correct advisory lock and either create the exact missing managed object or
   reject hard drift.
5. Add the kind-leaf model and naming/parser helpers.
6. Add catalog-backed discovery for graph parentage, leaf bounds, partitioned
   parent indexes, attached leaf indexes, and validity.
7. Separate logical edge property constraints from physical indexes.
8. Build one owner-transaction bootstrap primitive for base objects, global kind
   rows, the default graph, its declared leaves, constraints, and registries.
   It is testable only through internal disposable-database scaffolding at this
   stage. The normal `Driver.AssertSchema` path must not publish or accept
   `v2_accepted` until graph-scoped relation rendering, every helper/call-site
   change, direct/batch/Cypher lifecycle, and WP5 enforcement pass their join
   gate. In the joined bootstrap, write the marker last and publish caches only
   after commit. Secondary Schema.Graphs entries remain lazy.
9. Add schema teardown tests proving no new type, function, trigger, registry,
   sequence, or metadata relation is leaked.

Tests:

- DDL formatter unit tests including identifier limits and quoting.
- Bootstrap twice without drift or duplicate objects.
- Rollback of failed bootstrap leaves no accepted marker or poisoned cache.
- Operator bootstrap leaves a pending handoff without guessing a target role;
  the two-connection continuation binds the authenticated target OID/name,
  applies exact grants, records smoke, and never closes or swaps caller pools.
- Bootstrap initialization/handoff/smoke receipts use the operation scope;
  ambiguous commits at every control transaction, target-smoke replay, consumed
  or renewed tokens, failed/new attempts, and external report regeneration are
  idempotent and never require a migration ID.
- Wrong/superuser/owner/inherited target identities, the post-bootstrap operator
  pool, and crash after grants before smoke are rejected or resume idempotently.
- Catalog descriptors for healthy and deliberately malformed hierarchies.
- Physical routing checked with tableoid::regclass.
- No DEFAULT kind leaf.
- Duplicate graph-local edge ID across kinds rejected.
- Same ID in different graphs allowed.
- Same endpoint/kind/graph conflict and cross-kind separation match V1 through
  root, graph-parent, and direct-leaf writes.
- id, graph_id, and kind_id updates rejected by raw SQL.
- Property claims and graph cascade clean up transactionally.

Exit gate:

- The internal bootstrap harness can construct the complete candidate schema in
  a disposable database and catalog classification reports it as healthy.
- All identity/property/cascade contracts pass live PostgreSQL tests.
- The preparation V1 module tag/source archive has already been frozen. V2 is
  exercised on the unreleased transition development stack; no existing
  published release has switched its bootstrap.

Do not add an environment variable, DSN option, public constructor option, or
runtime layout toggle to make this testable. Freeze the preparation release
first, then test the real V2 policy on the unreleased transition branch/stage
until WP8 passes. Helper DDL, graph-scoped persistent-relation rendering,
translator call-site changes, obsolete-overload teardown, and their tests must
land atomically; no commit may expose incompatible helper signatures.

### WP3: Graph, declared-kind, and dynamic-kind lifecycle

Purpose: provision exact graph parents and leaves safely across processes and
transactions.

Implementation:

1. Add typed node-parent, edge-parent, and edge-leaf operations behind a
   compatibility-preserving/deprecated `Query.CreatePartitionTable` wrapper as
   determined by the WP0 API inventory.
2. Change createGraphPartitions so edge_<graph_id> is a LIST(kind_id)
   partitioned parent.
3. Add EnsureEdgeKindPartitions(ctx, tx, graphID, sortedKindIDs). Do not make
   generic AssertKinds create edge leaves because node-only kinds are valid.
4. Apply the locked canonical application-schema algorithm. Reject conflicting
   duplicate graph definitions before mutation; resolve DefaultGraph and
   WithGraph name-only values without discarding declarations.
5. Preserve the existing lazy graph lifecycle. AssertSchema records canonical
   definitions and asserts the default graph, but it does not materialize every
   Schema.Graphs entry. A secondary graph and its declared leaves are created
   only when WithGraph or AssertGraph first asserts it.
6. Eagerly create every edge kind declared for a newly asserted graph,
   including kinds with no rows.
7. Create dynamic leaves idempotently under pre-ID name locks followed by graph
   and sorted-kind numeric locks. Add provenance flags and create the leaf
   atomically, then re-read the catalog after locking.
8. Use the approved restricted provisioning privilege from WP0.
9. Refactor SchemaManager caching and transaction publication according to the
   cache rules above.
10. Define hard drift versus repairable managed absence:
   - An absent leaf for a known kind may be created under lock before writes.
   - An invalid exact managed index whose canonical definition and dependency
     set are known may be rebuilt under the graph lock during explicit
     assertion.
   - Wrong parent, wrong bound, DEFAULT leaf, duplicate bound, incompatible or
     unmanaged relation/index, wrong index lineage, or marker mismatch is hard
     drift.
11. Do not drop a leaf merely because a later schema assertion omits its kind.
    Omission is not authorization to delete data or physical storage.
12. Make explicit AssertSchema and health reconcile catalogs even if graph
    objects are cached.
13. Ensure two processes introducing the same kind converge on one valid leaf.
14. Return a typed missing-leaf/provisioning error containing graph_id and
    kind_id if a write reaches PostgreSQL without a usable leaf.

Tests:

- Declared empty kinds.
- Name-only default graph resolves the same declarations as Schema.Graphs.
- Secondary schema graphs stay lazy until WithGraph/AssertGraph and receive all
  declared leaves when asserted.
- Dynamic kind after graph creation.
- Node-only kind does not create an edge leaf.
- Omitting a kind during later assertion preserves its leaf and rows.
- Desired-topology rows survive reopen and preserve additive stored,
  declared-empty, migrated, dynamic, and retained provenance flags.
- Removing an expected empty leaf yields an accepted layout with a repairable
  finding and explicit assertion recreates it; registry evidence of rows or a
  detached lookalike makes the same absence hard drift.
- An unexpected leaf with no desired-topology row is hard drift.
- Two pools race to create the same leaf.
- Several kind IDs are locked and created in sorted order.
- Failed DDL and caller rollback do not publish cache entries.
- Graph created and used inside one transaction.
- External leaf DDL requested after any prior edge-parent read/write or opaque
  Raw statement returns a bounded retryable error before DDL starts.
- Cached graph with a removed or malformed leaf is reconciled or rejected as
  categorized.
- Non-owner runtime role can ensure a leaf but cannot run arbitrary DDL.

Exit gate:

- Direct lifecycle APIs produce exactly one graph parent and one leaf per
  declared or dynamically written edge kind.
- Concurrent and rollback tests prove catalog state, cache state, and returned
  graph/kind state agree.

### WP4: Direct, batch, Cypher, mutation, and hydration correctness

Purpose: make every application path route and scope edges correctly.

Direct transactions:

1. In CreateRelationshipByIDs, resolve the graph, assert/map the edge kind,
   ensure its leaf, then insert through edge_<graph_id>.
2. Scope UpdateRelationship and every ID-only edge statement to graph_id.
   Include kind_id when the entity has it; otherwise resolve it through
   edge_identity.
3. Convert root SQL constants in statements.go into graph-scoped formatters,
   including deleteNodeWithIDStatement and every direct/batch node delete.
   Fully qualify root sequence allocation through `pg_catalog.nextval`,
   `pg_catalog.pg_get_serial_sequence`, and `public.node`/`public.edge`.
4. Fix relationshipQuery.Update, which currently delegates property operations
   through query.Node instead of query.Relationship; add a regression test
   before changing it.
5. Publish transaction-local schema cache deltas only after Commit and discard
   them on Close or rollback.

Batch operations:

1. Buffer or group records with their graph identity. Batch.WithGraph must not
   reroute records that were buffered for a previous graph.
2. Before a relationship flush, collect distinct edge kinds, assert/map them,
   sort them, and ensure each leaf once.
3. Preserve array alignment and retryable buffers if leaf provisioning fails.
4. Scope relationship update/delete batches to graph and kind/identity lookup.
5. Route identity-property upsert through the registry-aware routine.
6. Document and test current autocommit behavior when Batch has no allocated
   transaction. Do not claim an atomic batch commit that the implementation
   does not provide.

Cypher:

1. Extend translate.Result with sorted, deduplicated
   RequiredEdgeKindIDs. Accumulate it across every query part only while
   translating edge creation, then sort/deduplicate on successful translation.
   Node kinds and read-only kind filters do not request edge leaves.
2. Add `FromCypherResult` and the shared `executeTranslated` path defined above;
   retain the existing `FromCypher` API as a metadata-dropping compatibility
   wrapper for callers that only format. Prove Transaction.Query and live query
   cannot drop provisioning metadata before execution. GraphBench's normal
   measured query uses Transaction.Query; its separate EXPLAIN path is mutation-
   forbidden at case ingestion and uses Result graph/readonly metadata only.
3. Ensure every required leaf before executing mutation SQL. Failure executes
   no translated application statement; scoped kind/catalog/provisioning SQL
   follows its own commit/cache contract. Raw arbitrary SQL does not trigger
   provisioning and receives the database's missing-leaf or immutability error.
4. Implement graph-scoped persistent-relation rendering. Map only storage
   `node`/`edge` table references and mutation targets to
   `node_<graph_id>`/`edge_<graph_id>` in normal translation, count/aggregate
   fast paths, expansions, nested shortest-path/filter SQL, and
   `EdgeArrayFromPathIDs` using the locked `PersistentRelations` context. Do not
   rewrite CTE aliases. Sequences stay root-owned, but render only
   fully qualified `pg_catalog` calls with `'public.node'`/`'public.edge'`
   serial-sequence lookups.
   Never render normal SQL directly to a kind leaf.
   Audit every other emitted managed relation, composite type, and helper
   function—including `kind`, `nodecomposite`, `edgecomposite`, `pathcomposite`,
   kind/endpoint/path helpers, and ASP harnesses—and render it as `public.*`.
   Qualify PostgreSQL built-ins/operators through `pg_catalog` where caller
   search_path could shadow them.
   Replace translator/optimizer semantic comparisons against literal
   `pgsql.TableNode`/`TableEdge` (including projection endpoint-join detection)
   with `PersistentRelations.IsNode/IsEdge`; retain shortest-path,
   endpoint-join, and limit-pushdown behavior tests.
5. Add kind equality to EdgeComposite update/delete joins. Record static kind
   IDs on typed relationship bindings, preserve them through safe copies,
   aliases, and projections, and duplicate a visible literal
   `kind_id = ANY(...)` predicate onto the target mutation for pruning. Do not
   infer a kind through arbitrary OR/NOT expressions.
6. Resolve a SET/REMOVE binding before mapping labels. Preserve node-label
   mutation, but return one stable immutable-relationship-kind error for edge,
   expansion-edge, and path-edge bindings. Do not promise unimplemented MERGE
   work.
7. Change `start_node`, `end_node`, `nodes_to_path`, and
   `ordered_edges_to_path` calls to include graph ID and apply the locked
   `edges_to_path`/swap-helper decision.
8. Hydrate ID arrays through `(graph_id, edge_id)` identity lookup and then the
   graph parent with both edge and kind IDs, preserving input duplicates and
   ordinality.

Generated cases:

- Add source case comments to the applicable existing translation files,
  including create.sql, delete.sql, update.sql, shortest_paths.sql,
  pattern_binding.sql, pattern_expansion.sql, pattern_rewriting.sql, and
  stepwise_traversal.sql.
- Regenerate through `make test_update` or the documented
  `CYSQL_UPDATE_CASES` workflow. That command rewrites the translation corpus;
  review the entire `cypher/models/pgsql/test/translation_cases/*.sql` diff,
  not only named files, and never hand-edit generated statements.
- Add a nonzero-graph structural corpus test that rejects executable root
  `node`/`edge` references in both final SQL and SQL-valued parameters while
  allowing only schema-qualified root sequence references and CTE aliases. Run
  the same assertion for `DefaultGraphID == 0` and require graph parents, not
  roots.
- Token/AST-audit the complete generated corpus for unqualified known managed
  relations, types, and functions, and run hostile-search_path integration
  cases so a caller-writable lookalike is never resolved.

Tests:

- Mixed three-kind direct and batch inserts.
- Same endpoint/same kind upsert and same endpoint/different kind separation.
- Dynamic kind insertion from direct, batch, and Cypher paths.
- Graph switch with buffered batch rows.
- Provision failure leaves the batch retryable.
- RequiredEdgeKindIDs is exact, sorted, and deduplicated.
- HasMutations is accumulated across multipart queries and blocks mutation in
  every Raw/EXPLAIN-only tool path.
- Metadata survives every format/execution adapter and a provision failure sends
  no translated application statement; tests distinguish allowed schema SQL.
- Relationship label mutation is rejected.
- Typed edge update/delete affects only the requested kind.
- Typed update and delete after WITH aliasing, projection, and multipart
  translation retain both the kind-equality join and a visible target kind
  predicate, with exact three-kind survivors.
- Explicit same edge ID in another kind is rejected.
- ID-only update/delete/hydration affects one graph and resolves one kind.
- startNode/endNode with colliding node IDs in different graphs.
- Path hydration preserves order across interleaved leaf IDs.

Exit gate:

- Shared semantic results are unchanged.
- Every driver-generated write reaches an existing correct leaf.
- No mutation can cross a graph or silently change an edge kind.
- Registry-backed upsert matches characterized V1 behavior.

### WP5: Constraint lifecycle, cascade, reset, and maintenance

Purpose: complete schema behavior outside ordinary inserts and reads.

Implementation:

1. Replace edge expression-unique-index reconciliation with logical
   registry-constraint reconciliation. Node constraints remain physical
   expression indexes.
2. Implement edge constraint add, backfill, validate, publish, drop, and re-add
   under the graph schema lock.
3. Keep a non-unique graph-parent expression index where measurements show it
   is needed for lookup; do not mistake it for uniqueness enforcement. V1
   unique expression indexes are replaced by registry authority and are never
   recreated as independent per-leaf unique constraints.
4. Install and reconcile the selected graph-aware node-delete trigger at the
   relation level proven by WP0.
5. Reconcile the graph-row deletion guard and deny target-role DELETE on the
   graph catalog. `WipeGraph` is the supported data-reset lifecycle and retains
   graph rows, logical constraint metadata, graph/kind partitions, and additive
   retained provenance. A missing graph row with those objects is hard drift;
   do not invent orphan/tombstone reuse semantics.
6. Update WipeGraph as a global atomic reset. Truncate node, edge, identity
   claims, and property claims together using the WP0-proven relation set;
   retain graph/schema definitions and preserve the retain delegate's rollback
   behavior.
   Invoke one fixed, schema-qualified SECURITY DEFINER reset function because
   the application role deliberately has no direct registry TRUNCATE privilege;
   grant only EXECUTE on that no-arbitrary-target function.
   If the retain delegate introduces a new edge kind, leaf provisioning must
   use the same transaction/connection that already holds the hierarchy locks;
   the normal external schema transaction can self-deadlock. Otherwise reject
   before prior edge-parent access with a retryable error. Publish its cache entry only after
   the complete wipe/retain transaction commits.
7. Replace graph-scoped benchmark/corpus child truncation with the WP0-proven
   safe option. Do not assume deleting claim rows first makes a referenced edge
   child truncatable, and never use TRUNCATE CASCADE.
8. Verify that retained empty partitions are reusable after wipe.
9. Extend OptimizeStorage for a nested pg_partition_tree hierarchy and verify
   VACUUM/ANALYZE reaches leaves. Explicitly analyze partitioned parents after
   material data changes because autovacuum does not analyze partitioned parent
   statistics automatically.
10. Define table_sizes() semantics. Prefer a logical graph aggregate for public
   reporting and a separate physical relation view for diagnostics.
11. Preserve endpoint behavior; do not introduce new endpoint foreign keys.

Tests:

- Constraint add with no rows, existing rows, duplicates, and concurrent
  driver and raw-SQL writers that do not honor advisory locks.
- Constraint drop/re-add and stale-claim checks.
- Conflicts across two or more kind leaves for direct, batch, and Cypher writes.
- Missing, JSON-null, empty text, default collation, and rollback.
- Node delete through both batch and translated child-partition SQL.
- Colliding node IDs in two graphs.
- Target-role and raw graph-row deletion are rejected; graph wipe across at
  least three kind leaves retains graph/topology/constraint metadata.
- Wipe retain introduces a new edge kind without self-deadlock; retain failure
  rolls back data, leaf expectation/topology, registries, and caches together.
- Registry state empty after delete, cascade, wipe, and rollback.
- Nested-tree vacuum/analyze and logical size accounting.

Exit gate:

- Constraint lifecycle is graph-wide, concurrent-safe, and behaviorally
  equivalent to V1.
- Delete, cascade, truncate, wipe, optimize, and sizing operate across every
  leaf without stale registry data.

### WP6: Offline migration engine and command

Purpose: provide the only supported path from any initialized V1, including an
empty one, to V2.

Command contract:

~~~text
dawgs-pg-migrate status [--report FILE]
dawgs-pg-migrate preflight --manifest FILE --capacity-attestation FILE --backup-evidence FILE --target-connection-file FILE --preparation-role ROLE --preparation-credential-plan FILE [--writer-role ROLE]... [--writer-probe-file FILE]... [--external-writer-evidence FILE]... --report FILE
dawgs-pg-migrate migrate --manifest FILE --preflight-report FILE --capacity-attestation FILE --backup-evidence FILE --target-connection-file FILE --preparation-credential-plan FILE --drain-evidence FILE [--writer-probe-file FILE]... [--external-writer-evidence FILE]... --confirm TOKEN --report FILE
dawgs-pg-migrate verify-fence --migration-id ID --manifest FILE --preflight-report FILE --capacity-attestation FILE --backup-evidence FILE --target-connection-file FILE --preparation-credential-plan FILE --drain-evidence FILE [--retirement-evidence FILE]... [--writer-probe-file FILE]... [--external-writer-evidence FILE]... --confirm TOKEN --report FILE
dawgs-pg-migrate resume --manifest FILE --migration-id ID --confirm TOKEN --report FILE
dawgs-pg-migrate validate --migration-id ID --report FILE
dawgs-pg-migrate accept --migration-id ID --validation-digest DIGEST --target-connection-file FILE --confirm TOKEN --report FILE
dawgs-pg-migrate smoke (--migration-id ID | --bootstrap) --target-connection-file FILE --confirm TOKEN --report FILE
dawgs-pg-migrate rollback --migration-id ID --confirm TOKEN --report FILE
dawgs-pg-migrate complete-v1-handoff --migration-id ID --preparation-connection-file FILE --handoff-evidence FILE --confirm TOKEN --report FILE
dawgs-pg-migrate cleanup --migration-id ID --cleanup-evidence FILE --confirm TOKEN --report FILE
dawgs-pg-migrate rebind-restore --migration-id ID --restore-evidence FILE [--target-connection-file FILE] [--writer-probe-file FILE]... [--retirement-evidence FILE]... [--external-writer-evidence FILE]... --confirm TOKEN --report FILE
dawgs-pg-migrate rebind-restore --bootstrap-operation-id ID --restore-evidence FILE --target-connection-file FILE --confirm TOKEN --report FILE
dawgs-pg-migrate confirm --action ACTION (--migration-id ID | --bootstrap | --bootstrap-operation-id ID) --evidence FILE --report FILE
dawgs-pg-migrate receipt (--migration-id ID | --bootstrap-operation-id ID) --type TYPE [--attempt-id ID] --report FILE
dawgs-pg-migrate health [--report FILE]
dawgs-pg-migrate version
~~~

Use a testable commandRuntime.run style and standard-library FlagSet unless a
repository-wide CLI choice supersedes it. The command uses raw pgx connections.
Each mutating invocation opens a dedicated control connection, reacquires the
exclusive session advisory layout lock, revalidates durable state and the
database writer fence, and holds the lock until that invocation exits.
Session locks cannot span separate migrate, verify-fence, resume, validate,
accept, rollback, or cleanup processes. Durable state and the ACL fence bridge
invocations. Losing the control connection outside commit returns to the last
proven checkpoint.
Connection loss during COMMIT has unknown outcome: return
`commit_outcome_unknown`, do not assert either result, and require status/resume
to reacquire the lock and re-read state plus object digests.

Every command except `version` accepts the global `--connection-file FILE` for
the operator/control connection. This is a versioned mode-0600 envelope holding
pgx transport/credentials plus a `current_identity_provider` block: built-in
self-managed selection or managed provider kind/version, resource scope,
restore-incarnation resolver, verifier/trust-root IDs and material, and any
ephemeral provider authentication required to verify it. The runtime's additive
pool/driver binding consumes the same provider configuration schema. Only
provider kind/version, nonsecret scope/key IDs, observed verified identity, and
canonical config digest enter reports/receipts; connection/provider secrets and
trust material are inputs only. `CONNECTION_STRING` is a convenience fallback
only for the built-in identity provider; if built-in identity facts are not
available, every command returns `current_identity_unavailable` and requires
the file. Use an explicit target connection file for target-role probes, and one
repeated `--writer-probe-file` for every distinct directly authenticated
application-writer credential path identified by preflight; do not
accept raw DSN flags that can leak through process listings. A role name or
operator `SET ROLE` is not a substitute because it does not reproduce
`session_user`, login-role settings, or authentication. Support repeated
writer-role flags for catalog inventory, lock and statement timeouts, report
paths, and state-bound confirmation tokens. Write
reports atomically with mode 0600 using a same-directory temporary file,
fsync/rename, and deterministic regeneration from durable facts. Never expose a
connection value in reports, diagnostics, or partial token output.
Every receipt-producing mutator accepts `--report`; failure to write that file
never causes the database mutation to run again. The read-only `receipt`
command regenerates a canonical report from durable facts. Without an explicit
attempt ID it resolves only the defined singleton receipt or the receipt named
by the relevant current pointer; it rejects ambiguous history.

`rebind-restore` validates options against durable state. Migration-scoped
accepted V2 requires a target connection and forbids writer-control rebasing;
in-progress V1-to-V2 forbids a target connection and requires the exact closed
writer/external-control facts rebased with phase-appropriate live probes or
retirement evidence as described below; rolled-back V1 forbids a target
connection and relies on the recorded retirement/handoff facts before resetting
handoff. Bootstrap scope is accepted V2 only and always requires a target
connection. Supplying a state-inapplicable option is an error, not an ignored
compatibility path. Its supplied scope ID must equal the
state-selected `active_migration_id`, `current_rollback_migration_id`,
`accepted_migration_id`, or `bootstrap_operation_id`; a historical row is never
eligible for mutation merely because its state or timestamp looks plausible.
Smoke, V1 handoff, and cleanup enforce the same pointer rule. Read-only
`receipt` may render an explicitly named historical type/attempt, but labels it
historical/non-authoritative and never resolves a historical row implicitly.

A writer-probe file is a versioned, mode-0600 JSON secret containing an opaque
probe ID, connection configuration, expected `session_user`/`current_user`, and
one locked retirement strategy:

- `provider_version`: a provider-issued immutable secret reference and
  credential/version ID plus the verifier for signed/versioned retirement
  evidence about that exact version.
- `postgres_role_nologin`: the exact PostgreSQL login-role OID/name and a
  cluster-scope attestation proving the role is installation-dedicated, or an
  inventory/approval of every affected database and application. The tool, not
  an unaudited manual step, retires it with `ALTER ROLE ... NOLOGIN` after the
  database-fence probe.

Persist only the probe ID, authenticated role OIDs/names, membership and
capability facts, strategy, immutable nonsecret credential reference/version,
scope-evidence digest, and a digest of the canonical immutable descriptor.
The secret/transport envelope, including host, port, database target, and
credential, is excluded from that immutable digest and is never persisted.
Every use nevertheless authenticates it and records the observed current-
identity binding separately. Preflight binds both the exact closed immutable
probe-set digest and its source database binding; later commands reject a
missing, additional, duplicated, or changed immutable descriptor. A raw or
substituted PostgreSQL password has no stable version
identity and cannot serve as retirement evidence: use the journaled NOLOGIN
strategy or an approved versioned provider. The target login is distinct, so
the migrator never rotates an old role into the target role.
Cluster-scope evidence binds the current PostgreSQL cluster identity and exact
role OID/name/fingerprint; restore rebind cannot reuse it to alter an unrelated
same-name or coincidentally same-OID role.

`--preparation-role` is required and names an already existing, installation-
dedicated dormant rollback login. At preflight it must be `NOLOGIN` with a null
password, own no object, have no role membership or managed access, and have no
role/database setting that broadens access. Preflight resolves and binds its
OID/name and complete eligibility fingerprint; it never creates the role or
guesses intent from a name. Null-verifier and alternate-auth-path proof comes
from readable authoritative cluster catalogs or the signed cluster credential
controller named by the plan; a deployment profile that exposes neither is
ineligible rather than assumed safe. The required versioned, nonsecret
`--preparation-credential-plan` binds that fingerprint and cluster scope to one
approved post-rollback issuer, issuance strategy, immutable credential/version
reference format, preparation connection-artifact format, lifetime, and policy
for proving that no password, certificate, proxy, or other authentication path
remains active before issuance or alongside the new credential. It contains no
credential and grants no access. Each repeated
external-writer evidence file is a versioned nonsecret descriptor/attestation
for a scheduled job, replication/subscription path, provider writer, or other
unprobeable control. Bind its stable control ID, controller/resource scope,
immutable credential/control facts, current cluster/database target, evidence
schema, and canonical digest. The complete probe plus external-control set is
closed: later phase-appropriate commands reject missing, extra, duplicated, or
wrong-cluster inputs, and reject changed immutable facts outside the explicit
restore/rebind mapping below.

Backup evidence is a versioned provider/operator attestation containing the
stable/current source identity, immutable backup ID/provider, completion and
verification times, retention/availability fact, restore-rehearsal identity and
result, evidence issuer/signature, and schema version. Preflight enforces the
WP0 freshness/restore policy and binds its digest into the report/token. The
required migrate input must match that approved digest (or force a renewed
report/token) and is journaled before the fence; absent, stale, wrong-source, or
unverified evidence blocks mutation. PostgreSQL is not asked to infer external
backup existence.

V1 handoff evidence is a versioned signed issuance/deployment attestation bound
to migration ID, current database identity and identity epoch/rebind pointer,
the preflighted credential-plan digest, exact preparation-role OID/name/
fingerprint, nonsecret new-credential provider/version reference, preparation
artifact version, actor, and time. It proves issuance happened after the
current rolled-back/pending epoch and that this version is the sole active
credential for the role, with every older version and alternate path absent or
retired. For the native PostgreSQL strategy, the approved issuer changes the
dedicated role from `NOLOGIN PASSWORD NULL` to exactly one new verifier and the
evidence covers cluster authentication policy; the migrator verifies the
resulting catalog facts but never reads, snapshots, or logs the verifier. The
versioned mode-0600 preparation connection file declares the same nonsecret
provider/version and artifact references, expected current database identity,
and exact session/current-user role, then carries the credential only as an
ephemeral input. `confirm --action v1-handoff` binds the evidence digest;
`complete-v1-handoff` authenticates that exact file and persists a nonce/epoch-
bound receipt digest atomically with the grants, passed status, and current
handoff pointer. A token contains only the authenticated binding, not a
substitute evidence body or credential.

`status` remains nonblocking even while a mutator holds the session lock: it
uses a separate read path and reports phase, high-water mark, copied/total rows,
rate, ETA when meaningful, last heartbeat, and stale-heartbeat warning. Define
cancellation at statement/chunk boundaries; never checkpoint a canceled or
unknown-outcome chunk.

`preflight` issues the initial migrate confirmation. The read-only `confirm`
command issues or renews action-specific bootstrap/acceptance smoke, external-
fence verification, resume, rollback, V1 handoff, accept, cleanup, and restore-
rebind confirmations only after validating the required state/report/evidence.
Tokens bind action, stable and current database identities, identity epoch/
current rebind pointer, migration/state digests, nonce, issue/expiry times, and
evidence digest. The mutator records the token
digest atomically with its phase change. Replay against a completed phase returns
idempotent status and never repeats destructive work; expired, wrong-action,
restored-identity, or already-consumed tokens cannot advance a new phase.
Versioned capacity, pre-migration-backup, preparation-credential-plan, writer-
probe, drain, external-writer, provider/native-role-retirement, V1-handoff,
restore/rebind, cleanup-evidence, report, receipt, and fleet-record schemas are
part of the WP0 contract, and the built-artifact smoke suite exercises every
CLI input.

Manifest:

~~~json
{
  "version": 1,
  "installation_id": "inventory-record-id",
  "source_configuration": {
    "artifact": "application-schema.json",
    "sha256": "..."
  },
  "owner_attestation": {
    "owner_id": "service-owner",
    "attested_at": "2026-01-01T00:00:00Z",
    "statement_version": 1
  },
  "graphs": [
    {
      "name": "default",
      "edge_kinds": ["KindA", "KindB"]
    }
  ],
  "excluded_unmaterialized_graphs": ["lazy-graph-with-no-database-row"]
}
~~~

The consuming application or deployment configuration generates this manifest
from its authoritative graph.Schema declarations, intersects them with the
materialized database graph inventory, and records an installation-owner
attestation. Lazy Schema.Graphs entries with no database graph row are listed
as excluded metadata and are not migration targets. A Retriever dump or global
kind-table scan cannot supply this authority.

Reject invalid UTF-8, duplicate JSON object keys, unknown fields, duplicate
graphs, duplicate kinds, missing materialized database graphs, and any stored
edge kind omitted from its graph declaration.
The database can mechanically prove those conditions, but it cannot prove that
the owner remembered every declared-empty kind; that is why the attestation and
source configuration are mandatory.
Preserve exact graph/kind UTF-8 bytes with no Unicode normalization. Sort by
unsigned raw UTF-8 bytes, define number/time encoding and array/object ordering
in the schema, and distinguish digest-bearing facts from presentation metadata.
Persist SHA-256 of the canonical bytes. A Retriever dump is not a canonical
migration manifest because it cannot include declared-but-empty kinds.

Reports and confirmations:

- Version every JSON format.
- Emit stable state, warning, blocker, and exit codes.
- Include timestamps, tool/source revision, database identity, manifest
  checksum, preflight digest, catalog/topology fingerprint, counts, checksums,
  phases, and receipts.
- Never print or persist connection strings, credentials, property values, or
  sampled user records.
- Bind destructive confirmation tokens to database identity, migration ID,
  current state, manifest checksum, versioned preflight digest, catalog
  snapshot, and validation digest as applicable. A generic --yes flag is
  insufficient.

The preflight report is also the canonical mode-0600 nonsecret input bundle. It
contains canonical manifest, capacity attestation, backup evidence,
preparation-credential plan, target-role public identity facts, immutable
writer-probe descriptors, external-control evidence, their individual digests,
and the observed database/catalog facts; it contains no connection transport or
credential. `migrate` requires that report plus the explicitly listed source
files, revalidates byte-canonical equality, and journals the bundle and digest
before/with the fence. `verify-fence` requires the same report and explicitly
resupplied manifest/capacity/backup/preparation files, target secret connection,
writer secret/transport envelopes, external-control evidence, and phase-new
drain/retirement evidence. It recomputes every mutable catalog, data,
dependency, role, capacity/freshness, target-authentication, preparation, and
control fact; no CLI input is recovered by guessing from a digest. The
authoritative fenced report/digest is durable, must be reviewed, and feeds a
new `confirm --action resume` token before `resume` may start global backfill.

Database identity is not only a mutable database name or restore-specific OID.
WP0 defines a `CurrentIdentityProvider` contract usable by both the raw-pgx
migrator and every runtime connection. Its verified result contains provider
kind/version, an immutable cluster or service-resource identity, a restore/
deployment-incarnation identity that is not copied inside the database, and
the observed database OID/name; signed providers also return issuer and key ID.
The built-in self-managed profile may use PostgreSQL system identifier plus
database OID/name for logical/new-database restores. A profile claiming support
for same-identity physical restore or in-place PITR must additionally supply a
trusted external incarnation value that changes for every restore. A managed-
service profile that hides system identity must supply the provider-signed
resource/incarnation equivalent to both migrator and runtime.

The manifest/fleet installation UUID is stable ownership identity only and can
never substitute for current identity because it is copied by restore. If a
profile cannot expose a non-copied cluster/resource identity, reject cross-
cluster rebind for that profile. If it cannot expose a non-copied restore
incarnation, reject same-identity restore/PITR support and require restoration
to a detectably new database/cluster before rebind. Constructor, preflight, and
restore commands return stable `current_identity_unavailable` rather than
silently weakening the gate. Read-only preflight never writes an identity.
Prototype provider availability, signature verification, rotation, outage, and
cache invalidation on every supported service/version profile. Reports keep
stable installation identity separate from current cluster/resource,
incarnation, and database identity.

#### WP6.1 Read-only status and preflight

Preflight must perform no mutations. It:

1. Acquires a dedicated inspection connection, takes the migration-wide session
   advisory lock to exclude another migrator, and starts a bounded raw-pgx
   `REPEATABLE READ READ ONLY` transaction (or the WP0-selected
   `SERIALIZABLE READ ONLY DEFERRABLE` form) with transaction-local timeouts.
   Export and import that same snapshot if parallel workers are required. The
   driver's current logical read wrapper is not a substitute for this database
   transaction. Classify and compute every catalog/data digest within it.
2. Verifies tool/runtime/source compatibility and PostgreSQL version.
3. Resolves database name and OID without exposing connection details.
4. Loads and canonicalizes the manifest.
5. Inventories graphs, kinds, stored kind IDs, declared-empty kinds, counts,
   min/max IDs, sequences, indexes, logical constraints, owners, grants,
   comments, tablespaces, storage parameters, triggers, and dependencies.
6. Resolves every manifest kind name to an existing global kind ID. If a
   declared kind is absent, stop with explicit final-V1 remediation rather than
   allocating an installation-specific ID during copy.
7. Rejects duplicate (graph_id, id), endpoint conflicts, property uniqueness
   violations, missing kind catalog rows, invalid kinds, corrupt properties,
   or graph isolation errors.
8. Classifies every dependency as preserved or unsupported.
   In particular, inventory inbound foreign keys, replica identity, and other
   objects bound to the V1 primary key. Preflight must either have a tested
   topological drop/rebind plan or reject them.
9. Calculates per-tablespace, WAL, archive, temporary-space, relation/index, and
   max_locks_per_transaction requirements with safety margins. PostgreSQL SQL
   cannot portably prove provider/filesystem free space, so require a versioned
   operator/provider capacity attestation and record its source and headroom.
   Validate the required versioned pre-migration backup/restore evidence against
   source identity, freshness, retention, and WP0 rehearsal policy.
10. Verifies migration and future runtime roles, required privileges, lock and
   statement timeouts, and that the capacity attestation covers the estimate.
   The separate target connection must authenticate directly as the intended
   non-owner role (`session_user = current_user`); record and bind its role OID,
   name, membership, and prohibited attributes into the preflight digest. Its
   login OID must differ from the schema owner, operator/control session user,
   rollback preparation role, and every V1 writer probe so native V1
   credentials can be retired without changing the target identity. Bind a
   planned V1 preparation-handoff role OID/name that is also distinct from the
   owner, operator, historical writer, and target logins; it receives newly
   issued credentials only if rollback completes. Resolve it only from the
   required `--preparation-role`, require it already exists as the installation-
   dedicated `NOLOGIN PASSWORD NULL` role with the locked no-membership/no-
   ownership/no-managed-access eligibility fingerprint, and bind that
   fingerprint plus the required preparation-credential-plan digest into the
   report/token. Reject a plan whose issuer, cluster scope, role fingerprint,
   sole-active proof, or connection-artifact contract cannot be verified.
11. Identifies current writer roles and sessions and defines the concrete fence
    actions. Each repeated writer-probe file must authenticate directly and
    bind its `session_user`, `current_user`, role OIDs, memberships, role/database
    settings, and applicable write paths into the preflight digest. Inventory
    transitive role membership, object/schema ownership, root and child grants,
    sequence grants, default ACLs, SECURITY DEFINER write functions, scheduled
    jobs, unresolved prepared transactions, COPY paths, and replication/
    subscription writers. Every discovered login-capable writer path must have
    an authenticated writer-probe input or a versioned repeated
    `--external-writer-evidence` attestation;
    an unfenceable owner or superuser path is a blocker.
12. Produces a versioned preflight digest over the manifest, database identity,
    topology, data counts/checksums, dependencies, role/privilege graph,
    capacity facts, timeouts, and catalog snapshot, then binds the confirmation
    token to that digest.

Any topology, data, dependency, manifest, capacity, or privilege blocker makes
preflight non-runnable. Warnings require an explicit recorded disposition.
Tests prove that the inspection and target preflight transactions cannot
mutate. Concurrent writers may change the live database but cannot create a
mixed-snapshot digest. The authenticated writer-probe inputs are identity and
probe-plan inputs during preliminary preflight; the actual DML, DDL, COPY,
function, and sequence-privilege fence probes run only after the journaled
database fence. The authoritative post-fence digest is recomputed in a new
transaction using the same snapshot protocol.

#### WP6.2 Writer fence and durable start

Migration:

1. Opens a dedicated control connection, acquires the session layout lock, and
   revalidates the approved preliminary preflight report/token against the
   explicitly resupplied manifest, capacity, backup, preparation plan, target
   connection, writer probes, external controls, and drain evidence. Before
   changing database state, it directly authenticates the target and every
   bound writer-probe file and verifies the preflighted session/current-user
   identities and settings. Missing, duplicate, stale, or mismatched input is a
   blocker.
2. In one transaction, creates the minimal journal/control objects, records
   `v1_to_v2_in_progress` with phase `fence_installing`, migration ID, manifest
   checksum, source fingerprint, complete pre-change catalog/ACL/default-ACL/
   sequence snapshot, approved pre-migration backup-evidence digest, planned
   fence actions, rollback object identities, and external drain/credential-
   attestation requirements. For first-time unmarked V1, it initializes the
   singleton's current identity at epoch zero with null rebind/current-rollback
   pointers. For a later migration from authoritative `v1_rolled_back`, it
   requires the provider identity to match, preserves current identity, epoch,
   and all append-only history, and creates a new migration row. If the current
   rebind pointer names the prior rollback scope, the start transaction appends
   an `identity_binding_carried_forward` receipt under the new migration scope,
   keyed by the migrate confirmation nonce and referencing the validated prior
   digest, identity, provider facts, and epoch, then repoints the layout to it.
   It atomically replaces `current_rollback_migration_id` with the new
   `active_migration_id` and resets only phase-specific target, validation,
   acceptance, smoke, and cleanup-map fields. It never recreates the singleton
   or resets the identity epoch. In that same transaction it applies every
   database-local fence change: database/schema/table/sequence/
   function ACLs and default ACLs, managed-object ownership, and managed
   rejection guards. It records
   `fence_installed_unverified`, and commits. If it rolls back, V1 is unchanged;
   if commit outcome is unknown, status/resume resolves it from durable facts.
   Do not change cluster-global LOGIN/password attributes, superuser flags, or
   role memberships here; the database-local snapshot cannot make those safe or
   reversible for unrelated databases.
3. Validates the versioned drain evidence, confirms application traffic is
   stopped, and terminates every old application session. It also proves
   scheduled, replication, subscription, and prepared-transaction writers are
   stopped or covered by the recorded external-control plan. Application drain
   may precede the journal because it does not mutate the database; no managed
   database-local authorization/fence change may occur outside step 2. External
   writer credentials and cluster-global login attributes must remain valid
   until step 5 commits.
4. Opens a fresh direct connection from every bound writer-probe file and
   requires the recorded identity; operator `SET ROLE` is forbidden. A specific
   database CONNECT authorization rejection bound to that preauthenticated
   probe is sufficient. Otherwise, in rollback-only transactions it attempts
   every applicable root and direct-child INSERT, COPY, UPDATE, DELETE,
   TRUNCATE/DDL, inherited-role, and SECURITY DEFINER write path with valid
   unique sentinels, and accepts only the specified authorization or managed-
   fence SQLSTATEs. Constraint, network, TLS, timeout, or arbitrary function
   errors do not prove a fence. Any unexpected transactional success is rolled
   back and fails the migration.
   Exact effective sequence privileges are checked without consuming a value;
   if a prototype requires a nontransactional allocation probe, journal the
   allocation and apply the no-rewind rule. A migration-role positive control
   proves the endpoint and intended maintenance path remain usable.
5. In a new transaction, persists the canonical per-identity probe matrix,
   backend/session facts, catalog fence digest, and result as
   `fence_database_verified_retirement_pending`. No managed data, root-key,
   registry, or partition mutation is permitted before this phase. `migrate`
   returns the stable `credential_retirement_required` status. Only after this
   receipt commits may external credentials be retired, so a crash in an
   earlier phase can reconnect and repeat the direct probes.
6. The idempotent `verify-fence` command requires the journal-matching canonical
   preflight report plus its explicitly resupplied manifest, capacity, backup,
   preparation-plan, target connection, exact bound probe set, and versioned
   drain/retirement/external-writer evidence sets. The target and probe files
   provide secrets/transport only. Manifest, backup, preparation plan, and
   immutable descriptor digests must match the journal; a renewed capacity
   attestation is reported as an approval-invalidating delta rather than
   silently substituted. Repeated
   retirement files form a closed one-to-one mapping by
   probe/control ID for every `provider_version` strategy; missing, extra,
   duplicate, wrong-provider, or wrong-version entries fail. Native-role
   receipts are tool-generated and have no external retirement file. For a
   `provider_version` probe, it validates signed evidence that the exact bound
   immutable credential version was retired, then requires the old secret to
   reach the healthy endpoint but receive one of the explicitly supported
   authentication rejection codes. A substituted unbound secret cannot pass.
   For `postgres_role_nologin`, it acquires the WP0-defined cluster-scoped lock,
   revalidates the role OID/name and approved cross-database scope, and in one
   transaction records the retirement facts while executing
   `ALTER ROLE ... NOLOGIN PASSWORD NULL`; catalog state, not rejection of an
   unbound password, is authoritative. Do not change memberships. The receipt
   records only migration/probe ID, exact role identity, action, precondition
   fingerprint, scope-evidence digest, transaction facts, and the observed
   `rolcanlogin=false`/password-slot-absent booleans, never a password verifier.
   It then proves the old direct login is rejected.
   DNS, generic TLS, timeout, or network failures never count; WP0 may admit a
   credential-specific provider/TLS rejection only with a healthy endpoint
   control and an unambiguous signed mapping. The command proves no old sessions
   remain, revalidates the database fence digest and non-database writer
   controls, persists the evidence digest, and atomically advances to
   `fence_verified`. Cluster-global role changes are never inferred as
   reversible by a database restore, and rollback leaves retired credentials
   disabled until the separate preparation-role handoff. Lost COMMIT resolution
   uses the journal plus exact catalog/provider version facts; replay is
   idempotent.
7. Re-runs every mutable preflight check in a fresh authoritative read-only
   snapshot under the fence and recomputes the
   versioned preflight digest. Allowlist only the exact journaled database-local
   fence/session-drain deltas plus each bound provider-version retirement or
   exact-role NOLOGIN/password-null receipt produced by steps 2 through 6. A
   membership change, unplanned role attribute, or any other change in counts,
   checksums, topology, dependencies, capacity, manifest, roles, or privileges
   invalidates approval: write a new report/token and stop for renewed operator
   confirmation.
8. Persists the authoritative fenced digest and renewed confirmation requirement
   before global preparation.

If the fence cannot be independently proven, stop without touching managed data
or topology and leave the journaled fence phase for explicit resume/rollback.
From `fence_installed_unverified`, `resume` returns `writer_probe_required`; the
idempotent `verify-fence` must perform or revalidate steps 3 through 5, including
drain evidence, session termination, non-database writer checks, and direct
probes, and can do so only when the complete bound probe set is supplied. From
`fence_database_verified_retirement_pending`,
`resume` returns `credential_retirement_required`; only `fence_verified` may
enter authoritative preflight or global preparation. External credential
retirement before the database-probe receipt is an operator error: remain
fenced and either restore the recorded database ACL snapshot followed by the
V1 handoff, or reissue equivalent probe access and repeat verification. An
attestation or `SET ROLE` is never a substitute. Resume or rollback can restore
the recorded ACL state from any fence phase; a crash cannot leave an
unjournaled database fence. Add failpoints before and after each transaction,
authenticated probe, external attestation, and ambiguous COMMIT—not only at
later phase boundaries.

#### WP6 shared checkpoint protocol

Persist only validated phase boundaries:

- global_started and global_prepared.
- fence_installing, fence_installed_unverified,
  fence_database_verified_retirement_pending, and fence_verified.
- identity_backfill_started/complete, property_backfill_started/complete, and
  enforcement_key_swap_complete.
- Per graph: staging_created, copy_complete, validation_complete, and cutover.
- all_graphs_complete, v2_pending_acceptance, accepted, and cleanup_complete.
- rollback_started, per-graph rollback completion, and rolled_back.

Each checkpoint records the manifest and source-catalog checksums plus the
object identities, counts, and digests needed to prove it still applies.
Resume revalidates those facts before continuing. It never advances state just
because an object with an expected-looking name exists.

The copy strategy is selected from the WP0/rehearsal transaction and WAL
budgets. A graph may copy in one transaction, or in deterministic ID-keyset
chunks with a durable high-water mark. An incomplete chunk is rolled back; a
resumed chunked copy revalidates the completed range and uses an idempotent
target key. Do not introduce offset pagination or a resume marker that can skip
or duplicate sparse IDs.

Every chunk writes its idempotent DML and progress row atomically in the same
transaction, recording strategy/encoder version, keyset high-water,
source/target row counts, and deterministic chunk digest. Failpoints cover
mid-chunk, immediately before commit, ambiguous COMMIT, and post-commit before
client acknowledgement. Resume resolves durable rows and the progress digest
before deciding whether an idempotent chunk needs replay.

#### WP6.3 Global preparation

Global preparation is resumable; registry population is not one unbounded
transaction:

1. In a short transaction, create the remaining V2 runtime metadata and empty
   registry relations without adding the edge_identity-to-edge foreign key.
2. Populate identity and property claims in deterministic source-edge
   `(graph_id, id)` keyset chunks. Generate and validate every constraint claim
   for an edge atomically inside its source-edge chunk; never persist a property
   value in progress metadata. Commit idempotent chunk DML and its
   `(graph_id, id)` high-water/count/digest checkpoint together while the writer
   fence and V1 primary key remain authoritative. Never use OFFSET or a hash
   alone as a resume key.
3. Validate complete registry coverage, uniqueness, and source/target digests in
   a coherent snapshot. A missing/extra claim blocks the atomic phase.
4. In one separately budgeted atomic transaction, install the approved claim,
   delete, property, and immutability enforcement and transition the root from
   the V1 primary key to a validated unique/primary
   key on (id, graph_id, kind_id), using the exact drop/add choreography proven
   in WP0 and rehearsal. PostgreSQL does not support promoting a prebuilt
   partitioned index with ADD CONSTRAINT USING INDEX. Accept and preflight the
   recursive build/lock/disk cost unless another PG16/18-proven sequence is
   selected. Drop and rebind supported dependent objects in topological order.
   At no point may both the V1 key and the identity registry be ineffective.
5. Keep graph-local identity enforced by the populated registry primary key and
   claim/delete triggers during per-graph conversion. A populated foreign key
   to the partitioned edge root would prevent detaching a referenced V1 graph
   partition.
6. Install graph-safe cascade/helper functions with their graph-scoped
   translator call sites as the atomic SQL-compatibility unit defined by R2; no
   active runtime may observe mismatched signatures.
7. Capture all new object OIDs/names and mark global preparation complete.

The exact operation order and maximum atomic-phase duration/lock/WAL cost must be proven on
the frozen V1 fixture and restored production-scale copies. Never leave a
checkpoint claiming completion unless all objects and registry counts for that
checkpoint validate. The copy/checkpoint fields in the durable-state model are
mandatory, not optional implementation details.

#### WP6.4 Per-graph stage, validate, and cut over

Process graphs in deterministic graph-ID order. For each graph:

1. Revalidate its source catalog snapshot and ensure no prior completed graph
   has drifted.
2. Create a bounded, collision-free staging edge parent with the source
   columns, defaults, storage parameters, tablespace, and owner, partitioned by
   LIST(kind_id). Add and validate an exact graph_id check constraint so final
   attachment does not require an unbounded source scan.
3. Create leaves for the union of:
   - Kind IDs stored in V1 rows.
   - Edge kinds declared in the canonical manifest.
   Insert the matching dawgs_edge_kind_leaf expectations in the same staged
   phase, with migrated or declared provenance as appropriate.
4. Prebuild matching partitioned primary-key, endpoint, base-index, and only the
   measured non-unique managed property-index hierarchies with collision-free
   names. Do not recreate V1 unique expression indexes on individual leaves;
   the property registry is the graph-wide authority. Rehearsal must prove
   final attachment does not start an unplanned index build inside the cutover
   lock window.
5. Copy rows without changing edge IDs, graph IDs, kind IDs, endpoints,
   properties, or observable text values.
6. Route every row through an exact kind leaf; absence of a leaf is fatal.
7. Do not execute ordinary identity/property claim triggers during staging
   copy because the claims already describe the attached V1 rows. Install or
   inherit normal V2 enforcement only as part of the atomic cutover, then
   validate trigger attachment.
8. Analyze staging leaves.
9. Validate:
   - Total and per-kind counts.
   - Minimum and maximum IDs.
   - Endpoint and kind aggregates.
   - Exact bidirectional EXCEPT equality.
   - Deterministic streaming SHA-256 over ordered typed row values.
   - Identity and property registry ownership.
   - Parent/leaf/index catalog topology.
10. In one cutover transaction:
   - Verify the migration's deferred-FK/non-FK identity mechanism is active and
     no populated edge_identity foreign key can block the detach.
   - Add or preserve a validated graph-bound check on the legacy table so a
     pre-acceptance rollback can reattach it without an unexpected full scan.
   - Detach the V1 graph relation, then rename it using a recorded unique name.
   - Rename and attach the V2 graph parent as edge_<graph_id>.
   - Rename or attach indexes through the recorded mapping.
   - Restore owner, ACL, comments, and every supported dependency.
   - Validate topology, inherited enforcement triggers, and data again.
   - Persist graph completion, legacy names, counts, and checksums.
11. Restrict detached V1 objects to the migration role. Label them as stale
    rollback material, never as a live post-acceptance copy.

On resume, skip a completed graph only after validating its checkpoint,
catalog, manifest checksum, row checksums, and recorded object mapping.

The checksum format is a versioned protocol, not string concatenation. Order
rows by (graph_id, id); encode a format byte, per-field null tag, fixed-width
signed integers in one documented endian order, and length-prefixed UTF-8
bytes. Encode properties from one explicitly tested canonical JSONB
representation and prove identical output on PostgreSQL 16 and 18. The report
stores the encoder version. Bidirectional EXCEPT inside the same database
remains the authoritative pre-cutover equality check; the checksum supports
checkpoint, resume, and receipt comparison.

#### WP6.5 Final validation and pending acceptance

After every graph:

1. Validate the root hierarchy, graph parent bounds, kind leaf bounds, absence
   of DEFAULT leaves, index attachment/validity, triggers, and registries.
2. Verify every stored and manifest-declared kind has exactly one leaf.
3. Add and validate the edge_identity-to-edge foreign key, or finalize the
   approved equivalent cleanup mechanism, only after all V2 graph parents are
   attached. A migration test must prove that attempting the foreign key before
   detach is rejected and that the final constraint validates afterward.
   Remove migration-only claim-delete triggers after the final FK/equivalent
   cleanup path is active.
4. Reconcile graph-wide property constraint claims.
5. Preserve sequence ownership, increment, min/max, cache, cycle, grants, and
   effective next allocation. Never rewind the effective next ID.
6. Analyze all new relations.
7. Run read-only direct, Cypher, path, and graph-isolation smoke queries.
8. Close or invalidate migration worker connections and prepared statements
   that observed pre-cutover relation metadata. The target runtime starts with
   a fresh pool.
9. Record the detached-object retention and earliest cleanup deadline.
10. Persist the versioned validation receipt facts and digest in the database.
11. Commit the receipt with state v2_pending_acceptance. Application writers
    remain fenced, and the external report can be regenerated idempotently.

#### WP6.6 Acceptance

Acceptance is one explicit transaction:

1. Require the exact migration ID selected by `active_migration_id`, manifest
   checksum, catalog fingerprint, and validation digest. Authenticate the supplied target connection and require
   its session/current-user OID and name to match the preflighted target fact;
   never accept a role name supplied only as text at this stage.
2. Re-run fast topology, registry, sequence, and fence validation.
3. Record accepted_at and rollback_cutoff_at.
4. Persist the canonical acceptance facts and receipt digest with
   `post_acceptance_smoke_status=pending` and the authenticated target role
   OID/name, verified current database identity, and the existing identity epoch
   and rebind pointer. Those are epoch zero/null only for a never-restored first
   migration; acceptance never erases restore provenance. Point the layout at
   the migration's exact current legacy-cleanup map and clear both active and
   current-rollback migration pointers while setting `accepted_migration_id`
   to this exact scope as part of the state transition. Migration acceptance
   requires a null bootstrap-operation pointer.
5. Set state v2_accepted.
6. Apply the preflighted target ACL matrix while keeping every old V1 login
   revoked/retired. The target uses a distinct role OID and separately issued
   credentials; it receives required DML on the roots and every edge/node graph relation
   named by the driver, sequence usage, and EXECUTE on restricted registry/leaf
   functions, plus only the read access to edge_identity and layout/
   desired-topology metadata required by generated ID hydration and health. It
   receives no direct registry DML, no property-claim value access, and no broad
   schema CREATE. Provisioning applies the same owner/ACL policy to every future
   leaf.
7. Commit receipt, marker, and privilege handoff atomically.

Render the external receipt file from the durable row after commit; a report
file write failure is recoverable and idempotent. The runtime gate refuses
ordinary application operations with `acceptance_smoke_pending` until the raw-
pgx migrator `smoke` command (or the equivalent bootstrap continuation) runs a
minimal target-role write/read/delete test
and durably records `passed`, or records the approved empty-install privilege
fallback. A crash leaves `pending`; `smoke` is idempotent and may be rerun in
accepted state. A failed result is durable and requires forward repair followed
by a rerun, or restore/reconcile. Never relabel detached V1 tables as an
in-place rollback source.

For an installation with an existing graph and kind, the smoke uses a
prevalidated explicit edge ID and unique sentinel endpoints, an empty property
bag, and the existing graph parent. In one committed transaction it inserts,
reads and verifies tableoid/registries, then deletes and verifies cleanup. It
does not call nextval, change the edge sequence, or collide with property
constraints. If no materialized graph/kind exists, use an explicit
privilege-only fallback that verifies the target ACL/function matrix and V2
health without inventing application schema merely for smoke.
Traffic activation requires the durable passed/fallback receipt, not merely the
accepted marker.

#### WP6.7 Pre-acceptance rollback

Rollback is allowed only in v1_to_v2_in_progress or
v2_pending_acceptance while the writer fence is intact and no V2 application
write has been allowed.

It:

1. Validates the migration ID is the exact `active_migration_id`, plus manifest
   checksum, state, fence, and recorded object map.
2. Uses the recorded inverse dependency order. Disable or drop V2 claim
   triggers and registry foreign keys only where required for partition
   reversal, while the database remains fenced.
3. Reverses graph cutovers in reverse graph order and validates each restored
   V1 graph before checkpointing it.
4. Restores root keys, functions, triggers, owners, comments, supported
   dependencies, sequence attributes, and non-writer ACL metadata from the
   snapshot while retaining the application write fence. Stage and validate the
   exact final preparation-role/default-ACL matrix without granting it yet.
5. Removes only the recorded V2 staging, registry, and enforcement objects
   after their dependents have been restored; retain the migration audit
   record.
6. Restores the sequence effective next value without rewinding it below any
   allocation observed during migration.
7. Proves V1 data/catalog equivalence and the staged final ACL matrix, then in
   one database transaction returns the singleton to `v1`, clears
   `active_migration_id`, sets `current_rollback_migration_id` to this exact
   migration, records `rolled_back`, sets `rollback_handoff_status=pending`,
   and clears that row's current handoff-receipt pointer while retaining every
   database writer revoke/fence. Historical, preparation, and target roles
   remain unable to write.
8. Outside that transaction, reissue/deploy preparation credentials while
   traffic remains stopped. Then run resumable `complete-v1-handoff` with a
   separately authenticated preparation connection. It verifies the recorded
   role OID/name is the distinct preflighted preparation login, verifies newly
   issued sole-active credential/operator handoff evidence, current identity
   epoch, preparation artifact, and confirmation-token binding. In one
   transaction it applies the preparation role/default ACL matrix, appends the
   nonce/epoch-bound handoff receipt, points the migration row at that digest,
   removes the database fence, and marks handoff passed. It never re-enables a
   retired historical role or restores/snapshots its password;
   `postgres_role_nologin` remains NOLOGIN with no password. A lost COMMIT is
   resolved from state, grants, receipt digest, and current pointer; replay is
   idempotent. Resume V1 traffic only after the preparation client accepts the
   passed shape.

Any incomplete reverse operation remains a fenced mixed state and must be
resumed; it is never reported as a successful rollback.

Structural rollback may be complete with handoff pending, but the tool does not
report application-operational V1 or full logical equivalence until the atomic
handoff passes.

Logical equivalence means the V1 managed names, columns, defaults, constraints,
index semantics, functions, triggers, data, ACLs, comments, and supported
dependencies match the frozen contract. It explicitly permits the inert
migration-control namespace/history, new catalog OIDs/relfilenodes for rebuilt
objects, refreshed statistics, and a sequence effective-next value advanced by
the no-rewind rule, plus the preflighted preparation-role/credential handoff in
place of a retired historical credential. The preparation compatibility client
must accept the marked v1 state. Tests and reports compare these allowlisted
differences rather than claiming byte-for-byte catalog identity.

#### WP6.8 Cleanup

Cleanup is allowed only after acceptance, a current-scope/current-identity-
epoch smoke pointer whose receipt is `passed`/approved `fallback`, the configured
soak, an externally identified fresh backup plus successful restore rehearsal,
operator approval, and fleet record update. Pending, failed, missing, or pre-
rebind smoke can never authorize cleanup. The tool verifies database state/
digests; backup
existence and provider restore evidence are supplied as a versioned operator
attestation because PostgreSQL cannot prove them universally. Before dropping
anything, persist backup ID, backup/restore evidence digests, restored database
identity, soak start/duration, approval identity/time, and fleet-record receipt
and the exact current smoke receipt/rebind/identity-epoch digests in the
canonical cleanup facts/token. The supplied migration ID must equal the
layout's `accepted_migration_id`; a bootstrap-scoped database has no cleanup
command. At acceptance, persist an original cleanup map
whose entries bind a logical object ID to exact qualified name and current OID,
relkind, OID-independent catalog/dependency fingerprint, and immutable detached-
data count/checksum. The layout's current-cleanup-map digest points to that map
or to a later accepted-restore rebind map for the current identity epoch.
Cleanup resolves only that pointed map, revalidates every OID, name,
fingerprint, dependency, count, and checksum, and drops those exact legacy
objects. A stale/missing pointer, OID reuse, extra dependency, or any mismatch
refuses cleanup. It never uses a name pattern or broad schema drop. It leaves
accepted V2 state and the diagnostic marker intact.

Persist canonical cleanup facts and their digest in the same transaction that
drops the recorded objects, advances cleanup state, and points the layout at
the canonical empty/current cleanup map. Generate or regenerate the external
receipt file from that durable row.

Restore/PITR behavior is explicit:

| Restored state | Permitted recovery |
|---|---|
| Pre-fence V1 | Classify as V1; rerun preliminary preflight with new identity |
| fence_installing or fence_installed_unverified | Remain fenced; supply the complete bound writer-probe set to verify-fence, or restore its recorded ACL snapshot |
| fence_database_verified_retirement_pending | Remain fenced; validate the probe receipt, retire credentials, and supply/replay drain/retirement evidence to verify-fence, or roll back |
| fence_verified or any registry/cutover checkpoint | Remain fenced; validate durable chunks/object maps, then resume or pre-acceptance rollback |
| v2_pending_acceptance | Remain fenced; validate, accept, or pre-acceptance rollback |
| v1_rolled_back with handoff pending/passed | Unchanged original identity may complete/validate handoff; every detectable same-cluster restore must rebind and redo handoff; cross-cluster rebind is unsupported |
| v2_accepted with smoke pending/failed | No in-place rollback; rerun smoke after forward repair or restore/reconcile |
| v2_accepted with smoke passed, before or after cleanup | Validate accepted V2; forward repair or restore/PITR only |

A changed current-identity tuple invalidates every old confirmation token.
In-progress rebind supports a changed database OID/name, restore incarnation,
or both only within the same stable PostgreSQL cluster/resource identity. This
includes detectable same-cluster in-place PITR. `rebind-restore` then requires canonical restore
evidence, exact migration/manifest/source/checkpoint fingerprints, and exact
matches for every writer role OID/name/fingerprint, provider credential-version
fact, cluster-scope retirement receipt, current database fence ACL/guard, and
the complete bound writer-probe and external-control IDs.

The restore evidence authorizes exactly one old-to-new database target mapping
inside that cluster. Rebinding writer probes is phase-aware:

- While an exact bound credential is demonstrably live, every supplied file
  retains the preflighted immutable probe/role/credential/strategy digest while
  its nonpersisted transport envelope targets the candidate database and
  directly observes its new current identity. A file that still reaches the
  source/wrong database is rejected.
- Once exact retirement is demonstrably complete—even if a crash or ambiguous
  commit left durable phase at
  `fence_database_verified_retirement_pending`—the credential need not and must
  not be made live for a probe. The tool uses durable immutable descriptors plus
  current-cluster catalog proof of the exact native role's `NOLOGIN PASSWORD
  NULL` state, or fresh signed provider-retirement evidence for the exact
  credential/version. It also proves the operator's healthy candidate endpoint,
  current identity, and database-fence digest. Retired secret probe files are
  optional and never treated as an authentication requirement. If neither live
  direct authentication nor exact catalog/provider retirement can be proven,
  rebind fails with `writer_credential_state_unknown`.

Each external control supplies a signed old-to-new target mapping or renewed
attestation with identical stable control and immutable credential facts. No
control may be added, removed, or silently retargeted. In one transaction,
rebind stores the authoritative current probe/retirement/external-control-set
binding digests in the nonce receipt, appends that `rebind_restore` receipt,
records the new current database identity, increments the identity epoch, and
updates the current rebind pointer without changing stable installation
identity. All subsequent verify-fence/resume actions compare against the
current receipt binding, not the obsolete source-database binding; resume is
forbidden before that exact receipt.
Ambiguous COMMIT resolves from the nonce/pointer/current identity. A different
cluster identity returns stable
`cross_cluster_in_progress_restore_unsupported`: use a pre-fence V1 backup and
rerun preflight instead. It must never remap or trust stale in-progress writer/
retirement facts.

For a `v1_rolled_back` receipt scope, only the unchanged original database (or a
provider-attested non-restore HA failover that preserves the current
incarnation) keeps recorded pending/passed behavior, and only while all
structure, grants, role facts, and the passed handoff receipt pointer/identity
epoch still match. Every detectable same-cluster restore changes database OID/
name, restore incarnation, or both and uses
`rebind-restore --migration-id`: it validates the historical migration and
retirement receipts, revokes any copied preparation grants/default ACLs,
appends the nonce-bound rebind receipt, advances the identity epoch/pointer, and
sets `rollback_handoff_status=pending` and clears the current rollback-handoff
pointer atomically. A newly authenticated preparation connection must then
rerun `complete-v1-handoff` with a new confirmation nonce; its new epoch-bound
receipt and pointer are committed with the grants/passed status. A copied
passed receipt/grant never reopens the clone. Cross-cluster rolled-back rebind
returns stable `cross_cluster_rolled_back_restore_unsupported`; use a pre-fence
V1 backup and rerun preflight. The preparation compatibility client applies the
current database-identity gate before accepting either handoff substate.

For accepted V2, cross-cluster rebind is supported because migration cannot
resume. The command accepts exactly one generalized receipt scope: its migration
ID or bootstrap operation ID. It validates the
immutable initialization/acceptance receipt and topology, authenticates the
current target connection, requires the same non-owner/distinct-from-owner,
operator identity policy, and, for migration scope only, the preflighted
preparation/historical-writer distinctness policy. In one
transaction applies the restore-evidence role map, restores the exact managed-
object owner/default-ACL/final privilege matrix, removes stale pre-restore
target/writer grants, rejects dangling or colliding OID/name principals,
and reconciles the current target binding. For accepted pre-cleanup state, it
also resolves each original legacy logical object by exact qualified name,
requires the OID-independent catalog/dependency and detached-data fingerprints,
rejects a missing/extra/colliding object, and records an authoritative map to
the restored current OIDs. For post-cleanup state it instead proves the exact
legacy objects remain absent and records the canonical empty map. Bootstrap
scope likewise proves that no legacy managed object exists and records the
current canonical empty map; it is never treated as a migration cleanup phase.

The same transaction appends a `rebind_restore` receipt whose attempt ID is the
confirmation nonce, stores the new current database identity, increments the
identity epoch, points to that exact rebind receipt and its current cleanup-map
digest, sets smoke back to pending, and clears/replaces the current smoke-
receipt digest without rewriting historical facts. Unknown COMMIT and
replay resolve the exact nonce plus layout pointer; an older receipt or
`created_at` ordering can never win. Runtime remains closed until a new scope-
bound `smoke` attempt
through that target records passed/fallback; the pre-restore role's smoke
receipt is audit history, not an operational gate. The command does not reopen
migration or rollback. Test every row of this matrix for both migration- and
fresh-bootstrap-scoped accepted databases.

WP6 tests:

- Empty V1 and empty graph.
- Multiple graphs/kinds, severe skew, interleaved IDs, and declared-empty kinds.
- Property indexes and graph-wide edge constraints.
- Identity/property collisions, missing kind rows, sequence gaps, empty
  sequences, custom increments/cache/bounds/cycle/ACL.
- Owners, grants, comments, and supported dependencies.
- Every unsupported dependency class rejected without mutation.
- Missing, stale, unverified, wrong-installation, and changed pre-migration
  backup evidence rejected before journaling/fencing; exact approved evidence
  digest survives resume.
- Preparation-role/credential-plan omission, stale cluster or role binding,
  non-dormant role, alternate credential path, unknown field/version, and role
  reuse as owner/operator/writer/target are rejected. Handoff proves the new
  version is sole-active, rejects stale/replayed issuance evidence and a
  mismatched preparation artifact, and never persists a secret or verifier.
- Long transactions, lock timeout, late writer, directly authenticated fresh
  probes before retirement, credential-specific rejection plus catalog/signed
  proof afterward, session-user-sensitive functions, role/database defaults,
  missing probe coverage, and lost control connection.
- Crash before and after `fence_database_verified_retirement_pending`, refusal of
  premature credential retirement without reissued probe access, retirement-
  evidence replay, and proof that no PostgreSQL ACL change predates the
  durable journal.
- Crash/restore after each native or provider credential is provably retired
  but before `fence_verified` commits; rebind resolves exact retirement without
  reactivating it and rejects unknown or partially evidenced credential state.
- Failpoint after every durable global and per-graph checkpoint.
- Resume with identical manifest and rejection with changed manifest.
- Pre-acceptance rollback logical-equivalence with only the documented
  allowlisted differences.
- Rollback crash while handoff is pending, preparation-client refusal, and
  ambiguous-COMMIT/idempotent retry of the final `complete-v1-handoff` grant and
  passed-status transaction.
- The unchanged original rolled-back database preserves a passed handoff pointer
  only when facts match. Restoring its backup anywhere detectable in the same
  cluster revokes copied preparation grants, clears the pointer, requires a
  second nonce/epoch-bound handoff, and covers unknown COMMIT and report
  regeneration; cross-cluster rolled-back rebind is refused.
- Two migrate-to-rollback-to-handoff cycles preserve append-only history while
  each new start selects a new active scope without resetting identity epoch;
  after restore/rebind, the second start appends a scope-local identity-binding
  carry-forward receipt and repoints it atomically. Handoff, restore, current and
  explicitly historical report regeneration, and preparation-client readiness
  never select an older passed row by timestamp.
- Acceptance refusal on stale digest and rollback refusal after acceptance.
- Crash after acceptance before/during smoke, durable pending/failed/passed
  results, and idempotent smoke retry.
- Cleanup deletes only the current pointer's exact legacy objects. An accepted
  pre-cleanup restore must rebind an OID-independent logical map to current
  OIDs, pass new target smoke, and clean those objects; stale OIDs, OID reuse,
  name/fingerprint/dependency drift, copied maps, and pending/failed/missing or
  stale pre-rebind smoke receipts are rejected.
- Accepted and post-cleanup V2 backups restore into a different database OID and
  pass OID-independent fingerprint, registry, sequence, ACL, leaf, rebind, and
  new-target smoke validation; include accepted fresh-bootstrap backups and
  rehearse the supported PITR state matrix.
- Same-cluster in-progress database-OID and incarnation-only rebinds with exact
  writer/fence facts,
  correctly retargeted transport-only probe envelopes before retirement,
  catalog/signed retirement proof without credential reactivation afterward,
  and signed external-control mappings; reject probes that still reach the
  source/wrong database, changed immutable descriptors, changed control sets,
  changed-cluster restore, and every role/provider/fence mismatch. Accepted
  cross-cluster role remap rejects stale grants and OID/name collisions for
  both migration and bootstrap receipt scopes.
- CLI attestation inputs, token issue/expiry/action/replay/restore invalidation,
  read-only guarantees, exit codes, JSON compatibility, and redaction.

Exit gate:

- The built binary completes status, preflight/confirm, migrate/verify-fence/
  resume, validate, accept/smoke, rollback/complete-v1-handoff, rebind-restore,
  health, and cleanup against the frozen V1 fixture on PostgreSQL 16 and 18.
- Every injected failure can resume or roll back according to state.
- Post-acceptance downgrade is categorically rejected.

### WP7: Operational tools, observability, and artifacts

Purpose: remove graph-heap assumptions from all supported tooling before the
transition release.

Startup and bootstrap boundary:

1. Add a public read-only PostgreSQL runtime-readiness check (or optional
   interface implemented by `*pg.Driver`) that returns the same typed layout/
   smoke/target-identity errors as the lowest runtime gate without asserting
   schema. Every tool that currently calls `AssertSchema` and then operates must
   invoke it first. An operator or inherited/SET ROLE session therefore fails
   even against accepted V2. On operational V2, the later `AssertSchema` is
   graph/kind/index reconciliation through restricted owner routines, never
   layout bootstrap.
2. `cmd/retriever`/`retriever/load.go` accepts only its existing target-role
   connection to an accepted, smoke-passed V2 database. It carries no operator
   credential and fails before manifest assertion on uninitialized, V1, mixed,
   or pending state with bootstrap/migration guidance. A restore/load into a
   fresh database runs the separate operator bootstrap/handoff first.
3. `cmd/graphbench/postgres.go`, `cmd/benchmark/main.go`, and
   `cmd/plancorpus/capture.go` likewise treat their existing PostgreSQL DSN as
   an accepted target role, run the readiness check before corpus schema
   reconciliation, and never accept or retain an operator secret. Dedicated
   database creation belongs to `benchmark_transition`/CI, which runs operator
   `AssertSchema` plus `smoke --bootstrap`, closes operator resources, and then
   launches the tool with only the target DSN.
4. `tools/dawgrun/pkg/commands/db.go` treats `open --init-graph` as permission to
   create a missing graph only inside operational V2. A layout-state or
   `schema_uninitialized` result from `SetDefaultGraph` is returned with
   migrator/bootstrap guidance and must not fall through to `AssertSchema`.
   Dawgrun never bootstraps the PostgreSQL layout. `graph/switch.go` remains a
   policy-free delegate.
5. Ordinary integration tests receive `CONNECTION_STRING` for an already
   accepted, smoke-passed non-owner target database. Their existing
   `AssertSchema` calls reconcile test graphs only. PostgreSQL CI uses a
   separate operator during setup to bootstrap/handoff, closes it, and exposes
   only the target DSN to `make test_all`; local integration docs provide the
   same preparation command. Fresh-bootstrap, state, and role tests live only
   in the disposable `pg_physical`/migration harnesses with their admin inputs.
6. Add guard tests for every named caller proving an operator DSN cannot cause
   an implicit tool bootstrap, an uninitialized/pending database fails before
   schema mutation, and accepted-target reconciliation still works. Update the
   corresponding CLI help, READMEs, workflow, and Make setup target.

Retriever:

1. Make openDatabase surface typed migration-required errors.
2. Replace simple to_regclass graph discovery with accepted-V2 validation:
   edge_<graph> relkind is partitioned, every child is a LIST(kind_id) leaf,
   there is no DEFAULT or duplicate bound, stored kinds have leaves, and
   partitioned indexes are valid.
3. Add layout version, topology fingerprint, and leaf count to benchmark/report
   metadata.
4. Test keyset scans with globally interleaved IDs across leaves for strict
   ordering, no duplication, and no omission.
5. Document that Retriever output is a logical backup but not the canonical
   application manifest.
6. Preserve the final V1-compatible Retriever artifact for pre-migration
   backup. The transition Retriever is V2-only.

GraphBench:

1. Sum heap/index/TOAST size over physical leaves from pg_partition_tree.
   A partitioned graph parent has no representative heap size.
2. Enumerate leaves for deterministic VACUUM ANALYZE and record analyze
   freshness.
3. Add LayoutVersion, EdgeLeafCount, and TopologyFingerprint to PostgreSQL
   environment records and bump artifact/reference versions as needed.
4. Prove the selected global or graph-scoped reset recursively empties all
   leaves and corresponding registry claims without touching unrelated state.
5. Replace text/name regex assertions with structured
   EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) checks using the canonical leaf-name
   parser when asserting executed leaves.
6. Add plan gates for one-kind, multi-kind, kind-agnostic, and ID-hydration
   workloads.
7. Add an explicit V1-to-V2 layout-transition comparison mode that requires
   identical logical fixture checksums and the expected layout pair. Keep
   ordinary environment comparison strict.
8. Never compare V1 graph-parent heap bytes directly with a V2 parent that has
   no heap.

Plan corpus:

1. Record layout version and topology fingerprint.
2. Record physical edge leaf relations touched from structured JSON plans.
3. Report one-, multi-, and all-leaf pruning summaries.
4. Prove the selected reset clears all kind leaves and corresponding registry
   claims.
5. Refresh environment-specific captures as reviewed evidence, not semantic
   goldens.

Export and maintenance:

1. Convert cmd/export away from a direct unguarded pg.NewDriver path or invoke
   the same layout validation explicitly.
2. Document the transition export as V2-only and preserve the final V1 export
   artifact for pre-migration extraction.
3. Add nested-tree tests to OptimizeStorage and logical table size reporting.

Benchmark and harness tools:

1. Update `cmd/benchmark` and `tools/dawgrun`, and implement the currently empty
   `cmd/benchdiff` source directory. They surface the
   same runtime gate and record layout version/fingerprint, graph/kind/leaf/row
   counts, PostgreSQL version and relevant configuration, dataset seed, and
   constraint cardinality. They also consume one canonical versioned case
   manifest that fixes protocol/case IDs, logical operations and query
   semantics, parameter distributions and seed, warmup/sample ordering,
   repetitions, concurrency, timeouts, correctness checks, and metric units.
   Reports carry its digest plus any approved layout-specific adapter version.
   Benchdiff rejects missing fields, unequal case-manifest digests, incompatible
   adapter mappings, or unequal units rather than forming ratios across unlike
   work.
2. Add a `benchmark_transition` harness that creates two disposable databases
   from the same frozen, deterministically seeded V1 snapshot. Keep one V1 and
   take the clone through preliminary preflight, fence installation,
   verify-fence, authoritative fenced-report review, renewed resume
   confirmation, resume/migrate-to-pending, validate, accept, and target-role
   smoke to operational V2. Close operator resources and launch the target
   artifact with only its target DSN. Run the frozen version-pinned V1 benchmark
   artifact against the former and the target artifact against the latter. Require equal
   pre-migration logical fixture checksums before benchdiff. Provision/remap a
   distinct installation-dedicated V1 writer login/credential version for each
   database, plus separate operator and target roles and a randomized
   installation-dedicated `NOLOGIN PASSWORD NULL` preparation role for the
   migration clone. Generate its scoped preparation-credential plan and all
   later evidence through the normal schemas, and delete only the exact
   identity-guarded disposable roles/artifacts during harness cleanup;
   alternatively use independent clusters. Retiring the clone's writer must
   not alter the comparator's login, memberships, or ACLs. Run repeatable,
   counterbalanced V1/V2 samples rather than relying on a single V1-before-
   migration capture. One runtime binary never opens both layouts, and the
   frozen V1 artifact/adapter must emit the versioned metadata envelope accepted
   by benchdiff.
3. `quality_bench` and transition benchmarking use dedicated benchmark
   databases/DSNs that may be wiped; never the ordinary application or shared
   semantic-test database.
4. Keep `cmd/benchmark` separate from GraphBench but apply the same topology,
   warmup, repetition, and redaction protocol.

Observability:

1. Separate cheap constructor/runtime health (marker plus bounded catalog and
   digest facts) from deep `validate`; never scan all claims on every open.
2. Emit structured, non-sensitive events/metrics for layout error code and
   fingerprint, leaf ensure result/latency, schema-lock wait, registry conflict/
   retry/deadlock/orphan checks, planned/executed leaves, migration phase/rate/
   heartbeat, WAL/temp/capacity headroom, registry bloat/autovacuum, and sequence
   health.
3. Tie pause/abort thresholds and status fields to WP0 budgets. Do not use graph,
   kind, property values, raw relation names, DSNs, or other high-cardinality or
   sensitive data as metric labels.

Build and release:

1. Add Make targets:
   - prepare_test_pg.
   - build_pg_migrate.
   - test_migration.
   - test_pg_physical.
   - benchmark_transition.
   - migration_preflight_fixture.
   - migration_artifact.
   Add all seven to .PHONY and help, and make clean remove only their known
   generated artifact paths.
2. Build dist/dawgs-pg-migrate with CGO_ENABLED=0, -trimpath, embedded Go build
   information, source revision, and a SHA-256 file.
3. Assign explicit ownership for publishing and retaining the binary, checksum,
   module files, fixture, runbook, report schemas, supported PostgreSQL
   versions, release date, and EOL date.

Exit gate:

- All tools recognize and report V2 correctly.
- Performance artifacts cannot confuse V1 and V2 environments.
- The exact migrator binary and immutable metadata are reproducibly built and
  retained by the release system.

### WP8: Test, CI, performance, and documentation gates

Purpose: combine semantic, physical, operational, and performance evidence into
one activation decision.

#### Unit and formatter tests

Add or extend:

- drivers/pg/model tests for parent/leaf names and parsing.
- drivers/pg/query/format_test.go for graph parent/leaf DDL, direct
  update/delete, endpoint upsert, and registry property upsert.
- drivers/pg/internal/schema workspace tests for helper signatures,
  graph-aware cascade, registry hydration, and bootstrap/migration definition
  equality; query tests prove the obsolete schema embeds are gone.
- drivers/pg/batch_test.go for multi-kind ensure, graph switching, alignment,
  retryable failure, and dynamic kinds.
- translate/create_test.go for RequiredEdgeKindIDs.
- translate/semantic_drift_test.go for immutable edge kinds.
- translate/graph_scope_test.go for edge update/delete joins, graph-safe
  helpers, path hydration, and absence of executable unscoped root references.
- Existing translate/function_test.go, optimizer_safety_test.go,
  predicate_test.go, and create_test.go hard-coded root/helper expectations,
  including shortest-path SQL-valued parameters and optimizer-lowering safety.
- query/v2/backend_test.go expectations for graph-scoped persistent relations,
  schema-qualified root sequences, and graph-aware helper signatures.
- Catalog decoder, classifier, state-machine, manifest, report, confirmation,
  cache publication, bootstrap receipt/idempotency, connection-identity gate,
  and migration failpoint tests.
- Stable DriverName pg, pg.NewDriver signature, package path, URL handling, and
  DSN behavior.
- drivers/pg/pg.go pool-hook/guard traces: rejected V1, uninitialized, pending,
  operator, and wrong-role startup issue only allowed classifier/identity SQL;
  prebootstrap release does not churn; accepted target sessions load only
  `public.*` composites; reconnect/fingerprint changes reload; caller hooks are
  preserved; hostile search-path lookalike types are never registered.

#### Generated and mutation fixtures

Add source comments to applicable existing translation cases and regenerate the
entire directory, with particular review of `create.sql`, `delete.sql`,
`update.sql`, `shortest_paths.sql`, `pattern_binding.sql`,
`pattern_expansion.sql`, `pattern_rewriting.sql`, and
`stepwise_traversal.sql`. `reconciliation.sql` and `post_processing.sql` do not
exist and are not planned artifacts.

Add shared mutation/source cases with at least three relationship kinds and
direction, property, and type decoys. Keep graph decoys in the PostgreSQL-only
multigraph isolation suite because Neo4j treats WithGraph as a no-op. Shared
expected results remain backend-equivalent. Parser-accepted relationship label
mutation rejections belong in PostgreSQL translator negative tests.
Use existing `integration/testdata/cases/update_inline.json` and
`delete_inline.json`, and add a new
`integration/testdata/templates/relationship_mutation_shapes.json` for the
direction/property/type decoys.

#### Shared integration coverage

Add to existing templates and direct-write suites:

- Same endpoints across several relationship kinds.
- Typed property update/delete with exact survivors.
- Read-then-create/update/delete boundaries.
- Default-graph path and startNode/endNode semantics.
- A dedicated Go integration test whose asserted schema intentionally omits
  three edge kinds, then first introduces one each through direct, batch, and
  Cypher writes. A generated template is insufficient because the harness
  pre-asserts fixture kinds.

Do not add PostgreSQL-specific skips or expected values to shared cases.

#### PostgreSQL-only integration coverage

Add:

- pgsql_edge_kind_layout_test.go:
  two graphs by three kinds, pg_partition_tree, pg_get_expr bounds, and
  tableoid routing.
- pgsql_layout_gate_test.go:
  every state, drift shape, direct-constructor guard, bootstrap, accepted-state
  operator/wrong-role refusal, inherited or startup SET ROLE refusal, backend
  reconnect recheck, and exact target-role acceptance.
- pgsql_edge_identity_test.go:
  graph-local duplicate rejection, cross-graph allowance, immutable keys,
  update/delete/hydration and startNode/endNode isolation with colliding IDs,
  rollback, and cascade.
- pgsql_edge_property_constraint_test.go:
  direct/batch/Cypher conflicts across kinds, text/null behavior, lifecycle,
  backfill, and concurrent races.
- pgsql_edge_partition_pruning_test.go:
  EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) for one-, multi-, all-kind, mutation,
  and hydration plans. Execute mutation plans only inside rollback-only
  fixtures. Assert planned, removed, and executed leaf sets plus correctness,
  not exact node shapes or version-identical index choices.

Extend:

- pgsql_delete_cascade_test.go for child-partition deletes and colliding IDs.
- wipe graph tests for leaves and registries.
- optimize tests for nested partition trees.
- property plan tests for physical leaf names.
- `pgsql_aggregate_traversal_plan_test.go` for guarded direct translation,
  graph-parent joins, qualification, and EXPLAIN; audit existing property/count
  plan suites for physical leaf/index expectation changes.
- layout reconciliation tests that repair a deliberately invalid canonical
  managed index during explicit assertion and reject wrong-lineage/unmanaged
  lookalikes.
- Add new `docs/edge_kind_partitioning_traceability.md`, mapping each locked
  contract and stable edge-kind case ID to unit, shared-semantic, PostgreSQL
  physical, migration, and operational evidence.

State/classifier/bootstrap, drift/repair, ACL/role/SECURITY DEFINER, and crash
tests are destructive physical tests. Run them, as well as migration tests, in
admin-created disposable databases with randomized roles and exact
identity-guarded cleanup. The ordinary application `CONNECTION_STRING` is only
for backend-equivalent semantic tests against the prebootstrapped accepted
target role; its harness cleanup cannot safely bootstrap or reset catalog drift.
Use a distinct `pg_physical` build tag and `test_pg_physical` Make target with
`PG_PHYSICAL_TEST_CONNECTION_STRING` pointing to an admin/coordinator database.
Neither `make test_all` nor `quality_backend` includes that tag.

#### Migration compatibility matrix

Exercise:

| Runtime/tool | V1 | In progress | Pending | Accepted V2 |
|---|---|---|---|---|
| Preparation V1 compatibility client | Accept | Refuse | Refuse | Refuse |
| Transition runtime | Migration required | Refuse/resume guidance | Refuse/accept guidance | Accept only exact target identity with smoke passed |
| Migrator | Inspect/migrate | Resume/rollback | Validate/accept/rollback | Validate/health/cleanup |
| Historical pre-marker V1 client | May operate before maintenance; terminate and revoke before migration | Blocked by fence | Blocked by fence | Blocked by revoked legacy role |

The preparation client accepts only the exact `v1_rolled_back` inert-control
shape with handoff passed. It refuses handoff pending while the fence is active;
other mixed residual objects remain drift.

#### Pruning and performance matrix

At minimum run 1 graph by 1 kind, 1 by 10, 1 by 30, several graphs
by 30 kinds, and the approved maximum topology, each with fixed published row
counts, seeds, uniform/skew distributions, and constraint counts 0/1/many.
Record hardware/provider class, PostgreSQL settings/extensions, cold/warm-cache
protocol, analyze state, batch sizes, repetitions, medians/p95/confidence, and
both absolute SLOs and V1 regression ratios. Force custom and generic plans
explicitly rather than relying on ambient plan-cache choice. Measure:

- Literal one-kind and multi-kind predicates.
- Custom and generic parameter plans.
- No-kind scans.
- ID-only lookup/hydration with registry assistance.
- Graph/kind cardinality, leaf count, and severe row skew.
- Direct, batch, and Cypher write throughput.
- Registry trigger overhead and hot-value contention.
- Automatic and explicit IDs; single and varied batch sizes; mass
  update/delete, cascade, wipe, constraint backfill, registry bloat, and vacuum.
- Planning time, catalog/relcache load, autovacuum behavior, and connection
  startup.
- Migration elapsed time, lock duration, WAL, temporary disk, and validation
  cost.
- Total relations and indexes, catalog/relcache memory, and
  `max_locks_per_transaction` headroom—not leaf count alone.

Acceptance thresholds are the budgets approved in WP0. Index tuning happens
only after correctness and measured plan evidence; do not prematurely create
different indexes per kind. Compare the correctness-release indexes with
leaf-aware candidates such as start_id INCLUDE (id, end_id), end_id INCLUDE
(id, start_id), and removal or replacement of indexes led by kind_id, which is
constant inside a leaf.

#### CI

1. Keep the PostgreSQL 16/18 matrix established in WP0 and assert the actual
   server_version_num in every job.
2. Keep pure, untagged unit tests under make test. The existing COVERPKG
   includes new migration packages, so unit-test manifest, state, report,
   checkpoint, confirmation, classifier, and checksum logic rather than
   excluding migrator code or lowering overall coverage.
3. Keep backend-equivalent V2 semantics under the existing manual_integration
   convention. Run destructive PostgreSQL physical suites through the same
   disposable-database/admin coordinator model as migration tests. PostgreSQL
   semantic jobs first run `prepare_test_pg` with separate operator and target
   identities, close operator resources, and expose only the accepted non-owner
   target as `CONNECTION_STRING`.
4. Add migration_integration as a distinct build tag and test_migration target.
   Run it with -race -count=1 -p=1 -parallel=1 against an admin/coordinator
   MIGRATION_TEST_CONNECTION_STRING; cross-cluster accepted-restore cases also
   require the independent RESTORE_TARGET_TEST_CONNECTION_STRING coordinator.
5. The migration harness creates a uniquely named disposable database and
   randomized old/target/migration roles plus an installation-dedicated
   `NOLOGIN PASSWORD NULL` preparation role per test or suite. It generates the
   scoped preparation plan/evidence, verifies target and preparation identities,
   and drops only those exact resources. It never runs failpoint, ACL, role, or
   crash tests against the ordinary application CONNECTION_STRING.
   Cross-cluster accepted-restore cases additionally require an independent
   `RESTORE_TARGET_TEST_CONNECTION_STRING` coordinator, assert its PostgreSQL
   system/cluster identity differs from the source, and create/drop exact roles
   and databases there. A second database on the source cluster is not a
   substitute.
6. Add the PostgreSQL 16/18 migration jobs when WP6b lands.
   Add matching `pg_physical` disposable-database jobs on both majors when the
   first destructive physical test lands.
7. Smoke-test the built migrator binary, not only package APIs, when WP7
   packaging lands.
8. Preserve make test_update followed by the clean-worktree generated-case
   check.
9. Verify the frozen V1 schema, seed, and metadata fixture SHA-256 values in CI.
10. Add an executable compatibility harness covering the preparation module
    client, transition driver, and migrator across V1, in-progress, pending,
    and accepted V2.
11. Run race tests for layout, migration, and command packages.
12. Keep Neo4j shared semantic integration green.

#### Documentation

Update:

- README.md for V2-only runtime and migration-required startup.
- docs/development.md for disposable migration testing.
- docs/postgresql_translation.md for graph/kind pruning and supported versions.
- Retriever, GraphBench, plan corpus, export, benchmark/benchdiff, and dawgrun
  READMEs or usage documentation.
- The pull request template for topology, migration, PostgreSQL-version,
  corpus, and operator-documentation evidence.

Add:

- Operator migration runbook.
- Manifest and report schema reference.
- Writer-fence and role handoff procedure.
- Wave checklist.
- Fleet-ledger template.
- Rollback-cutoff and cleanup receipts.
- V1 EOL and late-upgrade archive policy.

Exit gate:

- All correctness, migration, plan, performance, and documentation evidence is
  reviewed for PostgreSQL 16 and 18 plus every other major declared supported
  by the WP0 ADR.
- The shared Neo4j suite passes without PostgreSQL-specific semantic changes.
- No critical or unbounded performance regression remains.

### WP9: Activation release and fleet rollout

Purpose: ship one coherent transition and move every supported installation.

#### Activation change

Activation is a release promotion, not an untested storage-policy flip. The
real V2 bootstrap and runtime policy from WP1-WP5 must already have passed the
full suite on the unreleased transition stack. The final activation PR/release
is intentionally small:

1. Verify operator-authenticated fresh AssertSchema bootstraps V2 only, the
   two-connection target handoff/smoke completes durably, and ordinary target
   runtime accepts only v2_accepted with smoke/fallback passed and exact
   recorded session/current-user identity.
2. Remove any unexported development-only scaffolding without changing the
   tested policy. There is no public layout flag to remove or flip.
3. Ship the exact dawgs-pg-migrate artifact, checksums, runbook, schemas, and
   release notes with the runtime.
4. Verify package and distribution systems make the migrator available before
   an installation can receive the V2-only runtime.
5. Refuse release if any WP0-WP8 gate or rollback rehearsal is incomplete.

#### Fleet ledger

Maintain an authoritative ledger outside this repository; keep only its
template here. Each supported installation records:

- Installation and database identity.
- Business and technical owners.
- DAWGS module, consuming application artifact, and PostgreSQL versions.
- Graph/kind/leaf and size inventory.
- Canonical manifest checksum.
- Backup and restore rehearsal.
- Preflight report and blockers.
- Scheduled wave and maintenance window.
- Migration ID and artifact checksum.
- Validation and acceptance digests.
- Rollback cutoff.
- Target-runtime smoke result.
- Soak and monitoring result.
- Fresh backup.
- Legacy cleanup receipt.
- Final V2 health fingerprint or formal decommission record.

An unknown, silent, or unreachable installation remains open and blocks fleet
retirement.

#### Rehearsal

Before production:

1. Restore representative small, large, highly skewed, high-kind-count, custom
   ACL, and dependency-bearing backups into isolated environments.
2. Run the exact release artifact and canonical manifest.
3. Measure time, lock, disk, WAL, validation, and rollback.
4. Execute full pre-acceptance rollback at least once per supported PostgreSQL
   version and deployment architecture.
5. Execute acceptance, target-role smoke, backup, and cleanup on separate
   rehearsals.
6. Restore migration-scoped accepted pre-cleanup/post-cleanup backups and a
   fresh-bootstrap-scoped accepted backup into new database identities; run
   scope-aware `rebind-restore`, require a new target smoke, and exercise the
   supported PITR state matrix. For pre-cleanup restore, execute cleanup through
   the rebound current-OID map.
7. Validate handoff-passed/pending behavior on the unchanged original database,
   then restore both rolled-back V1 backups to a detectable same-cluster current
   identity; prove copied grants stay closed, rebind and complete a new handoff,
   and prove cross-cluster refusal.
8. Update runbook estimates and escalation thresholds from evidence.

#### Per-installation wave procedure

1. Verify owners, artifact checksums, manifest, and restore rehearsal; take and
   verify the scheduled final pre-migration backup without a migrator database
   mutation, then issue its immutable source-bound evidence. Verify and stage
   the exact checksummed preparation release/compatibility client, rollback
   deployment configuration, and versioned preparation connection-artifact
   template while leaving the credential unissued and the dormant role closed.
2. While V1 is online, run status and the preliminary read-only preflight with
   that exact backup evidence, capacity evidence, preparation-credential plan,
   target connection, and complete writer-probe/external-control set. Approve
   its report/token; the plan/report bind the staged preparation artifact and
   deployment checksum. If the backup, evidence, or staged artifact changes,
   rerun preflight and renew confirmation before any fence mutation.
3. Enter maintenance and stop/drain V1 traffic.
4. Invoke migrate's crash-safe fence phase. It journals the rollback snapshot
   atomically with every database role/ACL fence change, terminates old sessions,
   and uses fresh directly authenticated writer probes to durably prove old
   logins cannot write. Do not mutate database roles or ACLs manually before
   the journal exists.
5. Only after the durable `fence_database_verified_retirement_pending` receipt,
   rotate/revoke the external old credentials and stop any non-database writer
   paths. Supply the versioned evidence and exact probe set to `verify-fence`
   and require `fence_verified`.
6. Review the authoritative fenced preflight digest and renew confirmation if
   any nonallowlisted fact changed.
7. Continue migrate or resume to v2_pending_acceptance.
8. Run validate and read-only application smoke.
9. If validation fails, resume repair or pre-acceptance rollback while fenced;
   if rolling back, issue the new preparation credential, complete the separate
   V1 handoff, switch to the exact staged preparation artifact/connection file,
   and pass its identity/readiness check before any preparation traffic.
10. Explicitly accept with the validation digest.
11. Run the migrator's resumable target-role smoke and require its durable
    passed/fallback receipt.
12. Deploy the target runtime with the new role.
13. Resume traffic gradually and monitor errors, latency, locks, registry
    contention, pruning, autovacuum, storage, and sequence behavior.
14. Complete the soak and take/verify a fresh V2 backup.
15. Run current-pointer, exact-OID/fingerprint cleanup after approval.
16. Store validation, acceptance, smoke, health, backup, and cleanup receipts in the
    fleet ledger.

Pause later waves on any unexplained checksum difference, graph-isolation
failure, identity/property conflict anomaly, deadlock, missed leaf, role-fence
failure, sequence regression, unbounded performance change, or restore
problem.

Exit gate:

- Every supported installation has accepted V2, completed soak and backup, and
  cleaned its legacy objects, or has a formal approved decommission record.
- Fleet health shows no mixed, pending, drifted, or unknown state.

### WP10: V1 runtime and migration retirement

Purpose: realize the benefit of choosing one driver instead of carrying a
permanent divergent implementation.

#### Fleet retirement

After the WP9 gate:

1. Remove V1 bootstrap, runtime reconciliation, write, documentation, and
   normal CI branches from the active driver.
2. Retain only enough V1 detection to return an actionable unsupported-layout
   error.
3. Remove any transition feature gates or layout switches used only during
   development.
4. Keep the standalone migrator, frozen V1 fixture, migration CI, checksummed
   binary, source revision, runbook, and tested PostgreSQL matrix for the
   published late-upgrade horizon.
5. Publish the final V1-compatible module EOL and exact last supported migration
   procedure.

#### End of late-upgrade horizon

1. Remove active V1 migration engine code and its routine CI from the current
   branch.
2. Preserve the immutable, checksummed transition archive required by support
   policy: binary, source/module metadata, fixture, report schemas, runbook,
   tested PostgreSQL versions, and EOL dates.
3. Keep current-runtime V1 detection concise and point it to the archived
   support policy; never recreate V1 or silently attempt migration.
4. Verify no public documentation suggests V1 remains writable or supported.

Exit gate:

- The active driver, CI, docs, and operational tooling have one layout model.
- No permanent divergence surcharge remains.

## Detailed test inventory

The work-package tests are minimum coverage. The release traceability matrix
must additionally map every locked contract to at least one unit test and one
live or operational test where PostgreSQL behavior is involved.

### Identity and routing

- Explicit same (graph_id, id) in two kinds is rejected.
- Same id in two graphs is allowed.
- Concurrent cross-kind explicit-ID inserts produce one winner.
- Raw INSERT through the root and directly into separate leaves enforce the
  same identity/property/immutability rules; raw direct-leaf COPY does too even
  if the DAWGS API does not expose COPY.
- Newly created and migration-attached leaves have the exact combined claim and
  immutability trigger set before writes are granted.
- Endpoint ON CONFLICT update does not leave an orphan identity claim.
- Multi-row insert failure rolls back all claims.
- id, graph_id, and kind_id raw updates are rejected.
- Delete, graph cascade, rollback, and wipe leave no stale claims.
- Sequence next value does not rewind across migration or rollback.

### Property constraints

- Same constrained text in the same and different kind leaves conflicts.
- Missing and JSON-null do not claim; empty text does.
- Legacy/default collation behavior matches.
- Insert, update, delete, direct, batch, and Cypher paths agree.
- Identity-property upsert returns and updates the same logical edge as V1.
- Concurrent create/update claim races converge without lost updates.
- Add/backfill rejects existing duplicates before publication.
- Drop/re-add does not retain stale claims.

### Topology and lifecycle

- All manifest-declared empty kinds receive leaves.
- Dynamic edge kinds receive leaves; node-only kinds do not.
- No DEFAULT leaf is present.
- A missing or invalid canonical managed index is repaired during explicit
  assertion under lock; wrong lineage, unmanaged/unknown index definitions,
  wrong bounds/parents, duplicate bounds, and marker mismatch are hard drift.
- Two driver processes safely race on graph/kind creation.
- Failed transaction never poisons graph, kind, or leaf caches.
- Restricted runtime role can provision only valid managed leaves.

### Query semantics and pruning

- One literal kind touches one leaf.
- Several kinds touch exactly their leaves.
- Kind-agnostic query stays inside one graph and touches all expected leaves.
- ID registry hydration touches the resolved kind leaf.
- Generic and custom prepared plans are characterized.
- Typed mutation retains a kind predicate and exact survivors.
- startNode/endNode and node cascade remain graph-safe with colliding IDs.
- Paths with interleaved edge IDs retain order and uniqueness.

### Migration and operations

- Read-only commands cause no catalog or data change.
- Every durable checkpoint has a failpoint/resume test.
- Changed manifest or catalog invalidates a prior confirmation.
- Late V1 writers are rejected by the independent fence.
- Lost session lock leaves durable state resumable.
- Per-graph cutover and reverse cutover are atomic.
- Validation detects one-row, one-property, one-claim, one-index, and one-bound
  corruption.
- Pre-acceptance rollback restores V1 application-visible logical state with
  only the documented inert-control/OID/statistics/sequence differences.
- Acceptance is atomic with target role grant.
- Post-acceptance rollback is refused.
- Cleanup touches only recorded objects.
- Reports and logs are deterministic enough for automation and redact secrets.

## Review and pull request sequence

Use reviewable PRs with one coherent invariant each:

1. WP0A ADRs, V1 fixture/checksum guard, PostgreSQL 16/18 base matrix, exported
   API inventory, and characterization.
2. WP0B database/privilege/lock/concurrency prototypes, reproducible baselines,
   and approved budgets.
3. Typed catalog model, classifier, public state/errors, preparation-release gate,
   and frozen V1-compatible module/client artifact.
4. Canonical internal V2 schema definitions, topology model, naming, inert DDL
   primitives, and registries. This PR exposes no accepted runtime bootstrap.
5. Schema manager refactor, pre-ID/numeric advisory locks, restricted schema
   operations, and
   commit-safe caches.
6. Direct and batch writes plus registry-backed property upsert.
7. One atomic SQL-compatibility PR: graph-scoped persistent-relation rendering,
   translated metadata/execution, helper DDL and call sites, obsolete-overload
   teardown, hydration/mutation scoping, generated cases, and shared fixtures.
8. Constraint lifecycle, cascade, wipe, optimize, sizing, and explicit parent
   analysis.
9. Join PR enabling the real accepted V2 bootstrap/guard only after PRs 4-8 pass
   together on the unreleased transition stack.
10. Migrator state/manifest/report contracts and read-only status/preflight
    (WP6a; may start after PR 3).
11. Crash-safe fence, chunked global registry preparation, per-graph migration,
    validation, resume, and
   pre-acceptance rollback.
12. Acceptance, accepted/post-cleanup restore, health, cleanup, CLI packaging,
    and artifact smoke tests.
13. Retriever, GraphBench, plan corpus, export, benchmark/benchdiff, dawgrun,
    observability, and structured pruning gates.
14. Complete PostgreSQL 16/18 migration/artifact jobs, performance evidence,
   runbooks, compatibility harness, and release metadata.
15. Small activation/release-promotion PR for the already tested V2-only
   bootstrap/runtime policy.
16. Fleet-wave evidence and fixes as needed; do not mix unrelated feature work.
17. V1 runtime retirement after fleet gates.
18. Migrator retirement after the published late-upgrade horizon.

PR 10's read-only work can proceed after PR 3. Interface scaffolding may overlap
after PR 4, but lifecycle depends on topology, direct/batch/Cypher work depends
on lifecycle, and mutating migration PR 11 depends on the finalized joined
DDL/enforcement behavior. PR 9 is the first commit allowed to expose normal
`v2_accepted` bootstrap/open. PR 15 cannot merge until PRs 1 through 14 are
green and the distributable migrator exists.

Each PR must:

- State the locked contract it advances.
- List catalog/data changes and rollback behavior.
- Include unit tests and the appropriate shared/PostgreSQL/migration tests.
- Update nearby documentation.
- Preserve unrelated worktree changes.
- Run formatting and scoped tests.
- Include explain, benchmark, or migration evidence when it changes plans,
  performance, locks, storage, or recovery.

## Effort and staffing forecast

The current planning range is 15 to 24 engineer-weeks, approximately 8 to 12
elapsed weeks with three experienced engineers working on coordinated tracks.
Fleet rollout calendar time is additional and depends on the number, size, and
operational ownership of installations.

This forecast assumes the committed single-driver design. It contains no
parallel-driver adapter, generalized two-layout engine, duplicate operational
toolchain, or permanent dual CI/support matrix; reintroducing those would
require a new scope and estimate.

| Track | Initial range | Main uncertainty |
|---|---:|---|
| Contracts, prototypes, catalog, and V2 DDL | 3-5 engineer-weeks | Registry and trigger behavior across PG16/18 |
| Lifecycle, direct/batch/Cypher behavior | 4-6 engineer-weeks | Commit-safe DDL/cache concurrency and upsert parity |
| Migrator, recovery, CLI, and packaging | 4-7 engineer-weeks | Dependency preservation, fencing, crash/resume |
| Tooling, CI, performance, and documentation | 3-4 engineer-weeks | Plan/size artifact changes and scale thresholds |
| Activation and engineering support for rollout | 1-2 engineer-weeks | Rehearsal findings; excludes operator wave time |

Re-estimate after WP0. The property registry/upsert prototype, multi-process
schema lifecycle, and crash-safe migration are the critical path. Physical
partition DDL alone is a small fraction of the effort.

Suggested ownership:

- Storage lead: catalog, DDL, registries, constraints, migration data path.
- Driver lead: SchemaManager, direct/batch APIs, Cypher integration, cache and
  locking.
- Reliability lead: CLI, fencing, reports, CI, GraphBench, runbook, artifacts,
  and fleet evidence.

Use a single named decision owner for contract interpretations and a separate
operator approver for acceptance/rollback/cleanup procedures.

## Validation commands

The implementation should add and run:

~~~bash
make format
make lint
make build
make test
git diff --check

# On a clean CI checkout, prove generated cases are current.
make test_update
git diff --exit-code

make migration_preflight_fixture
MIGRATION_TEST_CONNECTION_STRING="$PG_ADMIN_CONNECTION_STRING" \
  RESTORE_TARGET_TEST_CONNECTION_STRING="$PG_RESTORE_ADMIN_CONNECTION_STRING" make test_migration
PG_PHYSICAL_TEST_CONNECTION_STRING="$PG_ADMIN_CONNECTION_STRING" make test_pg_physical
BENCHMARK_TEST_CONNECTION_STRING="$PG_ADMIN_CONNECTION_STRING" make benchmark_transition
go test -race ./drivers/pg/internal/catalog ./drivers/pg/internal/layout ./drivers/pg/internal/schema
go test -race ./drivers/pg/migrate ./cmd/pgmigrate
go test -race ./cmd/retriever ./cmd/graphbench ./cmd/plancorpus ./cmd/benchmark ./cmd/benchdiff ./tools/dawgrun
make build_pg_migrate
make migration_artifact

# Enforce the repository's existing quality and benchmark budgets.
make metrics_check
BENCH_BASE=path/to/v1.json BENCH_TARGET=path/to/v2.json make bench_diff
~~~

With separately supplied benign backend connections:

~~~bash
PG_TEST_OPERATOR_CONNECTION_STRING="$PG_ADMIN_CONNECTION_STRING" \
  PG_TEST_TARGET_CONNECTION_FILE=path/to/target-connection.json make prepare_test_pg
CONNECTION_STRING="$PG_CONNECTION_STRING" make test_all
CONNECTION_STRING="$NEO4J_CONNECTION_STRING" make test_all
PG_CONNECTION_STRING="$PG_CONNECTION_STRING" \
  NEO4J_CONNECTION_STRING="$NEO4J_CONNECTION_STRING" make quality_backend
CONNECTION_STRING="$DISPOSABLE_BENCHMARK_CONNECTION_STRING" make quality_bench
~~~

The migration and destructive physical suites must use dedicated disposable
databases. They deliberately commit DDL, change roles/ACLs, create drift, lose
connections, inject failures, resume, and roll back, so a transaction around
the normal shared integration database is not sufficient.

The artifact smoke job must execute the built binary through status, preflight,
confirm, migrate, verify-fence, resume, validate, accept, smoke, health, and
cleanup with versioned manifest, capacity, backup, preparation-credential,
writer-probe, drain, external-writer, retirement, handoff, restore/rebind, and
cleanup inputs against a database initialized from the frozen V1 fixture. A
rollback job exercises pending refusal, sole-active credential evidence, and
`complete-v1-handoff`. Restore jobs exercise `rebind-restore` for in-progress,
rolled-back pending/passed, accepted migration before/after cleanup, and
accepted fresh-bootstrap states; every accepted restore requires a new target
smoke.

## Activation checklist

- [ ] High-level ADRs match edge_kind_partitioning.md.
- [ ] Exact final V1 schema and artifacts are frozen and checksummed.
- [ ] V1 edge constraint and upsert behavior is characterized.
- [ ] Identity and property registry prototypes pass PostgreSQL 16 and 18.
- [ ] Node child-partition cascade and restricted leaf DDL designs are proven.
- [ ] Canonical public namespace, owner/operator/runtime role handoff, default
      ACLs, safe SECURITY DEFINER routines, and future-leaf grants are proven.
- [ ] V1 and V2 catalog classification fails closed on mixed or drifted states.
- [ ] Preparation V1 release exists and refuses V2.
- [ ] Fresh bootstrap creates only complete V2 with the exact pending-handoff
      shape, then records authenticated target handoff/smoke before runtime.
- [ ] Every accepted operation revalidates exact target session/current-user
      identity; operator, inherited/SET ROLE, and reconnected wrong-role
      sessions remain nonoperational.
- [ ] Every supported profile provides a trusted non-copied current cluster/
      resource and restore-incarnation identity, or explicitly rejects the
      restore modes it cannot distinguish; installation UUID is never a
      substitute.
- [ ] edge_<graph_id> remains the stable graph SQL parent.
- [ ] Nonzero-graph structural translation tests prove all executable persistent
      node/edge references are graph-scoped across normal, fast-path, expansion,
      nested, mutation, and SQL-valued-parameter output.
- [ ] Every declared and dynamic edge kind receives one exact leaf.
- [ ] Durable desired-topology rows make declared-empty, migrated, dynamic, and
      retained leaves discoverable after restart.
- [ ] No DEFAULT leaf exists.
- [ ] Cache state cannot outlive a rolled-back catalog change.
- [ ] Direct, batch, and Cypher writes ensure leaves correctly.
- [ ] Batch graph switching cannot reroute buffered records.
- [ ] Edge ID uniqueness and routing-column immutability are database-enforced.
- [ ] Graph-wide property uniqueness and upsert semantics match V1.
- [ ] Combined trigger ordering, property metadata/claim foreign keys, direct
      registry privileges, and direct-leaf enforcement are proven.
- [ ] ID-only reads and mutations resolve graph and kind safely.
- [ ] Relationship label mutation is explicitly rejected.
- [ ] Node deletion and graph cascade are graph-safe.
- [ ] Wipe, truncate, optimize, analyze, and logical size reporting cover leaves
      and registries.
- [ ] Migrator preflight is read-only and requires a canonical manifest.
- [ ] The preflight digest invalidates changed data, topology, dependency,
      capacity, manifest, role, or privilege facts.
- [ ] Writer fence is independently proven against every old application write
      path.
- [ ] Preliminary preflight binds one directly authenticated writer-probe
      descriptor for every probeable old-writer login path; the journaled
      database fence is proven through fresh direct sessions before external
      credentials are retired, and `verify-fence` proves their retirement.
- [ ] Each mutating CLI invocation reacquires the session layout lock and
      revalidates durable state/fence.
- [ ] Fence intent and the full rollback snapshot are durable before or
      atomically with database ACL changes; ambiguous COMMIT is recoverable.
- [ ] The identity-to-edge foreign key is deferred across graph detach/attach
      and validated only after all V2 parents are active.
- [ ] Migration is checkpointed, resumable, and exactly validated.
- [ ] Global identity/property backfill is bounded, keyset-checkpointed, and
      rehearsed at production scale before the atomic root-key transition.
- [ ] Pre-acceptance rollback is rehearsed.
- [ ] Rolled-back V1 stays database-fenced while handoff is pending; the final
      resumable grant/status transaction passes before the preparation client or
      traffic can proceed.
- [ ] The preparation role starts installation-dedicated, `NOLOGIN PASSWORD
      NULL`, and access-free; the bound credential plan and handoff evidence
      prove one newly issued sole-active credential without persisting secrets.
- [ ] Before fencing, each installation stages and binds the exact checksummed
      preparation release/client and connection-artifact template; after
      rollback, only that artifact opens with the new handoff credential.
- [ ] Active, current-rollback, accepted-migration, and bootstrap scope pointers
      remain mutually exclusive and select every receipt/handoff/rebind action
      across repeated migration/rollback cycles without timestamp inference.
- [ ] Acceptance atomically persists its receipt, marks V2, and grants the
      separately authenticated non-owner target role whose OID is distinct from
      owner, operator, rollback preparation, and every V1 writer login.
- [ ] Acceptance records smoke pending; the idempotent accepted-state smoke
      command records passed/fallback before the runtime or traffic can proceed.
- [ ] Post-acceptance in-place rollback is refused.
- [ ] Cleanup requires the current identity-epoch/scope smoke receipt to be
      passed/fallback and deletes only recorded legacy objects after soak and
      backup.
- [ ] Accepted pre-cleanup restore creates an identity-epoch-bound current-OID
      cleanup map from exact OID-independent fingerprints; cleanup refuses stale
      maps, OID reuse, name/dependency drift, and bootstrap scope.
- [ ] Migration- and bootstrap-scoped accepted restores, post-cleanup restores,
      scope-aware rebind plus new target smoke, and the supported PITR state
      matrix pass in databases with different OIDs.
- [ ] In-progress rebind is limited to the same stable cluster with exact
      writer/fence facts and covers database-identity and incarnation-only
      changes; cross-cluster in-progress restore fails closed, while accepted
      cross-cluster rebind proves exact owner/ACL role remapping and stale-grant
      removal.
- [ ] Changed-database in-progress rebind preserves immutable probe/control
      identities, explicitly maps their old-to-new targets, rejects probes still
      reaching the source, and advances the authoritative current binding.
- [ ] Rolled-back same-cluster restore resets preparation handoff and requires a
      new nonce/epoch-bound receipt; cross-cluster rolled-back restore fails
      closed.
- [ ] Retriever, GraphBench, plan corpus, export, benchmark/benchdiff, and
      dawgrun are V2-aware.
- [ ] The frozen V1 benchmark artifact emits the common versioned comparison
      envelope, and transition rehearsals isolate comparator/migration writer
      roles so retirement cannot disable the V1 side.
- [ ] Benchdiff accepts only equal workload case-manifest digests, semantic case
      IDs, schedules, distributions, units, and approved V1/V2 adapter mappings.
- [ ] One-, multi-, all-kind, and ID-hydration plans meet approved gates.
- [ ] PostgreSQL 16 and 18 suites pass.
- [ ] Destructive `pg_physical` and migration suites pass only in identity-
      guarded disposable databases on both required majors.
- [ ] Frozen-fixture checksum and executable compatibility-matrix jobs pass.
- [ ] Overall unit coverage does not drop.
- [ ] Shared Neo4j semantics pass unchanged.
- [ ] Built migrator binary, checksum, report schemas, and runbook ship with the
      target runtime.
- [ ] Every supported installation is present in the fleet ledger.

## Fleet retirement checklist

- [ ] Every supported installation is accepted V2 or formally decommissioned.
- [ ] Every accepted installation has completed soak and a verified fresh
      backup.
- [ ] Every migrated accepted installation with retained legacy objects has a
      cleanup receipt; fresh-bootstrap installations retain the canonical empty
      cleanup map and require no cleanup command.
- [ ] No unknown, V1, mixed, in-progress, pending, or drifted installation
      remains.
- [ ] V1 runtime/bootstrap/reconciliation paths and normal V1 CI are removed.
- [ ] Current runtime retains only actionable V1 detection.
- [ ] Frozen migrator, fixture, artifact, tests, and runbook are retained for
      the published late-upgrade horizon.
- [ ] At horizon end, active migration code and CI are removed and the immutable
      transition archive is verified.

## Definition of done

Track three non-interchangeable completion gates:

1. Fleet transition complete: every supported installation is accepted V2 or
   formally decommissioned, has completed its required soak/backup/cleanup, and
   no old writer can reconnect.
2. V1 runtime retirement complete: the active pg driver, operational tools,
   normal CI, and current documentation have one schema model; only actionable
   V1 detection and the separately maintained late-upgrade path remain.
3. Late-upgrade retirement complete: the published horizon has ended, active
   V1 migrator code/fixture CI has left the current branch, and the required
   immutable transition archive has been verified.

The transition program reaches its final definition of done only at gate 3.
Before then, the completed gates and the remaining support obligation must be
reported explicitly. Fresh V2 support, a successful development migration, or
migration of most installations is progress but not completion.
