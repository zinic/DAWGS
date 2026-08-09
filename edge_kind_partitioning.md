# PostgreSQL Edge Kind Partitioning Plan

## Status

This document is the high-level delivery and retirement plan. The product
direction is final: the existing PostgreSQL `pg` driver advances to the
graph-and-kind layout, every supported installation migrates, and the current
one-level layout is retired. The document also records the remaining
implementation work, migration strategy, and validation gates. It does not
claim that edge-kind partitioning is implemented.

The repository-vetted execution order and implementation contracts live in
[edge_kind_partitioning_plan.md](edge_kind_partitioning_plan.md). This document
is authoritative for product direction and retirement policy; where a detailed
mechanic or phase ordering here differs from that execution plan, the execution
plan governs. A product-contract change still requires both documents and its
ADR to be updated.

## Objective

Partition PostgreSQL edges by both their graph and relationship kind so that a
query constrained to one or a small set of edge kinds can avoid planning and
scanning unrelated edge data.

The change must:

- Preserve graph isolation and existing Cypher semantics.
- Keep the existing graph-scoped `edge_<graph_id>` relation usable by the SQL
  translator, direct writes, maintenance commands, and diagnostic tooling.
- Support edge kinds declared when a graph is created and kinds introduced
  dynamically after graph creation.
- Preserve edge IDs, properties, constraints, indexes, and sequence state when
  migrating an existing database.
- Provide an explicit and recoverable migration rather than relying on
  `CREATE TABLE IF NOT EXISTS` to reshape existing tables.
- Demonstrate that kind-constrained queries prune partitions and that
  kind-agnostic workloads remain within an agreed performance budget.

## Program Decision: One Driver, One Destination Layout

Keep one PostgreSQL driver and one schema lineage. Preserve the existing `pg`
registration, `pg.NewDriver`, package path, URL handling, and connection-string
contract. Do not add a `pg-edge-kind` driver, a user-selectable storage-layout
option, or a permanent shared engine that keeps both layouts operational.

This accepts the one-time migration and fleet-coordination cost in order to
avoid carrying duplicate schema, CI, tooling, documentation, and operational
work after every installation has moved. Refactor only the seams required by
the version 2 topology and offline migration; do not spend the transition
building a reusable two-layout runtime architecture.

The hierarchical graph-and-kind layout is schema version 2 and becomes the
only steady-state layout supported by the `pg` runtime. The current one-level
graph layout is schema version 1 and becomes a migration source only. Refactor
the existing driver around the version 2 topology; isolate version 1 catalog
knowledge in layout classification, the offline migrator, frozen fixtures, and
actionable error reporting.

The supported state transitions are (smoke is a substate of the accepted
marker, not a separate layout version):

```text
uninitialized ── operator V2 bootstrap ──▶ v2_accepted[handoff/smoke=pending]
v1 ── offline migration ──▶ v1_to_v2_in_progress ──▶ v2_pending_acceptance
 ▲                                  │                              │
 └──────────── pre-write rollback ──┴──────────────────────────────┘
v2_pending_acceptance ── explicit accept/grant ─▶ v2_accepted[smoke=pending]
v2_accepted[pending] ── target-role handoff/smoke ──▶ [smoke=passed/fallback]
```

Operator-authenticated schema assertion may bootstrap only a truly
uninitialized database. Normal target-runtime operations accept only a
catalog-consistent `v2_accepted`
database whose smoke/fallback result passed. They refuse `v1` with the stable
`migration_required` error and refuse `v1_to_v2_in_progress`,
`v2_pending_acceptance`, an
accepted marker with smoke pending, or an unrecognized mixed state with resume/
recovery instructions. Application writers remain fenced or runtime-gated
until the accepted smoke passes. Once a version 2 application write is allowed,
recovery is forward-fix or restore/reconcile; the retained version 1 tables are
not a live downgrade path.

Operational acceptance is identity-bound as well as state-bound: service-start
assertion validates the complete contract, and the first use of each physical
application session must match the asserted cluster/database binding and have
`session_user = current_user` matching the recorded non-owner target role
OID/name. Reused sessions inherit that startup assertion and backend check;
managed identity or epoch changes require drain and reassertion. The role must differ from the schema
owner and operator/control session user; migration scope additionally requires
it to differ from the preflighted rollback-preparation role and every V1 writer
login. Fresh bootstrap has no preparation or V1-writer identity to compare. A
clone/restore mismatch returns `restore_rebind_required` before
managed-type or application SQL. Operator, migration, inherited, and `SET ROLE`
sessions remain nonoperational against accepted V2. In particular, the operator
driver that performed fresh `AssertSchema` cannot become a target runtime after
handoff merely because its pool stayed open.

Pool creation must not load DAWGS composite types before that readiness check.
Prebootstrap operator connections remain raw; only a guarded, exact-target
session may load schema-qualified `public.*` types before application SQL.
Reconnects and restored/fingerprint-changed databases revalidate and reload,
and caller-supplied pool hooks remain composed rather than overwritten.

Both fresh bootstrap and migration acceptance durably record a target-role
smoke/fallback result. The marker and grants may commit with smoke pending for
crash safety, but ordinary runtime operations and traffic remain blocked until
that resumable check passes. Fresh bootstrap uses its own operation UUID and
append-only initialization, handoff, and smoke receipts; it never invents a
sentinel migration ID, and reports can be regenerated after an ambiguous commit
from those durable facts.

All generally available version 2 artifacts must ship initialization,
migration, validation, resume, and pre-write rollback together. Fresh-database
support may be used as an internal implementation milestone, but it must not be
released as a greenfield-only driver while initialized V1 installations lack a
supported upgrade path.

## Recommended Architecture

Use hierarchical `LIST` partitioning:

```text
edge                              PARTITION BY LIST (graph_id)
└── edge_<graph_id>               PARTITION BY LIST (kind_id)
    ├── edge_<graph_id>_<kind_id>
    └── edge_<graph_id>_<kind_id>
```

Example DDL shape:

```sql
create table edge
(
  id         bigint   not null default nextval('edge_id_seq'),
  graph_id   integer  not null,
  start_id   bigint   not null,
  end_id     bigint   not null,
  kind_id    smallint not null,
  properties jsonb    not null,

  primary key (id, graph_id, kind_id),
  foreign key (graph_id) references graph (id) on delete cascade,
  unique (start_id, end_id, kind_id, graph_id)
) partition by list (graph_id);

create table edge_7
  partition of edge
  for values in (7)
  partition by list (kind_id);

create table edge_7_12
  partition of edge_7
  for values in (12);
```

This is an effective composite routing key: PostgreSQL first routes on
`graph_id`, then on `kind_id`. PostgreSQL does not permit
`PARTITION BY LIST (graph_id, kind_id)` because `LIST` accepts only one column
or expression. Multi-column keys are supported for `RANGE` and `HASH`.

If a literal two-column root key is a hard external requirement, a separate
design can use `PARTITION BY RANGE (graph_id, kind_id)` with graph-level range
partitions that are subpartitioned by `LIST (kind_id)`. That design rebuilds the
root table, complicates bounds, and does not provide a material routing benefit
over hierarchical `LIST`, so it is not the recommended implementation.

Do not create a `DEFAULT` kind partition. A default partition would hide missed
partition-provisioning paths, mix unrelated kinds again, and make later
attachment of a dedicated kind partition require validating or emptying the
default partition. Declared kinds should be created eagerly and dynamic kinds
should be created through an idempotent runtime ensure operation.

## Success Criteria

The effort is complete only when all of the following are true:

1. Every PostgreSQL edge row is stored in exactly one graph-and-kind leaf.
2. `edge_<graph_id>` remains the stable graph-scoped SQL target.
3. A kind-constrained query touches only the requested kind leaves when
   PostgreSQL has a plan-time or execution-time usable kind predicate.
4. Existing graph, edge, and path semantics pass the shared integration corpus
   without PostgreSQL-specific expected-result changes.
5. Direct, batch, and Cypher writes can introduce a new edge kind without an
   operator manually creating a table.
6. Edge IDs remain database-enforced as unique by `(graph_id, id)`, including
   for explicit-ID SQL writes.
7. Existing edge property constraints remain graph-wide across kinds and
   preserve their observable upsert/conflict behavior.
8. Node deletion removes only edges in the deleted node's graph.
9. The migration is resumable, validates its result, preserves a recoverable
   legacy copy until cutover is accepted, and does not silently run at startup.
10. Operational tools report aggregate edge size and partition state correctly
    after `edge_<graph_id>` becomes a partitioned parent.
11. PostgreSQL 16 and PostgreSQL 18, the required initial validation majors,
    pass the PostgreSQL integration and migration suites. The current workflow
    runs PostgreSQL 18 only; the implementation adds the PostgreSQL 16 job and
    records PostgreSQL 17's support disposition before V2 DDL lands.
12. Performance gates are evaluated from a reproducible before/after baseline,
    including both kind-constrained and kind-agnostic workloads.
13. The public PostgreSQL driver identity and DSN contract remain `pg`; there is
    no second driver or permanent layout selection.
14. Every supported PostgreSQL installation is inventoried, assigned an owner,
    migrated to version 2, and validated before version 1 support is retired.
15. Normal `pg` startup cannot create or operate version 1 after the transition
    release, and no mixed-layout state can accept application writes.
16. Version 1 runtime, bootstrap, reconciliation, documentation, and CI paths
    are removed after the adoption and rollback-window gates pass. A frozen
    version 1 fixture and separately testable migrator remain only for the
    published upgrade-support horizon.

## Current State

### Storage

`drivers/pg/internal/schema/sql/schema_up.sql` creates `edge` as a
`LIST (graph_id)` partitioned table with:

- Primary key `(id, graph_id)`.
- Graph foreign key on `graph_id`.
- Endpoint uniqueness on `(start_id, end_id, kind_id, graph_id)`.
- Covering indexes rooted at `(start_id, kind_id)`, `(end_id, kind_id)`, and
  `(kind_id)`.

