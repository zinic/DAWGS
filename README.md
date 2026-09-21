# DAWGS

Database Abstraction Wrapper for Graph Schemas

![A Corgi Treat](logo_small.png)

DAWGS provides tools and query helpers for running property graphs on vanilla PostgreSQL without extra database
plugins. It exposes a backend abstraction for graph queries, with current backend support for PostgreSQL and Neo4j.
The query interface is built around openCypher, including a PostgreSQL SQL translator for environments that do not
support Cypher natively.

## Quick Start

Build the repository:

```bash
make build
```

Run unit tests:

```bash
make test
```

Run integration tests when a backend is available:

```bash
export CONNECTION_STRING="postgresql://dawgs:weneedbetterpasswords@localhost:65432/dawgs"
make test_integration
```

For an isolated temporary cluster using the locally installed PostgreSQL
server version, use the repository harness:

```bash
make test_integration_pg_temp
```

Because production V2 activation remains closed, the temporary-cluster target
adds the non-shipping `dawgs_rehearsal` build tag. To run the full unit and
integration validation against a script-owned cluster, use:

```bash
scripts/test-postgres --profile fast-read run -- \
  scripts/prepare-test-pg -- make test_all TEST_TAGS=dawgs_rehearsal
```

The harness uses the locally installed PostgreSQL server tools and removes the
cluster after the command finishes. Named instances can be kept alive for
debugging or run in parallel:

```bash
scripts/test-postgres up worker-1
eval "$(scripts/test-postgres env worker-1)"
make test_integration
scripts/test-postgres down worker-1

scripts/test-postgres run worker-2 -- make test_integration
```

Select `--profile fast-read` for disposable read attribution or
`--profile durable` for durability-sensitive write, WAL, and migration tests.
The harness records the live durability, WAL, and checkpoint settings in each
instance's mode-0600 state file. `--log-output /absolute/path` exports a
sanitized server log before teardown.

Set `DAWGS_TEST_POSTGRES_ROOT` to move the ephemeral cluster state from its
default location under `${TMPDIR:-/tmp}`. Instance names are isolated and each
cluster receives a dynamically allocated localhost port.

Use this module from another Go project:

```bash
go get github.com/specterops/dawgs
```

### PostgreSQL V2 layout and upgrades

> **Unreleased transition:** the V2 migrator and bootstrap path are currently
> restricted to identity-guarded disposable databases. Runtime activation and
> destructive legacy cleanup remain activation-interlocked until every gate in
> `gaps_plan.md` passes. Restore/rebind and cleanup are executable only in the
> identity-guarded disposable rehearsal harness.

The PostgreSQL driver operates only on the V2 edge layout: edges are first
partitioned by graph and then by relationship kind. Translated SQL targets the
stable graph parents (`public.node_<graph_id>` and
`public.edge_<graph_id>`), while PostgreSQL routes writes to
`edge_<graph_id>_<kind_id>` leaves. Edge IDs and edge property constraints are
enforced graph-wide across those leaves by database registries and triggers.

An existing V1 database is never upgraded implicitly by `AssertSchema`.
Application operations return `pg.ErrMigrationRequired`; use the offline
`dawgs-pg-migrate` artifact instead:

```bash
make build_pg_migrate
bin/dawgs-pg-migrate preflight --connection-file operator.json --manifest manifest.json --report preflight.json
```

Each PostgreSQL driver instance is closed until startup validation succeeds.
Services that own schema reconciliation call `AssertSchema` once before serving;
read-only services call `(*pg.Driver).AssertRuntime`. `Readiness` is a fresh
diagnostic and does not open the driver. Ordinary transactions then use a local
readiness gate, with a narrow identity check only once per physical backend.
DDL, ACL, trigger, restore, migration, or role changes require draining and
restarting/reasserting all service instances; see ADR 0014.

Preflight and every migration confirmation also require the direct target,
complete writer-probe set, dormant preparation role, capacity evidence, and a
recent restore-tested backup evidence file. `migrate` installs only the durable
writer fence. `verify-fence` is confirmed once for database probes and again
for credential retirement; only a separately confirmed `resume` may begin
bounded copy. See the runbook for the complete unreleased rehearsal syntax.

Fresh V2 rehearsal uses the same control and receipt tables:

```bash
bin/dawgs-pg-migrate bootstrap --connection-file operator.json --unreleased-disposable --report bootstrap.json
bin/dawgs-pg-migrate confirm --action bootstrap-handoff --connection-file operator.json \
  --migration-id OPERATION_ID --target-connection-file target.json --report handoff-confirmation.json
bin/dawgs-pg-migrate complete-bootstrap --connection-file operator.json --migration-id OPERATION_ID \
  --target-connection-file target.json --retirement-connection-file coordinator.json \
  --confirm TOKEN --unreleased-disposable --report handoff.json
```

The final bootstrap and migration acceptance transaction uses the directly
authenticated coordinator to transfer managed objects from the restricted
operator to the distinct database-owning `NOLOGIN` role. The target receives
only data privileges, database `CONNECT`/`TEMPORARY`, and the
fixed-search-path graph/leaf provisioning routines; it never receives database
or schema `CREATE`. `TEMPORARY` supports the existing invoker-security
shortest-path scratch tables. Driver `Raw` SQL rejects caller-created temporary
objects and persistent session-state changes so pooled backends cannot be
poisoned.

The runtime currently rejects all V2 activation, including accepted rehearsal
states. Before
acceptance, `rollback` restores the detached V1 graph partitions; after
acceptance, recovery is forward repair or restore followed by `rebind-restore`
and a fresh target smoke. Cleanup consumes only the immutable accepted object
map and requires current identity/smoke, configured soak, fresh backup/restore,
complete fleet/V1-retirement evidence, and a one-use confirmation. Both
destructive paths remain restricted to disposable rehearsals.
See [the migrator runbook](cmd/pgmigrate/README.md).

