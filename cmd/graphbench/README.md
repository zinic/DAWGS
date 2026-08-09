# GraphBench

`graphbench` runs the scale benchmark corpus under `benchmark/testdata/scale`.
It is meant for runtime gap accounting: query duration, returned row counts,
PostgreSQL plan details, Neo4j plan operators, fallback reasons, and comparison
summaries.

The current execution modes are:

- `postgres_sql`: runs DAWGS' PostgreSQL SQL translation against a PostgreSQL database.
- `local_traversal`: records explicit `not_implemented` placeholders until the local traversal executor lands.
- `neo4j`: runs the same corpus against Neo4j through the DAWGS Neo4j backend.

Apache AGE is not an execution mode in this harness yet. AGE behavior can be
captured in corpus `reference_design` notes so DAWGS can use it as design input
without treating it as a direct benchmark comparison.

## Inputs

The command loads cases from `benchmark/testdata/scale` by default and imports
the fixture datasets from `integration/testdata`.

Connection strings can be supplied as flags or environment variables:

- PostgreSQL: `-pg-connection`, `PG_CONNECTION_STRING`, `-connection`, or `CONNECTION_STRING`.
- PostgreSQL maintenance: `-pg-maintenance-connection` or
  `PG_MAINTENANCE_CONNECTION_STRING`. This must be a separate directly
  authenticated connection to the same database whose role is superuser,
  database owner, or has `MAINTAIN` on every active leaf.
- Neo4j: `-neo4j-connection`, `NEO4J_CONNECTION_STRING`, `-connection`, or `CONNECTION_STRING`.

## Examples

Run only PostgreSQL SQL translation:

```bash
go run ./cmd/graphbench \
  -modes postgres_sql \
  -pg-connection "$PG_CONNECTION_STRING" \
  -pg-maintenance-connection "$PG_MAINTENANCE_CONNECTION_STRING" \
  -jsonl-output .coverage/graphbench-postgres.jsonl \
  -summary .coverage/graphbench-postgres.md \
  -summary-json .coverage/graphbench-postgres.json
```

Capture PostgreSQL, local traversal placeholders, and Neo4j in one report:

```bash
go run ./cmd/graphbench \
  -modes postgres_sql,local_traversal,neo4j \
  -pg-connection "$PG_CONNECTION_STRING" \
  -neo4j-connection "$NEO4J_CONNECTION_STRING" \
  -jsonl-output .coverage/graphbench.jsonl \
  -summary .coverage/graphbench.md \
  -summary-json .coverage/graphbench.json
```

Compare a run against a previous JSONL capture:

```bash
go run ./cmd/graphbench \
  -modes postgres_sql,neo4j \
  -pg-connection "$PG_CONNECTION_STRING" \
  -neo4j-connection "$NEO4J_CONNECTION_STRING" \
  -baseline .coverage/graphbench-baseline.jsonl \
  -jsonl-output .coverage/graphbench.jsonl \
  -summary .coverage/graphbench.md
```

## Outputs

JSONL output contains one `CaseResult` record per case and execution mode.
Markdown and JSON summaries aggregate mode status counts, per-case timings, row
counts, fallback reasons, and baseline regressions or improvements when a
baseline capture is supplied. Warmup and timed iterations must match both row
count and an order-insensitive, duplicate-preserving logical result digest.
PostgreSQL records also separate parse, translate, and SQL-render client time.

PostgreSQL records include translated SQL and `EXPLAIN (ANALYZE, BUFFERS,
TIMING OFF, FORMAT JSON)` metrics, planned/removed/executed logical leaf sets,
layout/topology/server facts, summed heap/index/TOAST bytes, and analyze
freshness. Before each dataset, GraphBench verifies reset registries and runs
deterministic per-leaf `VACUUM (ANALYZE)` through the maintenance connection.
The privileged maintenance connection is short-lived and is closed before any
timed case begins.
Never use an application or semantic-test database. Neo4j records include plan
operator names when an `EXPLAIN` plan can be captured.