Each graph owns one ordinary edge partition named `edge_<graph_id>`. The
`drivers/pg/model.GraphPartitions` model likewise contains one node partition
and one edge partition per graph.

The node-delete cascade trigger deletes from the root `edge` table using only
`start_id` and `end_id`. Because entity IDs are graph-local by contract, this
can delete edges in another graph when node IDs collide. The partitioning change
must correct this query by joining on `graph_id` as well as endpoint ID.

### Schema and kind lifecycle

`drivers/pg/manager.go` has two separate lifecycle concepts:

- `AssertSchema` defines global kind IDs by walking `Schema.Graphs`.
- `AssertGraph` creates or reconciles the graph's node and edge partitions.

`Schema.DefaultGraph` is often only a graph name, while its full kind list is
available in `Schema.Graphs`. Partition provisioning therefore cannot rely only
on `DefaultGraph.Edges`.

`AssertKinds` can define a kind after a graph already exists. It does not know
whether a kind is being used for a node or an edge and does not know the target
graph. Creating edge-kind leaves directly inside generic `AssertKinds` would
therefore create the wrong scope and potentially create edge partitions for
node-only kinds.

The graph cache currently allows `AssertGraph` to return without reconciling the
database again. Cache entries can also be populated before the outer database
transaction is known to have committed. The new topology must not trust an
optimistic cache entry after a rollback, and repeated schema assertion must be
able to discover and repair missing leaves or indexes.

The manager's in-process lock coordinates only one `SchemaManager`. Multiple
driver processes can race to create the same graph or kind partition, so DDL
creation also needs database-level serialization.

### Writes and mutations

The affected paths include:

- Direct relationship creation in `drivers/pg/transaction.go`.
- Direct relationship property updates in `drivers/pg/transaction.go` and
  `drivers/pg/statements.go`.
- Buffered relationship creation in `drivers/pg/batch.go`.
- Relationship upsert-by-identity in `drivers/pg/batch.go` and
  `drivers/pg/query/format.go`.
- ID-only batch deletion in `drivers/pg/statements.go`.
- Cypher relationship creation in
  `cypher/models/pgsql/translate/create.go`.
- Cypher-generated relationship updates and deletes.

Root inserts already include both `graph_id` and `kind_id`, so PostgreSQL can
route them once the target leaf exists. Relationship property upserts target
`edge_<graph_id>`, which remains valid under the recommended hierarchy.

Several update, delete, and hydration paths identify an edge only by ID, or by
graph and ID without kind. These paths remain correct only if `(graph_id, id)`
continues to identify at most one edge. They will scan multiple kind leaves
unless the kind can be recovered before accessing the edge parent.

### Reads and SQL translation

The PostgreSQL translator currently emits executable root `node` and `edge`
references in normal, fast-path, expansion, and hydration paths; graph ID is not
yet a persistent-relation rendering context. V2 must map only persistent table
references and mutation targets to `node_<graph_id>` and `edge_<graph_id>`
across every translation path. The recommended hierarchy then lets PostgreSQL
prune kind leaves beneath that graph-scoped parent.

Kind-aware patterns commonly emit typed, materialized kind ID predicates. Those
are the best candidates for pruning. Truly kind-agnostic queries may consider
every kind leaf in the graph. Path hydration and public ID-only edge APIs must
first resolve `(graph_id, edge_id)` through `edge_identity` and join the graph
parent with the resolved kind; executor pruning for that parameterized join is
measured rather than assumed.

The following helpers and renderers require explicit review because they hydrate
edges from IDs:

- `nodes_to_path`, `edges_to_path`, and `ordered_edges_to_path` in
  `drivers/pg/internal/schema/sql/schema_up.sql`.
- `start_node`, `end_node`, `swap_forward_front`, and `swap_backward_front` in
  the same file.
- `EdgeArrayFromPathIDs` formatting in
  `cypher/models/pgsql/format/format.go`.
- Shortest-path and traversal helpers that retain edge ID arrays.

The initial storage change should not expand the public path representation only
to carry `kind_id`. First establish correctness through the edge identity
contract, measure the cost of cross-leaf hydration, and optimize the path state
separately if it is material.

### Indexes, constraints, and catalog discovery

Graph property indexes and constraints are created against
`edge_<graph_id>`. After the change that relation is a partitioned table, so a
logical index declared on it will own child indexes on each leaf.

Current discovery reads textual definitions from `pg_indexes` and parses them
with regular expressions. Partitioned indexes can render as `ON ONLY`, and
child index names are not a stable representation of the logical graph-level
schema. Topology and index reconciliation should move to PostgreSQL catalogs
instead of extending the regular expressions.

### Operational tooling

The following tools assume one physical edge relation per graph:

- `cmd/retriever` checks for `edge_<graph_id>` existence.
- `cmd/graphbench` runs maintenance and measures
  `pg_total_relation_size(edge_<graph_id>)`.
- GraphBench plan assertions match `edge_<graph_id>` scan and index names.
- `cmd/plancorpus` truncates graph-scoped node and edge relations.
- `cmd/benchmark`, `cmd/benchdiff`, and `tools/dawgrun` open the driver or compare
  artifacts without V2 layout metadata.

The relation continues to exist, but graph-scoped `TRUNCATE` is not automatically
valid once unpartitioned registries reference the hierarchy; `TRUNCATE` also
does not run ordinary delete triggers. The implementation must use the proven
managed reset relation set or a graph-scoped delete routine, never an assumed
child truncate or `TRUNCATE CASCADE`. Size reporting and plan parsing must
include leaves because a partitioned parent has no edge heap data.

## PostgreSQL Constraints That Shape the Design

### Partitioned uniqueness

PostgreSQL requires a unique constraint on a partitioned table to contain every
column in that table's partition key. If a partition is itself partitioned, the
constraint must also contain the descendant partition key. Consequently the
root primary key must change from:

```text
(id, graph_id)
```

to:

```text
(id, graph_id, kind_id)
```

This is a database requirement, but it is not by itself a sufficient DAWGS edge
identity contract. The new key permits the same `(graph_id, id)` pair to exist
in two kind leaves.

### Partition pruning

Partitioning is useful only when the query exposes a usable `kind_id` predicate.
The validation matrix must distinguish:

- One literal or materialized edge kind.
- Several literal or materialized edge kinds.
- A parameterized kind array under custom and generic plans.
- A kind predicate that is introduced only by a join.
- No edge-kind predicate.
- ID-only hydration.

Correctness must never depend on pruning. A query that cannot prune must still
return the same result while scanning the graph's kind leaves.

### Partition count

The total leaf count becomes approximately:

```text
sum(edge kinds present in each graph)
```

Planning cost, relcache memory, autovacuum work, schema assertion time, and
catalog query cost must be measured at realistic and worst-supported graph/kind
counts. The benchmark should include skew, because production graphs may place
most rows in a few kinds while retaining many small leaves.

### DDL locking and migration

`ATTACH PARTITION`, parent constraint changes, and partitioned index operations
take table locks. Providing a validated `CHECK (graph_id = <id>)` on a staged
table can avoid a full validation scan during attachment, but it does not make
the operation lock-free. The first supported migration should require a
maintenance window. An online migration would require a separate dual-write or
change-capture design and is out of scope for the first delivery.

## Finalized Contract Decisions

These contracts are part of the migration baseline. Record them in an ADR, but
do not reopen them as layout options during implementation. Prototyping may
choose the enforcement mechanism, not weaken the observable contract.

### Decision 1: edge ID uniqueness

Existing code and documentation treat an edge as uniquely identifiable by
`(graph_id, id)`. ID-only update, delete, and path-hydration paths depend on that
invariant.

Preserve strict database enforcement of `(graph_id, id)`. Add an unpartitioned
identity relation or equivalent enforcement mechanism with a unique key on
`(graph_id, id)` and the corresponding `kind_id`. Driver and raw SQL writes must
be unable to insert the same graph-local edge ID into two kind leaves.

Treat `id` and `graph_id` as immutable after insertion, just like `kind_id`.
Database enforcement must reject raw updates to any identity/routing column and
must transactionally remove registry state on delete and cascade. This closes
update paths that an insert-only registry trigger would otherwise miss.

Before selecting the exact mechanism, prototype:

- Multi-row root inserts.
- `INSERT ... ON CONFLICT DO UPDATE` paths.
- `COPY` or batched inserts, if introduced later.
- Deletes, node cascade, and WipeGraph cleanup behavior.
- Transaction rollback.
- Migration population and validation.
- Concurrent inserts of the same explicit ID into different kinds.
- Attempted updates to `id`, `graph_id`, and `kind_id`.
- The write-amplification and contention cost.

An `AFTER INSERT` enforcement trigger may avoid allocating an orphan identity
row for an insert that instead takes an `ON CONFLICT DO UPDATE` branch, but
trigger behavior and cleanup still require dedicated integration tests. Select
the exact mechanism only after the Phase 0 concurrency and performance
prototype.

Relying only on the shared sequence is rejected for this migration because raw
SQL could then create duplicate graph-local IDs and make ID-only operations
ambiguous. Weakening this invariant later requires a separately approved
breaking-contract ADR.

### Decision 2: edge property uniqueness scope

Preserve today's graph-wide property uniqueness across all relationship kinds.
A unique index on a kind-partitioned edge parent would have to include
`kind_id`, which would silently loosen the contract and permit the same value in
different kinds. A mandatory physical migration must not introduce that
unrelated semantic change.

PostgreSQL cannot enforce graph-wide cross-kind uniqueness with only a unique
index on the kind-partitioned edge table. Implement a separate uniqueness
registry or equivalent trigger-backed mechanism, keyed by graph, constraint,
and normalized property value. Prototype and benchmark it together with edge
identity enforcement. Preserve existing conflict behavior for direct, batch,
and Cypher writes and ensure registry entries are transactionally inserted,
updated, and deleted with their edges.