`make quality_backend` captures PostgreSQL and Neo4j integration results for backend equivalence comparison. It requires
`PG_CONNECTION_STRING` and `NEO4J_CONNECTION_STRING`. `make quality_bench` writes benchmark markdown and JSON captures
for later baseline comparison. Benchmark drift comparison can be performed by `make quality` through `tools/metrics` when
`BENCHMARK_REPORT` and `BENCHMARK_BASELINE` are provided.

Run the package benchmark suite with:

```bash
make test_bench
```

Use `cmd/benchdiff` to compare benchmarks between two committed refs without changing the active worktree:

```bash
go run ./cmd/benchdiff -base main -target HEAD -kind unit
```

For integration benchmark comparisons, provide two dedicated disposable databases:

```bash
export DAWGS_BENCHMARK_DISPOSABLE=1
export BENCH_BASE_CONNECTION_STRING="postgresql://dawgs:weneedbetterpasswords@localhost:65432/dawgs_base"
export BENCH_TARGET_CONNECTION_STRING="postgresql://dawgs:weneedbetterpasswords@localhost:65432/dawgs_target"
go run ./cmd/benchdiff -base main -target HEAD -kind all -driver pg -fail-regression 10%
```

Integration comparison refuses to run without the disposable marker and two
distinct database connections; the benchmark runner clears its own fixtures.
Never point either connection at a semantic-test or application database. The
harness exports committed refs without changing the active worktree and writes
mode-0600 versioned envelopes plus comparison JSON under `.bench/runs/`. It
refuses comparison when fixture, case/unit, parameter, seed, adapter, schedule,
Go/hardware, or PostgreSQL configuration/extension facts differ.

The integration benchmark runner includes committed `base`, `adcs_fanout`, and `traversal_shapes` datasets by default.
The traversal shape suite checks expected result counts for chain, fanout, bounded cycle, disconnected,
edge-kind-selective, and multi-path shortest-path scenarios before recording timings.

`make plan_corpus` captures plan diagnostics for the shared Cypher integration corpus. It accepts either
`CONNECTION_STRING` for one backend or `PG_CONNECTION_STRING` and `NEO4J_CONNECTION_STRING` for both backends, then
writes JSONL captures and markdown/JSON summaries under `.coverage/`.

`go run ./cmd/graphbench` captures runtime diagnostics for the scale corpus under `benchmark/testdata/scale`. The
current modes are `postgres_sql`, `local_traversal`, and `neo4j`; AGE is reference-design input only and is not a direct
comparison mode yet. The command can emit JSONL records plus Markdown and JSON summaries, and can compare current timings
against a previous JSONL baseline.

`go run ./cmd/retriever` dumps and loads live Dawgs graph databases as
manifest-based collections of compressed JSONL fragments. It supports
PostgreSQL and Neo4j, uncompressed, gzip, and zstd fragments, bounded keyset
scans, resumable dump checkpoints, checksum validation before load, optional
deterministic property scrubbing, and a read-throughput benchmark mode. It can
also package dumps as single HPKE/ML-KEM encrypted TAR archives.
See [cmd/retriever/README.md](cmd/retriever/README.md) for dump, encrypted
archive, load, scrubbed dump, metrics verification, and benchmark examples.
The same import/export functionality is available to library consumers from
`github.com/specterops/dawgs/retriever`; callers provide an already-open
`graph.Database`, and archive helpers support both path-based and stream-based
APIs. The package exposes CLI-matching default option constructors, structured
progress callbacks, manifest/metrics helpers, HPKE key envelope reader/writer
helpers, and typed errors for validation, compatibility, checksum, metrics, and
count mismatches.

The unreleased PostgreSQL transition tooling is documented in the
[operator migration and recovery runbook](docs/edge_kind_partitioning_runbook.md)
and [versioned schema reference](docs/edge_kind_partitioning_schemas.md).

PostgreSQL translates exact string property equality with a JSON string type guard and `properties ->>` extraction, so
indexes created on expressions such as `properties ->> 'objectid'` and `properties ->> 'name'` can be used for selective
anchors without matching JSON booleans or numbers. Simple relationship count fast paths depend on the schema's
`kind_id`-first edge index for efficient typed counts.

PostgreSQL property index regression coverage is hard-failing under the `manual_integration` tag. The synthetic plan
test translates Cypher to PgSQL, disables sequential scans for the `EXPLAIN`, and requires explicit node property
indexes to appear in the JSON plan:

For local development against a checkout, use a Go module replacement in the consuming project:

```go
replace github.com/specterops/dawgs => /path/to/dawgs
```

## Documentation

- [Development workflow](docs/development.md): build, test, integration, metrics, quality, and corpus-capture commands.
- [Cypher library](cypher/README.md): parser generation and Cypher package overview.
- [PostgreSQL translation](docs/postgresql_translation.md): PostgreSQL translator behavior, optimizer lowerings, indexing notes, and validation expectations.
- [Plan corpus capture](cmd/plancorpus/README.md): shared integration corpus plan diagnostics.
- [Graph benchmark capture](cmd/graphbench/README.md): runtime diagnostics for scale scenarios.
- [Cypher syntax support](cypher/Cypher%20Syntax%20Support.md): supported Cypher behavior and semantic notes.

## Repository Map

- `cypher/`: parser, Cypher AST, walkers, and backend translation models.
- `drivers/`: database driver implementations.
- `integration/`: backend-equivalent integration suites and fixtures.
- `cmd/`: command-line tools for capture, export, and diagnostics.
- `tools/`: developer tools such as `dawgrun` and metrics reporting.