Constraint lifecycle must remain safe after graph creation. Adding a constraint
must acquire the graph schema lock, install write-side enforcement, backfill and
validate all existing rows, and publish the constraint only after both sources
agree. Dropping it must disable enforcement and remove its registry metadata in
one transaction. Match the current `properties ->> field` text comparison,
collation, and SQL `NULL`/missing-property behavior exactly; do not replace it
with a lossy hash or make null values spuriously conflict.

Per-kind property uniqueness is explicitly out of scope. Adopting it later
requires a separately approved semantic-change ADR, release notes, and upgrade
tests.

### Decision 3: edge kind mutability

Treat `kind_id` as immutable after edge creation. Changing it is a partition-key
update and can move a row between leaves. DAWGS does not currently expose
relationship kind mutation as a normal property update.

The implementation should:

- Never include `kind_id` in ordinary edge property updates.
- Reject a raw update that changes `kind_id` through database enforcement.
- Require delete-and-create semantics for a future public kind-change API.

### Decision 4: migration ownership and driver lineage

The existing `pg` driver owns the version 2 layout. Do not introduce a second
driver, runtime layout toggle, or legacy compatibility mode. Do not silently
restructure populated databases from `AssertSchema` or startup. Add a durable
schema-layout version and ship a PostgreSQL-specific offline migration package
and command that must:

- Inspect and report the exact layout and migration state.
- Run preflight checks without mutation.
- Execute the migration with an explicit confirmation flag.
- Resume a partially completed per-graph migration.
- Validate a completed migration.
- Report pre-acceptance rollback instructions while writers remain fenced and
  legacy tables are retained.

Target-runtime startup must recognize any initialized version 1 layout,
including one with zero graphs or rows, and return an actionable error naming
the required migration. Only a truly uninitialized database is bootstrapped
directly at version 2; no supported path creates version 1 after the transition
release.

### Release and layout compatibility matrix

The release must publish and enforce this compatibility contract:

| Release or component | Version 1 graph-only layout | Version 2 graph/kind layout |
|---|---|---|
| Last version 1-compatible `pg` release | Temporarily supported and deprecated until its migration deadline | Explicitly refuses the version 2 marker |
| Transition release's `pg` runtime | Fails closed with `migration required` | Supported; this is its only writable layout |
| Offline migration command | Must inspect, preflight, migrate, resume, validate, and pre-write rollback | Must validate; it never acts as an application runtime |
| Post-retirement `pg` runtime | Unsupported, with guidance to the retained migration artifact | Supported |

Application runtime supports exactly one layout per release; only the offline
migration boundary understands legacy and mixed states. Never run version 1 and
version 2 writers concurrently. Before copy, terminate version 1 sessions and
revoke or rotate their role or credentials so an old deployment cannot
reconnect. Give only the target deployment the post-migration write role. A
historical version 1 binary may not know about the new layout marker, so the
marker alone is not a sufficient fence.

## Detailed Implementation Plan

### Phase 0: lock contracts and capture a baseline

Tasks:

1. Record the four decisions above.
2. Define the maximum supported graphs, kinds per graph, and total leaf count to
   use for scale validation.
3. Capture current PostgreSQL baselines for:
   - Single-kind traversal.
   - Multi-kind traversal.
   - Kind-agnostic traversal.
   - Relationship count by kind and without kind.
   - Ordered edge-ID hydration.
   - Direct relationship creation.
   - Buffered mixed-kind relationship creation.
   - Relationship property upsert.
   - Schema assertion and driver startup.
4. Prototype and benchmark strict edge identity enforcement and graph-wide
   property-uniqueness enforcement.
5. Record numeric regression budgets before optimizing the new layout.
6. Verify all DDL and pruning assumptions on PostgreSQL 16 and PostgreSQL 18.

Exit criteria:

- Storage, identity, uniqueness, immutability, and migration contracts are
  approved.
- Baseline artifacts and commands are reproducible.
- The identity and property-uniqueness enforcement mechanisms have passing
  concurrency prototypes and acceptable measured overhead.

### Phase 1: model names and catalog topology

Affected areas:

- `drivers/pg/model/model.go`
- `drivers/pg/model/format.go`
- `drivers/pg/query/query.go`
- `drivers/pg/query/definitions.go`
- New catalog query files under `drivers/pg/query/sql/`

Tasks:

1. Add a stable leaf naming helper based on numeric IDs, for example
   `EdgeKindPartitionTableName(graphID, kindID)` producing
   `edge_<graph_id>_<kind_id>`.
2. Extend the model to distinguish:
   - Node graph partition.
   - Edge graph partitioned parent.
   - Edge kind leaves keyed by kind ID.
   - Logical graph-level indexes and constraints.
3. Query PostgreSQL catalogs to validate:
   - Relation existence and relation kind.
   - Parent/child inheritance links.
   - Partition strategy and key attributes.
   - Graph and kind bounds.
   - Logical parent indexes and attached leaf indexes.
   - Valid and invalid index state.
4. Use `pg_partitioned_table`, `pg_inherits`, `pg_class`, `pg_namespace`,
   `pg_index`, `pg_attribute`, and `pg_get_expr` instead of parsing topology from
   `pg_indexes` text.
5. Categorize catalog discrepancies. An existing relation with the expected
   name but the wrong parent, partition key, bound, owner-sensitive dependency,
   or unmanaged definition is hard drift: return a diagnostic error and never
   attach or overwrite it blindly. A missing managed leaf/index, or an invalid
   managed index whose canonical definition and dependencies are known, is
   safely reconstructable absence and may be repaired under the schema lock
   during explicit assertion.
6. Reconcile logical parent indexes rather than treating generated child index
   names as separate graph schema declarations.
7. Ensure all identifier construction remains below PostgreSQL's identifier
   length limit and never embeds user-controlled kind names.
8. Keep version 1 topology structs and queries inside the read-only classifier
   and migration package. The active driver schema manager models only version
   2; do not introduce a general `StorageLayout` runtime abstraction.
9. Classify `v1`, `v1_to_v2_in_progress`, `v2_pending_acceptance`,
   `v2_accepted`, uninitialized, drifted, and unrecognized states explicitly.
10. Because deployed version 1 databases do not yet have a layout marker,
    recognize an unmarked database as `v1` only when its catalog fingerprint
    matches the frozen final version 1 core schema. The fingerprint must
    normalize catalog-generated names and explicitly inventory supported
    managed property constraints, owners, ACLs, comments, and dependencies;
    those accepted extensions are not drift. Treat any other populated,
    unmarked topology as drift requiring operator remediation.

Exit criteria:

- Unit tests cover naming, topology decoding, drift detection, and partitioned
  index discovery.
- Version 1, version 2, and migration states can be classified without changing
  the database, but classification alone never authorizes runtime access.

### Phase 2: target-layout DDL and initialization

Affected areas:

- `drivers/pg/internal/schema/sql/schema_up.sql`
- `drivers/pg/internal/schema/sql/schema_down.sql`
- Partition DDL formatting in `drivers/pg/query/format.go`
- Partition creation in `drivers/pg/query/query.go`

Tasks:

1. Change the root edge primary key to `(id, graph_id, kind_id)`.
2. Add strict graph-local edge identity enforcement objects and database
   immutability for `id`, `graph_id`, and `kind_id` after insertion.
3. Add graph-wide edge-property uniqueness enforcement objects and transactional
   cleanup for update, delete, and graph cascade.
4. Keep the graph foreign key and endpoint uniqueness constraint.
5. Change graph edge creation so `edge_<graph_id>` is itself partitioned by
   `LIST (kind_id)`.
6. Add a separate leaf creation formatter with both graph and kind context.
7. Apply non-unique edge property indexes to the graph edge parent and route
   uniqueness checks through the graph-wide enforcement mechanism.
8. Preserve current `ON CONFLICT` behavior without adding `kind_id` to the
   uniqueness contract.
9. Update schema teardown for new registry objects and rely on dropping the
   partitioned root to cascade through attached leaves.
10. Correct `delete_node_edges()` to match deleted nodes by graph and endpoint,
   preferably with `DELETE ... USING deleted_nodes`.
11. Preserve the current base edge indexes for the first correctness pass.
    Benchmark a follow-up leaf-aware index shape:
    - `(start_id) INCLUDE (id, end_id)`.
    - `(end_id) INCLUDE (id, start_id)`.
    - Removal or replacement of the kind-first index, because `kind_id` is
      constant within a leaf.
12. Add a schema layout version marker for every version 2 database.

Exit criteria:

- A truly uninitialized PostgreSQL database creates the complete two-level
  hierarchy and remains runtime-gated until target-role handoff/smoke passes.
- No version 2 code path can initialize the version 1 layout.
- Root and graph-parent constraints are valid.
- Full schema teardown removes all attached leaves. Deleting graph data leaves
  the empty physical graph/kind partitions in place unless a future explicit
  graph-partition teardown operation is added.
- The node cascade test proves that colliding node IDs in another graph are not
  affected.

### Phase 3: declared and dynamic leaf lifecycle

Affected areas:

- `drivers/pg/manager.go`
- `drivers/pg/driver.go`
- `drivers/pg/query/query.go`
- All edge creation call sites

Tasks:

1. Canonicalize graph schemas by name so the full definition in
   `Schema.Graphs` supplies edge kinds even when `DefaultGraph` is name-only.
   Preserve the current lazy graph lifecycle: `AssertSchema` records canonical
   definitions and global kind IDs, the default graph is asserted explicitly,
   and secondary graphs are created only when `WithGraph`/`AssertGraph` asserts
   them. Do not create every `Schema.Graphs` entry as a database graph merely to
   provision leaves.
2. During graph assertion:
   - Create or validate the graph edge parent.
   - Resolve all declared edge kind IDs.
   - Create or validate every declared leaf.
   - Reconcile logical indexes and constraints after the parent exists.
3. Add an edge-specific operation such as
   `EnsureEdgeKindPartitions(graphID, kindIDs)` rather than overloading generic
   `AssertKinds`.
4. Invoke the ensure operation after kind mapping and before every edge insert
   or upsert path that can introduce a new kind.
5. Run dynamic ensure in a committed schema transaction for an already-committed
   graph. Use a transaction-scoped PostgreSQL advisory lock derived from the
   graph and kind IDs to serialize different driver processes.
6. Inside the advisory lock, re-read the catalogs before creating anything.
   `CREATE TABLE IF NOT EXISTS` alone is insufficient because it does not verify
   parentage or bounds.
7. Define one lock order for schema operations: migration/layout lock, graph
   lock, then kind locks in ascending numeric order. Data writers must finish
   leaf ensure before acquiring row locks that could participate in a DDL
   deadlock.
8. Require every supported target installation to grant the driver narrowly
   scoped rights to create and attach edge-kind leaves. Check this in migration
   preflight and health diagnostics. An installation that cannot grant or
   delegate those rights must resolve that policy before migration; hand-created
   tables and a default partition are not supported fallbacks.
9. Do not automatically drop leaves for kinds omitted from a later schema
   assertion. Omission is not authorization to delete data.
10. Separate transaction-local graph creation from runtime ensure:
   - Leaves for a newly created graph are created in the graph transaction.
   - Runtime ensure operates only on a committed graph parent.
11. Make caches commit-safe:
   - Do not publish a newly created topology or kind ID before commit.
   - Apply the same rule to `kindsByID`, `kindIDsByKind`, graph topology, and
     edge-leaf state.
   - Evict and refetch affected kind and topology entries after rollback or an
     ambiguous DDL failure.
   - Reconcile on explicit schema assertion even when a graph is cached.
   - Prefer the catalog as the source of truth for repair.
12. Return an actionable missing-leaf error containing graph and kind IDs if a
    write somehow reaches PostgreSQL before ensure completes.
13. Implement graph-wide property-constraint add, reassert, and drop lifecycle
    under the graph schema lock, including locked backfill, validation,
    transactional cleanup, and concurrent-write protection.

Exit criteria:

- Full and name-only default graph schemas create the same leaves.
- Secondary graphs created through `WithGraph` receive their declared leaves.
- A previously unknown edge kind can be written without operator DDL.
- Two driver instances racing to introduce the same kind both succeed and
  produce one valid leaf.
- A rolled-back kind definition, graph creation, or leaf creation does not
  poison any kind or topology cache.

### Phase 4: write, update, delete, and hydration correctness

Affected areas:

- `drivers/pg/transaction.go`
- `drivers/pg/batch.go`
- `drivers/pg/statements.go`
- `drivers/pg/query/format.go`
- `cypher/models/pgsql/translate/create.go`
- `cypher/models/pgsql/format/format.go`
- Edge-hydration functions in `drivers/pg/internal/schema/sql/schema_up.sql`

Tasks:

1. Direct creation:
   - Resolve the kind.
   - Ensure its leaf for the target graph.
   - Insert through `edge` or `edge_<graph_id>` with both keys present.
2. Buffered creation:
   - Collect distinct mapped edge kind IDs from the buffer.
   - Ensure all required leaves once per flush.
   - Preserve mixed-kind batching and generated-ID behavior.
3. Relationship upsert-by-identity:
   - Ensure all destination leaves.
   - Route conflict detection through the graph-wide property-uniqueness
     mechanism.
   - Reproduce the version 1 result for identical identity properties both
     within one kind and across different kinds; do not admit a cross-kind
     duplicate that version 1 rejected.
4. Cypher `CREATE` paths:
   - Ensure the edge kind before executing generated SQL.
   - Avoid creating edge leaves while resolving node-only kinds.
   - Keep Cypher `MERGE` outside this change; the PostgreSQL translator does not
     currently implement it.
5. Direct updates:
   - Target the transaction's graph parent.
   - Include `graph_id` and mapped `kind_id` where the relationship object makes
     them available.
   - Return a not-found or invariant error if the ID and kind disagree.
6. ID-only deletion:
   - Always scope by target graph.
   - Resolve the kind through mandatory identity metadata and join the graph
     parent on both ID and kind; characterize pruning if the plan remains
     parameterized.
7. Cypher-generated updates and deletes:
   - Keep graph-scoped table rendering.
   - Retain or introduce kind predicates when the query statically knows them.
8. Path hydration:
   - Keep graph scoping mandatory.
   - Resolve every ID through `(graph_id, edge_id)` identity metadata and join
     edges with the resolved kind while preserving ordinality and duplicates.
   - Measure actual executor leaf scans across many kinds.
   - If hydration becomes material, evaluate carrying `(edge_id, kind_id)` in
     internal traversal state as a separate optimization without changing the
     public path result.
9. Enforce kind immutability according to Decision 3.
10. Enforce `id` and `graph_id` immutability and transactional identity-registry
    cleanup for delete and cascade.
11. Verify graph wipe and node cascade against the new hierarchy. Managed graph
    catalog rows are immutable during runtime: target roles cannot delete them,
    and raw deletion is rejected. WipeGraph clears data/claims while retaining
    graph/schema rows, logical constraints, and physical partitions; dropping
    those objects requires explicit full schema teardown.

Exit criteria:

- Direct, batch, and Cypher relationship mutation suites pass with at least two
  graphs and three edge kinds per graph.
- Every supported write path provisions leaves before insertion.
- ID-only operations affect at most one edge within the target graph.
- Property uniqueness behavior matches the recorded contract.
- Path order, direction, repeated-node handling, and edge hydration remain
  unchanged.

### Phase 5: explicit legacy migration

Add a versioned migration from the current one-level layout to the hierarchical
layout. The migration must be separate from ordinary schema assertion.

Persist these explicit states: `v1`, `v1_to_v2_in_progress` with per-graph
progress, `v2_pending_acceptance`, and `v2_accepted`. Only `v2_accepted` is a
writable target-runtime layout state, and ordinary operations still require its
smoke/fallback substate to have passed. `v2_pending_acceptance` remains fenced
and is the last point at which an in-place downgrade is supported.

#### Preflight

The dry-run phase must:

1. Acquire the bounded coordination lock needed to exclude a simultaneous
   state-changing migration while taking its coherent read-only snapshot, then
   release it when the command ends. This preliminary lock is not a writer
   fence and is not held across CLI invocations.
2. Read the schema layout version and classify the edge topology.
3. Refuse a mixed or unrecognized layout unless it is a recognized resumable
   migration state.
4. Enumerate every graph partition and its row count.
5. Enumerate distinct `kind_id` values in each graph.
6. Verify that every stored `kind_id` has a valid kind catalog entry, or stop
   with a remediation report. Never discard or silently quarantine rows.
7. Verify the legacy `(graph_id, id)` uniqueness invariant.
8. Enumerate every managed edge-property constraint, verify its graph-wide
   uniqueness, and capture the exact version 1 conflict semantics.
9. Inventory root and graph-level indexes, constraints, ownership, grants, and
   sequence dependencies that must be preserved.
10. Inventory or reject dependent objects that are not managed by DAWGS,
   including user triggers, row-level security policies, replica identity,
   comments, rules, and publication membership. Replacing a partition must not
   silently drop external database behavior.
11. Identify every application writer role/login path. Require a versioned,
    directly authenticated, repeated writer-probe file for each probeable path
    (plus versioned external-control evidence where direct probing is
    impossible), bind the complete nonsecret descriptor set and immutable
    credential-version or native-role retirement strategy, and verify that the
    migration role can fence them without losing its own DDL/DML access. Also
    require an explicit, installation-dedicated rollback-preparation role that
    is `NOLOGIN PASSWORD NULL` and access-free, distinct from owner/operator/
    writer/target identities, plus a scoped sole-active credential issuance
    plan for post-rollback handoff; never infer either by name.
12. Estimate required temporary disk space and identify the largest graph.
13. Confirm that the server version is supported.
14. Check long-running transactions, available lock slots, expected WAL volume,
    and `max_locks_per_transaction` capacity for the staged hierarchy.
15. Report the locks, lock timeout policy, and expected maintenance mode to the
    operator.
16. Require a canonical application schema manifest for every installed graph.
    The manifest supplies declared-but-empty edge kinds that cannot be recovered
    from the version 1 database. Reject an incomplete manifest rather than
    producing installation-dependent topology.
17. Require versioned pre-migration backup evidence bound to this installation
    and current database identity, with a verified restore rehearsal, retention,
    and freshness inside the WP0 policy. Missing, stale, or wrong-source evidence
    blocks mutation.

#### Migration preparation

The dry-run report is advisory, not permission to mutate. Execution must repeat
all data-, catalog-, dependency-, role-, and capacity-sensitive checks after the
write fence is active so changes between dry run and maintenance cannot cross
the cutover boundary.

1. On each state-changing invocation, acquire the session layout lock on that
   command's dedicated control connection, validate the migration ID and
   durable checkpoint, and release the lock when the command ends. Durable
   journal state, not a connection-spanning advisory lock, coordinates resume.
2. Stop application writers and drain version 1 traffic. Before changing the
   database, directly authenticate every writer-probe file bound by preflight
   and verify its expected session/current-user identity. A role name or
   operator `SET ROLE` is not an authentication probe.
3. In one PostgreSQL transaction, create the minimal journal/control objects,
   record `v1_to_v2_in_progress` at `fence_installing` with the manifest,
   source fingerprint, complete rollback/ACL/default-ACL snapshot, planned
   fence actions, approved backup-evidence digest, and progress metadata, and
   apply every transactional database
   local ownership, ACL/default-ACL, managed-guard, and other fence change.
   Advance to `fence_installed_unverified` and commit. A rollback leaves V1
   unchanged; an unknown commit is resolved from durable catalog and journal
   facts before retrying. No managed database-local authorization change may
   precede this journal. Cluster-global LOGIN/password/membership changes are
   excluded from this database-local snapshot.
4. Hold that database-enforced writer fence through every backfill, graph
   cutover, final validation, and acceptance or complete rollback handoff. An
   advisory lock alone is not a writer fence because version 1 binaries and raw
   SQL do not honor it.
5. Terminate every old session, then verify the database fence through fresh
   direct connections from the exact bound writer-probe files while those
   credentials remain valid. Accept only a designated database CONNECT denial
   or, after exact identity verification, designated denials from rollback-only
   root and child DML, `COPY`, applicable DDL, inherited privileges, exact
   sequence privileges, login defaults, `session_user`, and managed write
   functions. Persist the per-identity matrix as
   `fence_database_verified_retirement_pending`; no managed data or topology may
   change first.
6. Only after that durable receipt, retire every old credential and stop non-
   database writer paths. A provider-managed probe binds a nonsecret immutable
   credential version and requires signed evidence for retiring that exact
   version. For a dedicated native PostgreSQL role, `verify-fence` uses cluster-
   scoped coordination and atomically records the receipt while executing
   `ALTER ROLE ... NOLOGIN PASSWORD NULL`; it never stores a password verifier.
   Shared roles require explicit cluster-wide impact approval or are a preflight
   blocker.
   The command proves the endpoint healthy, old direct authentication is
   rejected, and no old sessions remain, revalidates the database fence digest,
   and advances atomically to `fence_verified`. A crash before the database-
   probe receipt requires usable writer-probe files again; it may not substitute
   `SET ROLE`, an unbound wrong secret, or ordinary attestation.
7. Repeat every mutable preflight check in a fresh coherent read-only snapshot
   under the verified fence. Compare its authoritative digest with the
   preliminary report, permit only the exact journaled database-fence, session-
   drain, and bound credential-retirement deltas, and require a new report and
   operator confirmation for any other change.
8. Prepare global enforcement resumably. Create empty registries in a short
   transaction, then populate identity and property claims in deterministic
   `(graph_id, id)` keyset chunks. Each chunk commits its idempotent writes and
   count/high-water/digest checkpoint atomically; resume validates those facts
   and resolves an unknown commit before replay.
9. After complete registry reconciliation, use one separately budgeted atomic
   transaction to install the claim/delete/immutability enforcement, replace
   the root key with `(id, graph_id, kind_id)`, install the corrected cascade
   and graph-safe helpers, rebind supported dependencies, and mark global
   preparation complete. The writer fence and V1 key remain authoritative
   during chunked backfill, and at no point may both the V1 key and the registry
   enforcement be ineffective.

A crash at any checkpoint must be classified from the journal and catalog as
resumable or explicitly roll-backable. It must not leave an unjournaled
database fence or require one PostgreSQL transaction to contain the unbounded
registry backfill.

#### Per-graph conversion

For each graph, in a deterministic order:

1. Create a staging graph edge table with the same columns, defaults, storage
   settings, and ownership as `edge_<graph_id>`, partitioned by `LIST (kind_id)`.
2. Create one staging leaf for the union of every kind stored in that graph and
   every kind declared for it in the required canonical application schema
   manifest. The version 1 database does not persist graph-to-kind declarations,
   so do not infer declared-but-empty kinds from the global kind table.
3. Add and validate a graph-bound check constraint to support attachment.
4. Copy rows from the legacy graph partition without regenerating IDs.
5. Build or attach the required primary, endpoint, base, property, identity, and
   graph-wide uniqueness objects using collision-free staging names. PostgreSQL
   index names are schema-wide; renaming the legacy table does not rename its
   indexes.
6. Analyze the staged leaves.
7. Validate before cutover:
   - Total row count.
   - Count per kind.
   - Minimum and maximum IDs.
   - Endpoint and kind aggregates.
   - A deterministic content checksum that includes properties.
   - Identity and property-uniqueness registry counts and mappings.
8. Begin one PostgreSQL transaction for the graph cutover while the global write
   fence remains active, and lock the legacy graph partition against writes.
9. Detach the legacy table from `edge` within that transaction.
10. Rename it to a versioned, clearly marked legacy name.
11. Rename the detached legacy parent indexes and their managed property indexes
    to versioned legacy names, using a catalog-recorded mapping rather than
    derived patterns.
12. Rename and attach the staged partitioned parent for the graph bound.
13. Rename the new logical parent indexes to canonical names after the legacy
    names are free. Generated leaf index names may remain implementation details,
    but their attachment and validity must be checked through the catalogs.
14. Reapply and validate the legacy table's owner, table ACL, sequence grants,
    comments, and every supported dependent object on the new hierarchy.
15. Validate catalog attachment and rerun row/count checks through `edge` and
    `edge_<graph_id>`.
16. Mark the graph complete in the same transaction and commit only after every
    detach, rename, attach, dependency, and catalog validation step succeeds.
    A crash or error must roll the entire graph cutover back to the still-attached
    legacy partition.
17. Keep the detached legacy table restricted to the migration role. Restoring
    application grants on the active hierarchy must not make the legacy copy
    writable.

The implementation must account for PostgreSQL's partitioned-index attachment
rules instead of assuming that index names alone establish equivalence.

#### Finalization

1. Preserve the original `edge_id_seq` object and its increment, minimum,
   maximum, cache, cycle, ownership, ACL, and `is_called` configuration. Preserve
   the pre-migration effective next allocation and advance it only when required
   to exceed the greatest stored edge ID. Never rewind it after deleted IDs,
   rolled-back allocations, or an empty-table migration.
2. Validate all graphs through the root and graph parents.
3. Run `ANALYZE` on the new hierarchy.
4. Mark the layout `v2_pending_acceptance`; keep the database write-fenced.
5. Close preparation-era pools and invalidate prepared statements or cached
   plans that may retain old relation metadata. Do not construct the target
   runtime pool before its separately authenticated handoff and smoke pass.
6. Run read-only migration-tool smoke queries before accepting writes. Normal
   application runtime must still reject `v2_pending_acceptance`.
7. Record the detached-table retention and post-acceptance soak deadline.
8. Authenticate through a separate connection as the exact target deployment
   role recorded by preflight; it must satisfy the non-owner/distinct-role
   policy above. In one PostgreSQL transaction, revalidate that identity and the
   write fence, persist the rollback-cutoff timestamp and accepted checksums, mark
   `v2_accepted` with
   `post_acceptance_smoke_status=pending`, and apply only the target ACL matrix.
   Do not restore reusable version 1 credentials. Committing this transaction
   is the categorical, auditable rollback cutoff.
9. After that commit, use the separately authenticated target connection to run
   the bounded version 2 read/write/delete smoke and durably mark it passed (or
   record the approved empty-install privilege-only fallback). Normal runtime
   remains fail-closed while smoke is pending or failed. An unknown smoke commit
   is reconciled from database facts before replay; a true post-acceptance
   failure is forward-fix or restore/reconcile, never an in-place version 1
   rollback.
10. Construct or reset target-runtime pools only after the durable smoke result
    passes, then resume reads and writes.
11. Drop legacy tables only through a separate cleanup action after target
   writes complete the documented soak, the current identity-epoch target smoke
   receipt remains passed/fallback, a fresh backup is verified, and acceptance
   checks succeed.

Restoring an accepted backup under a new database or role identity requires an
explicit scope-aware rebind using its migration ID or fresh-bootstrap operation
ID. Rebind installs the exact owner/default-ACL/final privilege matrix, removes
stale target/writer grants, rejects role collisions, appends a nonce-bound
receipt, atomically advances the current database binding/identity epoch and
receipt pointer, resets smoke to pending, and requires a new authenticated
target-role smoke; a pre-restore role's receipt cannot reopen runtime traffic.

An in-progress migration may rebind a changed database OID/name, detectable
restore incarnation, or both only within the same stable PostgreSQL cluster/
resource identity and only when every writer role, credential-version/
retirement fact, database fence, and external writer control still matches.
Cross-cluster in-progress remapping is refused; use a pre-fence V1 backup and
rerun preflight instead.

A `v1_rolled_back` database is also identity-bound. Only the unchanged original
database or a provider-attested non-restore HA failover keeps pending/passed
handoff, and only when its current rollback scope, receipt pointer, grants, and
role facts match. Every detectable same-cluster restore changes database OID/
name, restore incarnation, or both and must rebind that exact scope, revoke
copied preparation-role grants/default ACLs, advance the identity epoch, reset
handoff to pending, and complete a new nonce-bound credential handoff before
the preparation client can open it. Cross-cluster rolled-back rebind is refused;
use a pre-fence V1 backup and rerun preflight.

The stable installation UUID is ownership metadata, not current restore
identity. Each supported deployment profile must expose a trusted current
cluster/resource and restore-incarnation identity to both migrator and runtime;
profiles without it reject the restore modes they cannot distinguish rather
than treating a copied installation UUID as proof.

#### Resume and rollback

- Re-running the migration must skip completed graphs only after validating
  their attached topology and data checks.
- A failed pre-cutover graph conversion should drop or reuse only its explicit
  staging objects.
- Before `v2_accepted`, and only while application writers remain fenced,
  rollback may swap graph parents back under maintenance locks and validate them
  before reopening the last version 1-compatible release.
- A complete pre-write rollback must reattach every legacy graph child and
  restore them in reverse cutover order, then restore the captured root primary
  key, property-constraint semantics, partitioned-index attachment state,
  identity and immutability enforcement objects, trigger state, schema version,
  sequence attributes, owners, ACLs, and supported dependencies. Restore the
  sequence's increment, bounds, cache, cycle, ownership, and grants, but compute
  its effective next allocation monotonically according to Finalization step 1;
  rollback must never rewind it. Structural rollback records `rolled_back` with
  `rollback_handoff_status=pending` and retains every database writer fence.
  A separate, authenticated preparation-role connection whose login OID is
  distinct from the owner, operator, historical, and target roles must then
  atomically apply the recorded V1 preparation ACL/default-ACL matrix, remove
  the fence, and mark handoff passed. Retired historical roles/passwords remain
  disabled. Only after that durable handoff may the temporarily supported
  version 1 release reopen the database so migration can be rescheduled.
- Swapping one graph parent back without restoring the root state is a resumable
  mixed migration state, not a completed rollback, and application writers must
  remain fenced.
- Do not delete the active table or legacy table through broad patterns or
  inferred names.
- The acceptance transition is categorical: it is allowed only after all
  topology and data checks pass, and it records the rollback cutoff. After
  `v2_accepted` or the first version 2 application write, retained legacy tables
  are stale. In-place downgrade must be rejected; recovery is a forward fix or
  restore/reconcile from a verified backup.

Exit criteria:

- Migration tests cover empty, small, multi-graph, multi-kind, constrained, and
  interrupted databases.
- A dry run produces a complete operator report without mutations.
- Resume and pre-write rollback are demonstrated.
- The target `pg` runtime rejects `v1`, `v1_to_v2_in_progress`,
  `v2_pending_acceptance`, an accepted marker with smoke pending, and a rolled-
  back marker with handoff pending. It accepts only a catalog-consistent
  `v2_accepted` state with passed/fallback smoke. A truly uninitialized database
  may be bootstrapped as version 2, but remains fail-closed until its separate
  target-role handoff and smoke pass.
- No migration code runs during normal driver startup.

### Phase 6: tooling, diagnostics, and observability

Affected areas:

- `drivers/pg/optimize.go`
- `cmd/retriever/database.go`
- `cmd/graphbench/postgres.go`
- `cmd/benchmark/main.go`
- GraphBench plan parsing and invariant tests
- `cmd/plancorpus/capture.go`
- `retriever/load.go`
- `tools/dawgrun/pkg/commands/db.go`
- `integration/harness.go`

Tasks:

1. Make storage optimization enumerate and maintain all edge leaves through
   `pg_partition_tree`.
2. Make retriever validate that `edge_<graph_id>` is a partitioned parent and
   optionally report missing or invalid leaves.
3. Sum leaf and index bytes when reporting graph edge storage.
4. Analyze or vacuum the appropriate parent/leaves without double-counting
   results.
5. Update plan parsing so valid leaf scans such as
   `edge_<graph_id>_<kind_id>` are recognized.
6. Replace graph-child truncation with the registry-aware reset/delete mechanism
   proven by the execution plan and test that all leaves/claims are emptied
   without `TRUNCATE CASCADE` or unrelated data loss.
7. Add structured diagnostics for:
   - Missing edge-kind leaves.
   - Leaf creation duration and errors.
   - Topology drift.
   - Total graphs, graph edge parents, and edge kind leaves.
   - Migration phase and per-graph progress.
8. Avoid logging edge properties or other user data in topology diagnostics.
9. Provide a read-only health query or command that checks parentage, bounds,
   index validity, and schema layout version.
10. Give every AssertSchema-then-use caller a locked startup boundary. Retriever,
    GraphBench, benchmark, plan corpus, and dawgrun accept only an already
    operational target-role PostgreSQL connection, run the read-only state/
    target-identity readiness check first, and use `AssertSchema` only for
    restricted graph/kind reconciliation. They never bootstrap with an operator
    credential. Dawgrun's `--init-graph` creates only a missing graph inside
    operational V2.
11. Bootstrap disposable benchmark and CI databases in a separate setup step
    with operator plus target connections, close operator resources, and expose
    only the accepted target DSN to tools and ordinary integration tests.
    `CONNECTION_STRING` therefore names a prebootstrapped target database;
    fresh-bootstrap/state/role tests use the dedicated physical/migration suite.

Exit criteria:

- Existing operational commands work against multiple kind leaves.
- Every named tool refuses uninitialized, pending, or wrong-role connections
  before schema mutation while accepted-target reconciliation still works.
- Reported edge bytes match the sum of the leaf relations and indexes.
- Plan diagnostics identify the exact leaves touched by benchmark queries.

### Phase 7: tests and performance validation

#### Unit tests

Add tests for:

- Graph and kind partition naming.
- Graph-parent and leaf DDL formatting.
- Catalog topology decoding.
- Wrong-parent, wrong-bound, wrong-key, and ordinary-table drift.
- Logical partitioned index and constraint reconciliation.
- Graph-wide edge-property conflict and registry behavior across kind leaves.
- Property-registry value encoding with the same text, collation, SQL `NULL`,
  and missing-property semantics as the version 1 expression index.
- Schema-version state transitions and target-runtime refusal errors.
- Stable `pg` driver registration, constructor, and DSN behavior.
- Schema canonicalization for `Schema.Graphs` and `DefaultGraph`.
- Kind and topology cache invalidation after kind-definition rollback, graph
  rollback, leaf rollback, or ambiguous DDL failure.

#### PostgreSQL integration tests

Use PostgreSQL-scoped tests for physical layout. Do not add PostgreSQL-specific
assertions or skips to the backend-equivalent cases under
`integration/testdata/cases` or `integration/testdata/templates`.

Cover:

1. In the disposable PostgreSQL physical suite, operator-authenticated empty-
   database version 2 initialization, separate target handoff/smoke, operator-
   pool refusal afterward, and two graphs with at least three edge kinds each.
   Ordinary integration suites start from an accepted target-role database.
2. Table-to-leaf routing verified through `tableoid` and catalog bounds.
3. Full and name-only default graph definitions.
4. Secondary graph creation and driver reopen/reconciliation.
5. A kind declared at startup but unused.
6. A kind introduced dynamically after graph creation.
7. Concurrent leaf ensure from separate drivers and transactions.
8. Failure and rollback during kind definition and leaf creation, followed by a
   successful retry that proves no nonexistent kind ID or leaf remained cached.
9. Direct single-edge creation and update.
10. Buffered mixed-kind creation.
11. Relationship upsert by identity properties.
12. Cypher relationship creation, update, delete, and return.
13. Graph-wide property uniqueness and conflict behavior within one kind and
    across different kinds.
14. Rejection of an explicit duplicate `(graph_id, id)` insertion across kinds.
15. Database rejection of raw updates to `id`, `graph_id`, or `kind_id`, with no
    stale identity or property registry entry.
16. ID-only update, delete, and hydration.
17. Node deletion with colliding node IDs in two graphs.
18. Graph-row cascade with retained empty partitions, graph wipe, and
    graph-scoped truncate.
19. Property indexes and constraints created before and after a new leaf.
20. Reassertion that repairs an intentionally missing leaf or invalid index.
21. Kind omission that preserves the existing leaf and its data.
22. Adding a graph-wide property constraint after rows exist, including
    duplicate-data rejection and writes racing the backfill.
23. Dropping and re-adding a property constraint without stale registry entries.
24. Missing, JSON-null, empty, and collated text property values matching
    version 1 uniqueness behavior.

#### Migration tests

Instantiate a frozen fixture matching the exact final version 1 layout in a test
database, seed it, and run the migration. Cover:

- Empty edge tables.
- Several graphs and kinds.
- Skewed kind sizes.
- User property indexes and constraints.
- Deleted IDs and advanced sequence state.
- Declared-but-empty kinds supplied through the required canonical application
  schema manifest.
- Large properties and null/empty property bags allowed by current contracts.
- A preflight failure for an orphaned kind ID.
- Owners, table/sequence grants, comments, and each supported dependent-object
  type, plus preflight rejection for unsupported dependencies.
- An attempted late writer after the fence is established.
- A failpoint before and after each graph cutover.
- Resume after partial completion.
- Full pre-write rollback to the legacy root, graph children, constraints,
  indexes, enforcement objects, version, sequence, and grants.
- Idempotent validation after completion.
- Migration from a frozen fixture matching the exact last released version 1
  schema, not only a hand-simplified approximation.
- Stable `pg` driver name, constructor, and DSN across the migration.
- Operator-authenticated initialization of a truly uninitialized database as
  version 2, followed by separately authenticated target-role handoff/smoke and
  successful target-runtime open of operational `v2_accepted`.
- Target-runtime refusal of `v1`, `v1_to_v2_in_progress`, and
  `v2_pending_acceptance`.
- Proof that no target application write can occur before `v2_accepted` with a
  passed/fallback smoke result.
- Full pre-write downgrade followed by a safe version 1 reopen.
- Rejection of in-place downgrade after acceptance or a target-layout write.
- Safe failure of the last version 1 binary against a version 2 database.

Validate rows through both SQL and the public DAWGS APIs. Compare edge IDs,
graph IDs, endpoints, kinds, properties, counts, and path results.

#### Query-plan tests

For representative traversal, count, mutation lookup, and hydration shapes,
capture `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` and assert behavior rather than
fragile full plan text:

- One statically known kind touches one leaf.
- A set of known kinds touches only that set of leaves.
- An untyped relationship query can touch all graph leaves.
- No query touches another graph's leaves.
- ID-only hydration remains correct and its cross-leaf behavior is recorded.
- Custom and generic prepared plans are measured separately.
- Leaf indexes are used for endpoint-constrained traversal where expected.

Do not require a specific pruning mechanism when PostgreSQL versions legitimately
differ; assert the set of executed leaf relations and correctness.

#### Scale and performance matrix

At minimum benchmark:

- 1 graph × 1 kind.
- 1 graph × 10 kinds.
- 1 graph × 30 kinds.
- Multiple graphs × 30 kinds.
- The agreed maximum supported topology.
- Uniform and highly skewed row distributions.

Record:

- Planning and execution time separately.
- End-to-end warm and cold latency.
- Rows and buffers per leaf.
- Direct and batch insert throughput.
- Upsert throughput with identity and graph-wide property enforcement enabled.
- Driver startup and schema assertion latency.
- DDL ensure latency under no contention and contention.
- Migration copy throughput, cutover lock time, WAL volume, and peak temporary
  storage.
- Catalog and relcache memory indicators where practical.
- On-disk heap and index bytes.

Compare the existing and candidate index shapes after correctness is stable.
Select indexes from measured workload behavior rather than assuming that the
current kind-containing keys remain optimal inside a kind leaf.

#### Required validation commands

After implementation changes:

```bash
make format
make test
```

With a benign PostgreSQL `CONNECTION_STRING`, run the PostgreSQL-selected full
suite:

```bash
make test_all
```

Run the shared suite against Neo4j separately to prove that shared fixtures and
semantics were not made PostgreSQL-specific. Also run `git diff --check` and any
new migration command's dry-run and validation modes.

Exit criteria:

- All unit, integration, migration, and shared semantic suites pass.
- Physical-layout assertions are confined to PostgreSQL-specific tests.
- Pruning and performance results satisfy the budgets recorded in Phase 0.
- No unexplained regression is hidden by averaging kind-aware and kind-agnostic
  workloads together.

### Phase 8: documentation and rollout

Update:

- `README.md` for setup, supported PostgreSQL versions, and migration workflow.
- `docs/postgresql_translation.md` for graph/kind table routing and pruning.
- PostgreSQL operational documentation for partition counts, maintenance,
  health checks, and storage reporting.
- GraphBench documentation for the new plan and size interpretation.
- The migration command's operator runbook.
- A fleet-ledger template, migration-wave checklist, rollback-cutoff record, and
  version 1 deprecation/EOL notice.
- Archived-upgrade guidance for installations discovered after retirement.

Recommended rollout sequence:

1. Designate the last version 1-compatible `pg` release. Freeze it except for
   critical fixes, publish its migration deadline, and retain its exact binary
   and schema fixture as the temporary pre-write rollback baseline.
2. Merge read-only layout classification and migration dry-run support without
   changing the default storage of that preparation release. Make that release
   fail closed on a version 2 marker.
3. Build target-layout initialization and writes behind an internal development
   gate. Do not expose a generally available greenfield-only mode.
4. Create a fleet ledger of every supported installation. Record its owner,
   application version, PostgreSQL version, graph/schema manifest, size,
   rehearsal status, cutover date, layout state, validation result, rollback
   cutoff, soak end, and legacy-table cleanup status.
5. Publish the version 1 deprecation, transition window, retirement date,
   downtime expectation, credential-rotation requirement, and late-upgrade
   policy.
6. Validate the version 2 runtime, migration, rollback, and performance on
   PostgreSQL 16 and PostgreSQL 18. Rehearse representative and largest
   installations on restored backups; record duration, lock time, WAL, disk
   amplification, and validation output.
7. Ship one transition release set containing the target-only `pg` runtime,
   offline migrator, dry-run, validation, resume, pre-write rollback, health
   checks, and operator runbook. It must never create version 1.
8. Migrate installations in controlled waves. For each installation, first
   take and verify the scheduled final pre-migration backup without a migrator
   database mutation, and issue the immutable source-bound backup evidence.
   Verify and stage the exact checksummed preparation release/client, rollback
   deployment configuration, and connection-artifact template without issuing
   its credential or opening the dormant role. If the backup, evidence, or
   staged artifact changes, regenerate preflight and confirmation.
9. Run status and the preliminary coherent read-only preflight while V1 is
   online, using the canonical schema manifest, capacity and exact backup
   attestations, a separately authenticated target-role connection, the
   preparation-credential plan, and the complete repeated writer-probe/control
   set. Bind the staged preparation artifact/deployment checksum and review its
   report before entering the maintenance window.
10. Stop writers and drain traffic. Authenticate the bound writer-probe files;
   atomically journal the rollback facts with every PostgreSQL role/ACL fence
   change, terminate old sessions, and durably prove the database fence through
   fresh direct probe connections. Only then retire external credentials,
   complete `verify-fence` with its evidence, and run the authoritative fenced
   preflight. Renew confirmation if its digest differs from the approved
   preliminary report.
11. Migrate, validate, and run read-only smoke checks while the state remains
    `v2_pending_acceptance`. Roll back or resume before acceptance if any gate
    fails. After rollback, issue the preparation credential, complete its
    handoff, switch to the exact staged preparation client/connection artifact,
    and pass readiness before reopening V1 traffic.
12. Atomically record the rollback cutoff, mark `v2_accepted` with target smoke
    pending, and grant only the separately authenticated target role as
    specified in Phase 5. After commit, run and durably pass the bounded target-
    role smoke before constructing runtime pools or resuming traffic. Treat any
    true post-acceptance failure as forward-fix or restore/reconcile.
13. Keep the fleet ledger open until every supported installation reports
    `v2_accepted`. The only alternative disposition is formal decommissioning
    and removal from the supported inventory under the published data-retention
    policy. An active, missing, or merely unreachable version 1 installation
    blocks retirement; silence is not evidence of migration.
14. Retain detached version 1 tables through the per-installation soak period,
    but label them stale as soon as target writes resume. After a fresh backup,
    acceptance checks, and operator approval, remove them with the dedicated
    cleanup command and record completion.

### Phase 9: retire the version 1 layout

Retirement is a planned deliverable, not an open-ended compatibility period.
Apply these gates in order:

1. **Runtime gate:** the transition `pg` runtime has never served version 1 or a
   mixed state, and deployment controls prevent the last version 1 binary or
   credentials from reaching version 2 databases.
2. **Database gate:** every supported installation has passed version 2 smoke,
   correctness, and performance checks; completed its rollback/soak window; and
   removed detached legacy tables through the approved cleanup path. A pending
   cleanup action blocks retirement for that installation.
3. **Fleet gate:** the ledger shows every supported active installation at
   `v2_accepted`. Formal decommissioning and removal from supported inventory is
   the only alternative. Inactive, abandoned, or unreachable status alone is
   not a disposition and blocks the gate.
4. **Release gate:** the announced version 1 compatibility and rollback window
   has elapsed, and operators have received the final EOL notice.
5. **Code gate:** remove version 1 bootstrap, runtime schema reconciliation,
   feature flags, interchangeable-layout abstractions, and steady-state tests.
   Keep only minimal detection needed for an actionable unsupported/migration
   error outside the offline migrator.
6. **Migration-path gate:** retain a separately runnable and tested version 1 to
   version 2 migrator, or a versioned archived transition artifact, for the
   published late-upgrade horizon. An installation outside that horizon must
   first use the archived transition release or an export/restore procedure; it
   does not justify keeping version 1 runtime support in current `pg`.
7. **Test and documentation gate:** retain the frozen final version 1 fixture
   and migration regression suite as long as the upgrade artifact is supported.
   Remove version 1 from normal setup documentation, examples, runtime CI, and
   operational runbooks; replace it with EOL and archived-upgrade guidance.
8. **End-of-horizon gate:** when the announced late-upgrade horizon expires,
   remove the version 1 migrator, fixture, and regression suite from active code
   and CI. If support policy requires historical recovery, retain only an
   immutable, checksummed transition-release archive with its source and
   runbook. Current `pg` keeps only the actionable unsupported-layout error.

Retirement is complete when version 2 is the only layout created or served by
current `pg`, all supported installations have completed migration and cleanup,
and no production release branch depends on version 1 runtime behavior.

## Risk Register

| Risk | Consequence | Mitigation |
|---|---|---|
| Composite primary key weakens graph-local ID enforcement | ID-only operations can affect or hydrate multiple edges | Preserve `(graph_id, id)` through strict identity enforcement and concurrency tests |
| Dynamic kind is defined without a leaf | Inserts fail at routing time | Add edge-specific ensure at every write boundary and catalog-backed reconciliation |
| Multiple processes race on leaf creation | DDL errors, blocking, or inconsistent cache state | Use transaction advisory locks and recheck catalogs inside the lock |
| Schema operations acquire locks in different orders | DDL/write deadlock | Use one migration/graph/sorted-kind lock order and test concurrent creation |
| Driver lacks runtime DDL privileges | Dynamic kind writes fail | Require narrowly scoped leaf DDL rights and block migration in preflight until the installation supplies them |
| Graph cache is populated before commit | Later assertions trust nonexistent objects | Publish only committed topology and invalidate uncertain entries |
| Cross-kind property enforcement is incorrect or contended | Existing uniqueness semantics break or writes regress | Preserve graph-wide enforcement with a transactional registry/equivalent, concurrency tests, and a measured budget |
| Property constraint changes race existing writes | Registry state is incomplete or stale | Lock the graph lifecycle, enforce before backfill, validate before publish, and clean up atomically on drop |
| Kind-less queries plan many leaves | Planning or execution regression | Benchmark supported partition counts, preserve graph scoping, and optimize only measured hot paths |
| Generic prepared plans do not prune kind arrays | Expected read gain is not realized | Measure custom/generic modes and preserve typed/materialized predicates where appropriate |
| Existing index parser rejects partitioned index text | Graph assertion fails after migration | Replace text regex discovery with catalog-backed logical index discovery |
| Migration attachment holds strong locks | Extended write outage | Require a rehearsed maintenance window and use validated staging constraints |
| A writer commits after staging copy | Cutover silently loses an edge mutation | Hold a database-enforced application-role write fence from before copy through acceptance |
| Migration doubles edge storage temporarily | Disk exhaustion | Preflight disk needs and migrate on a restored production-sized database first |
| Legacy indexes retain canonical names | Staged index creation or final rename fails | Use staging names and an atomic catalog-driven legacy/new index rename sequence |
| Sequence finalization uses only `max(id)` | Previously allocated IDs can be reused | Preserve the original effective next allocation and only advance, never rewind |
| External ACLs or dependent objects are omitted | Permissions or database behavior changes after cutover | Inventory, reproduce, and validate supported dependencies; reject unsupported ones in preflight |
| Detached legacy cleanup affects active identity metadata | Active rows lose enforcement state | Make cleanup a separate, explicit operation and test registry behavior before dropping legacy tables |
| Node cascade ignores graph | Cross-graph edge deletion | Correct the trigger and add colliding-ID integration coverage |
| Operational tools read only parent relation size | Misleading zero or small edge size | Aggregate through `pg_partition_tree` |
| Too many small leaves increase maintenance cost | Autovacuum, catalog, and planning overhead | Establish supported topology limits and monitor leaf counts |
| One transaction creates too many partition/index objects | `max_locks_per_transaction` exhaustion | Size the lock budget in preflight and create leaves in bounded transactions where safe |
| Version 1 and target binaries overlap | A legacy writer corrupts or fails against version 2 | Terminate old sessions, rotate or revoke their role, give only the target release new credentials, and enforce the compatibility matrix |
| A supported installation is missed | Retirement strands an active database on version 1 | Maintain an owner-backed fleet ledger and block retirement until each installation is verified on version 2 or formally decommissioned |
| Version 1 compatibility persists indefinitely | Both layouts continue consuming engineering and CI capacity | Publish EOL dates and make code, release, fleet, and test removal gates part of delivery |
| Retained legacy tables are mistaken for a live rollback copy | Post-acceptance writes are lost during downgrade | Record the acceptance cutoff, label retained tables stale, and reject in-place downgrade after target writes resume |
| Archived migration tooling decays | A late installation has no safe upgrade path | Keep the frozen version 1 fixture and migration suite active for the announced late-upgrade horizon |

## Delivery Boundaries

### Program delivery includes

- One stable `pg` driver whose sole destination and steady-state layout is
  hierarchical graph/kind edge partitioning.
- Operator-assisted version 2 initialization and separate target handoff for
  truly uninitialized databases, plus mandatory migration of every supported
  initialized version 1 installation, including empty ones.
- Catalog-backed topology discovery and reconciliation.
- Declared and dynamic edge-kind leaf provisioning.
- Direct, batch, and Cypher write support.
- Recorded edge identity and property uniqueness contracts.
- Correct graph-scoped node cascade behavior.
- An explicit maintenance-window migration with dry-run, validation, resume,
  and pre-write rollback support.
- Fleet inventory, controlled migration waves, per-installation acceptance and
  cleanup, a published version 1 EOL, and removal of version 1 runtime support.
- Updated operational tools, tests, benchmarks, and documentation.

### Program delivery does not include

- A second PostgreSQL driver, permanent runtime layout selection, or long-term
  dual-layout support.
- A generally available fresh-only version 2 mode or an opt-out that lets a
  supported installation upgrade `pg` without migrating.
- Automatic migration during normal driver startup.
- Indefinite version 1 runtime support after the published retirement window.
- Kind partitioning for nodes.
- Automatic dropping or merging of unused edge-kind leaves.
- An online zero-downtime migration.
- Hash subpartitioning within a very large kind.
- A public relationship-kind mutation API.
- A public graph-row or graph-partition deletion API; WipeGraph remains the
  data-only reset and retains graph/schema topology.
- Changing the public path representation solely for pruning.
- Backend-specific semantic changes to the shared integration corpus.
- A literal two-column `LIST` root key, which PostgreSQL does not support.

## Pull Request Sequence

Keep the work reviewable and avoid mixing migration risk with query tuning:

1. **Catalog and contracts:** layout classification, topology model, ADR, and
   baseline artifacts.
2. **Target schema:** hierarchical DDL, naming, strict identity and graph-wide
   property enforcement, state marker, and cascade correction behind an
   internal gate.
3. **Lifecycle:** graph assertion, declared leaves, dynamic ensure, locking, and
   cache behavior.
4. **Writes and mutations:** direct, batch, Cypher, conflict targets, delete,
   update, and hydration correctness.
5. **Migration:** frozen version 1 fixture, command, manifest preflight,
   per-graph conversion, validation, resume, and pre-write rollback.
6. **Operations:** retriever, GraphBench, plan corpus, storage optimization, and
   health diagnostics.
7. **Performance and index tuning:** pruning validation, scale results, and any
   measured leaf-specific index changes.
8. **Transition release and rollout:** one release-gated artifact set, operator
   runbook, compatibility notes, fleet ledger, and production rehearsal
   evidence.
9. **Retirement:** after the fleet, soak, support-window, and backup gates,
   remove version 1 runtime/bootstrap branches and normal CI while retaining the
   supported offline upgrade artifact and frozen fixture.
10. **End of late-upgrade horizon:** remove the version 1 migrator, fixture, and
    migration CI from the active branch and publish or verify the immutable
    transition archive required by support policy.

Each pull request must preserve a coherent supported layout. Internal feature
gates may support implementation, but no generally available release may make
version 2 the default until initialization, migration, validation, resume,
pre-write rollback, old-binary fencing, documentation, and operational tooling
are release-ready together. Target-runtime startup must reject every unsupported
layout state clearly.

## Final Acceptance Checklist

- [ ] The approved architecture is hierarchical `LIST (graph_id)` then
      `LIST (kind_id)`.
- [ ] `pg` remains the only PostgreSQL driver; its name, constructor, package,
      URL handling, and DSN contract are unchanged.
- [ ] No divergent driver, runtime layout option, or generally available
      greenfield-only mode exists.
- [ ] Edge IDs remain database-enforced as unique by `(graph_id, id)`.
- [ ] `id`, `graph_id`, and `kind_id` are database-immutable after insertion,
      and registry cleanup is transactional on deletion and cascade.
- [ ] Edge property constraints remain graph-wide across relationship kinds and
      preserve version 1 conflict behavior.
- [ ] Adding, validating, dropping, and re-adding a property constraint is safe
      with existing and concurrent rows and preserves text/null semantics.
- [ ] Relationship kind is immutable.
- [ ] Empty-database version 2 initialization produces valid graph parents and
      kind leaves, append-only bootstrap receipts, and cannot create version 1.
- [ ] All declared edge kinds are provisioned for every asserted graph.
- [ ] Dynamic edge kinds are provisioned idempotently and safely across
      processes.
- [ ] Kind and topology cache state cannot outlive a rolled-back database
      change.
- [ ] Direct, batch, and Cypher writes route to the correct leaf.
- [ ] ID-only operations remain correct.
- [ ] Node deletion is scoped by graph and endpoint.
- [ ] Graph-row cascade, wipe, truncate, vacuum, analyze, and size reporting work
      across leaves, with the documented retained-partition behavior.
- [ ] Hard catalog drift produces an actionable error instead of silent repair
      or overwrite; only explicitly categorized, safely reconstructable managed
      absence is repaired under lock.
- [ ] The migration dry run, execution, validation, resume, and rollback paths
      are tested.
- [ ] The exact final version 1 schema fixture migrates successfully, including
      declared-but-empty kinds from the required application manifest.
- [ ] An operator-authenticated driver initializes truly uninitialized databases
      as version 2; only the separately authenticated target runtime accepts
      operational `v2_accepted` with passed/fallback smoke and it refuses
      legacy, mixed, and pending states.
- [ ] Every accepted application connection matches the recorded target
      session/current-user OID and name; operator, inherited/SET ROLE, and stale
      restored identities remain gated until the required handoff/rebind smoke.
- [ ] Pool hooks issue no managed-type SQL before readiness and load only
      schema-qualified composites on revalidated target sessions without
      discarding raw bootstrap connections or overwriting caller hooks.
- [ ] Full in-place rollback is allowed only before acceptance while writers
      remain fenced; post-acceptance downgrade is rejected.
- [ ] Sequence state never rewinds, and properties, indexes, constraints,
      ownership, ACLs, and supported dependencies survive migration.
- [ ] A database-enforced write fence prevents late legacy writers during copy
      and cutover.
- [ ] Version 1 sessions are terminated and their role or credentials cannot
      reconnect after cutover.
- [ ] Writer-probe credential continuity is bound by provider version or exact-
      role NOLOGIN/password-null retirement, with cluster-wide effects approved
      and no secret/verifier persisted.
- [ ] Retriever, GraphBench, benchmark, plan corpus, dawgrun, and ordinary
      integration tests require prebootstrapped accepted target connections and
      cannot implicitly bootstrap with an operator pool.
- [ ] Single- and multi-kind plans touch only the expected leaves.
- [ ] Kind-agnostic and ID-only performance stays within the Phase 0 budget.
- [ ] PostgreSQL 16 and PostgreSQL 18 integration suites pass.
- [ ] The shared Neo4j-backed semantic suite passes unchanged.
- [ ] README, PostgreSQL translation documentation, and the operator runbook are
      updated.
- [ ] Every supported installation has an owner, a recorded migration and
      validation result, a rollback cutoff, and cleanup completion; any removed
      installation has a formal decommission record.
- [ ] Every supported installation has removed its detached legacy tables after
      explicit post-soak approval and a verified backup before fleet retirement.
- [ ] Version 1 runtime, bootstrap, reconciliation, normal CI, and setup
      documentation are removed after the fleet and support-window gates.
- [ ] A separately testable offline upgrade artifact and frozen version 1
      fixture remain for the published late-upgrade horizon.
- [ ] At the end of that horizon, version 1 migration code and tests leave the
      active branch and CI; only the policy-required immutable transition
      archive and current runtime's unsupported-layout diagnostic remain.

## PostgreSQL References

- Partition key syntax and partitioned unique-constraint requirements:
  <https://www.postgresql.org/docs/current/sql-createtable.html>
- Declarative partitioning, subpartitioning, pruning, and maintenance:
  <https://www.postgresql.org/docs/current/ddl-partitioning.html>
- Partition attachment and table locking behavior:
  <https://www.postgresql.org/docs/current/sql-altertable.html>
- Native role login/password retirement:
  <https://www.postgresql.org/docs/18/sql-alterrole.html>
