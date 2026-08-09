# V2 performance work 1 benchmark runbook

## Purpose and current disposition

This is the operator companion to [`v2_perf_work_1.md`](v2_perf_work_1.md).
It defines how to construct each benchmark layout, run the live suites on
temporary PostgreSQL instances, retain comparable evidence, and decide the
final performance gate.

This runbook governs performance evidence only. It does not authorize V2
activation, migration of an application database, fleet rollout, V1 cleanup,
or an in-place rollback after acceptance. Use
[`docs/edge_kind_partitioning_runbook.md`](docs/edge_kind_partitioning_runbook.md)
for transition recovery and lifecycle procedure.

> **Current disposition: final gate blocked.** The temporary PostgreSQL harness
> now has explicit recorded `fast-read` and `durable` profiles, accepted-target
> schema reconciliation and graph/leaf provisioning use the supported target
> contract, GraphBench closes its maintenance connection before timing, and
> both live runners validate logical result digests. A build-tagged disposable
> rehearsal seam keeps production activation closed. The full frozen V1/V2
> transition orchestrator, cold/warm confirmatory populations, approved
> topology/capacity inputs, PostgreSQL 16 evidence, exact promoted-artifact
> authorization, and ADR 0012 numeric approvals are still missing. Diagnostic
> checkpoints are not final-gate evidence.

Implemented diagnostic checkpoints include a single-round-trip accepted guard,
stable bounded guard observations, direct render-time graph scoping, exact
typed path-hydration kind unions, idempotent existing graph/leaf provisioning,
and read paths that do not provision leaves. The guard still recomputes the
exhaustive structural topology in that one statement. It therefore preserves
next-operation drift refusal, but it has not met WP2's topology-independent
catalog/buffer bound. No cache is authorized. Closing that gate requires an
authoritative cross-process runtime-contract token whose update coverage is
proven for every supported DDL, ACL, registry, receipt, restore, and role
mutation source.

The performance gate has four possible dispositions:

| Disposition | Meaning |
|---|---|
| `blocked` | A prerequisite, approved budget, supported harness capability, PostgreSQL major, topology point, or required approval is missing. No confirmatory population may be interpreted. |
| `invalid` | The intended run started, but its source, environment, order, fixtures, results, cleanup, or evidence envelope was incompatible or incomplete. Preserve it for diagnosis and rerun the affected matrix cell. |
| `evidence_complete_fail` | The run was valid and at least one correctness, safety, capacity, or approved numeric gate failed. Retain the failure and leave activation closed. |
| `evidence_complete_pass` | Every required matrix cell and hard gate passed for the exact candidate. This makes the performance work eligible for the separate parent activation review; it does not activate V2. |

## Layouts and estimands

Never label all new-format results simply `v2`. Record one of the following
layout IDs in every sample, plan, log, and summary:

| Layout ID | Construction | Runtime used | Question answered |
|---|---|---|---|
| `v1_frozen` | Empty temporary database initialized and populated by the frozen V1 tools at `bd64d910ef1bfe01b886fa584796275e5c533c8e` | Binary built from that exact revision | What does the old runtime on the old physical layout cost? |
| `v2_bootstrap` | Canonical empty-V2 bootstrap, target handoff with its canonical empty-install smoke fallback, accepted-state verification, graph provisioning through the supported routine, and fixture load | Exact V2 candidate | What does steady-state V2 cost without migration history or retained V1 objects? |
| `v2_migrated` | Identical frozen V1 fixture followed by the complete fenced V1-to-V2 transition, acceptance, target smoke, and fixture checksum verification | Exact V2 candidate | What does steady-state V2 cost after the real migration path, including retained pre-cleanup objects? |
| `v2_promoted` | The `v2_bootstrap` and `v2_migrated` constructions rerun with the exact artifact proposed for shipment and no rehearsal seam | Exact checksummed promotion binary | Did the artifact that would ship reproduce the accepted candidate result? |

`v2_promoted` is an artifact condition, not a fourth substitute schema. It must
be exercised through both the bootstrap and migration constructions. Results
from raw, mixed, fenced, copy-in-progress, pending, rolled-back, drifted, or
accepted-but-unsmoked states are refusal/correctness evidence and must not be
included in steady-state timing populations.

The exact promotion binary cannot run while the production activation
interlock is closed. Avoid a circular approval by using a two-step parent
review: after the rehearsal candidate passes all other performance gates, the
release authority may authorize the frozen no-seam binary for disposable
script-owned confirmation only. That narrow authorization does not permit an
application migration or production activation. The final performance gate
remains `blocked` until the exact-artifact confirmation passes, after which the
parent review resumes its independent activation decision.

For each applicable layout, retain separate topology labels for:

- one graph with 1, 10, 12, 30, 64, and the approved maximum active edge-kind
  leaf count;
- several graphs with 30 active edge-kind leaves each;
- the approved maximum combined graph/leaf topology; and
- the approved row-count, skew, and constraint-cardinality points;
- typed, statically multi-kind, and untyped operations;
- forced custom and forced generic plan modes; and
- each approved deployment/hardware class.

Do not pool topology points. A small-layout pass cannot stand in for the
approved maximum, and a global geometric mean cannot hide a failed case.

## Workload and PostgreSQL profiles

Use the same profile on both sides of every V1/V2 comparison.

| Profile | Required PostgreSQL settings | Permitted evidence |
|---|---|---|
| `fast-read` | `fsync=off`, `synchronous_commit=off`, `full_page_writes=off`, with all live values recorded | Read-path attribution, planning, pruning, fixed-cost checkpoints, and continuity with the retained baseline. It is explicitly non-durable. |
| `durable` | `fsync=on`, `synchronous_commit=on`, `full_page_writes=on`, with checkpoint/WAL settings frozen and recorded | Writes, ingest, provisioning, registry contention, WAL, disk, migration, rollback, crash, and capacity evidence. |

`scripts/test-postgres --profile fast-read|durable` records the selected
profile, exact server version, durability settings, and WAL/checkpoint settings
in the private instance state. Use `--log-output` to copy the sanitized server
log outside the ephemeral root before teardown. A durable profile makes the
server settings suitable for diagnostic durability-sensitive runs; it does not
by itself make an unapproved fixture, host, or sample population final evidence.

The required live suites are:

| Suite | Purpose | Final warm sample count |
|---|---|---:|
| `cmd/benchmark` | End-to-end direct and Cypher operations, exact logical result, phase timing, and direct-API/Cypher plans | At least 30 per case, layout, round, major, topology, and plan mode |
| GraphBench `postgres_sql` | Translation-to-SQL wall time, SQL, plan/execution metrics, buffers, and logical leaf sets | At least 30 per case, layout, round, major, topology, and plan mode |
| WP1 microbenchmarks | Guard, pool acquisition, parse/translate/scope/render, decode, scan, and commit attribution | Predeclared count sufficient for a stable benchstat-style comparison |
| WP5/WP6 lifecycle matrix | Writes, provisioning, contention, storage, WAL, migration, rollback, restore, and capacity | Workload-specific count approved in the run specification |
| Retriever live scan, when included | Read-scan throughput and bounded keyset behavior on the approved scale fixtures | Workload-specific count approved in the run specification |

Each live suite performs one untimed warmup today. The final harness must also
capture a cold-new-backend observation separately and must never mix it into
the warm population. The frozen run specification must explicitly say whether
the Retriever live-scan suite is `included` or `out_of_scope`, with the decision
owner and rationale. Silence does not mean out of scope.

## Responsibilities

Name these people or automation principals in the run manifest before a final
run:

- the decision owner, who resolves comparison-contract and budget questions;
- a separate operator approver, who approves bootstrap, migration, rollback,
  restore, cleanup, and temporary-instance handling;
- the runtime/security reviewer, who reviews readiness, target privileges,
  graph isolation, and fail-closed behavior;
- the evidence owner, who verifies hashes, redaction, retention, and
  reproducibility; and
- the benchmark operator, who runs the frozen protocol without changing it.

One person may fill the evidence-owner and benchmark-operator roles. The
decision owner and operator approver must remain distinct. Record approvals
against the canonical `evidence-manifest.json` digest. An approval of an
earlier manifest is stale. Generate the outer `SHA256SUMS` only after adding
those approval records; it protects packaging integrity and is not a circular
approval target.

## Safety rules

- Use only invocation-owned clusters from `scripts/test-postgres` and only
  randomized databases/roles from `scripts/prepare-test-pg`.
- Never point a live benchmark at an application, shared development,
  semantic-test, or manually managed database. Both live runners clear and
  reload their fixtures.
- Keep artifacts outside `DAWGS_TEST_POSTGRES_ROOT`; the cluster root is
  private disposable state. The current launcher deletes only the named
  instance directory. Its per-instance lock file and the gate-specific runtime
  root remain until the orchestrator proves no instance/process remains and
  removes that exact private root.
- Never enable shell tracing while connection strings or one-use
  confirmations are in scope. Do not put a DSN, password, credential verifier,
  or confirmation token in a log, command transcript, or retained manifest.
- Keep raw connection envelopes, writer probes, and confirmation-token reports
  in an instance-owned mode-0700 secret directory with mode-0600 files and a
  teardown trap. Retain only schema-defined sanitized facts and receipt/report
  digests. The final harness must use an in-process migrator adapter or a
  reviewed `--confirm-file`/stdin interface; the current CLI's `--confirm`
  argument exposes the token through process arguments and is not admissible
  for the final gate.
- Give every final timed V2 process only the accepted target connection. A
  separate orchestration process may use a same-database maintenance
  connection for canonical `VACUUM (ANALYZE)`, but it must close that
  connection before timing. Current GraphBench keeps such a connection while
  it runs; that is a disclosed diagnostic limitation to remove in WP0.
- Do not add an out-of-band target privilege, create a graph directly, repair
  catalogs manually, skip a fence check, or hand-edit a result to make a run
  proceed.
- A benchmark-only activation seam is acceptable only for a clearly labeled
  checkpoint build, in an immutable disposable source copy, with its patch and
  binary hashes retained. It is never final-gate evidence.
- Preserve failed and outlier samples. Diagnostic reruns are additive and may
  not replace observations in a confirmatory population.
- Accepted V2 has no in-place rollback. On an accepted-layout failure, use
  forward repair or restore/rebind according to the migration runbook.

## One-time operator setup

Run the whole procedure from the repository root in a dedicated Bash session
or versioned Bash script. The examples depend on Bash process substitution and
must fail closed; do not paste them piecemeal into a permissive interactive
shell. Set task-specific variables, but do not copy connection strings into
them.

```bash
set -euo pipefail
umask 077

perf_v1_revision=bd64d910ef1bfe01b886fa584796275e5c533c8e
perf_candidate_revision=REPLACE_WITH_EXACT_COMMIT
perf_pg16_bin=/absolute/path/to/postgresql/16/bin
perf_pg18_bin=/absolute/path/to/postgresql/18/bin
perf_gate_nonce="$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"
perf_gate_id="$(date -u +%Y%m%dT%H%M%SZ)-v2-perf-$perf_gate_nonce"
perf_run_root="$PWD/.bench/runs/$perf_gate_id"
perf_pg_root="$PWD/.bench/postgres/$perf_gate_id"
mkdir -p "$PWD/.bench/runs" "$PWD/.bench/postgres"
test ! -e "$perf_run_root"
test ! -e "$perf_pg_root"
mkdir -m 700 "$perf_run_root" "$perf_pg_root"
```

The final candidate must be a committed, immutable, clean input. Do not clean
or reset the shared worktree. Build from a detached clean checkout or a
checksummed `git archive` in a separate directory. For a checkpoint built from
a dirty worktree, archive tracked and untracked source explicitly and label the
run `diagnostic`; it cannot be promoted later by relabeling it.

Verify the frozen V1 revision and PostgreSQL binary sets:

```bash
git cat-file -e "$perf_v1_revision^{commit}"
git cat-file -e "$perf_candidate_revision^{commit}"

for perf_pg_bin in "$perf_pg16_bin" "$perf_pg18_bin"; do
  test -x "$perf_pg_bin/postgres"
  test -x "$perf_pg_bin/initdb"
  test -x "$perf_pg_bin/pg_ctl"
  test -x "$perf_pg_bin/createdb"
  test -x "$perf_pg_bin/psql"
  "$perf_pg_bin/postgres" --version
done

mkdir -m 700 "$perf_run_root/environment"
record_perf_pg_toolchain() {
  local perf_pg_major="$1"
  local perf_pg_directory="$2"
  local perf_pg_binary

  (
    cd "$perf_pg_directory"
    sha256sum postgres initdb pg_ctl createdb psql
  ) > "$perf_run_root/environment/pg${perf_pg_major}-binaries.sha256"
  for perf_pg_binary in postgres initdb pg_ctl createdb psql; do
    "$perf_pg_directory/$perf_pg_binary" --version
  done > "$perf_run_root/environment/pg${perf_pg_major}-binaries.version.txt"
}
record_perf_pg_toolchain 16 "$perf_pg16_bin"
record_perf_pg_toolchain 18 "$perf_pg18_bin"
```

`--bin-dir` must be an absolute, non-symlink directory. The temporary script
checks that all five binaries exist and that the running server major agrees
with the selected `postgres` binary. The layout runner must additionally
reject anything other than PostgreSQL 16 or 18 and record the exact
`server_version_num`.

Check host stability before every retained run. Record, rather than merely
inspect, the CPU model/count, memory, kernel, filesystem/mount, storage class,
Go version, power/governor policy, container/VM/provider class, background
load, clock source, locale, and relevant PostgreSQL settings. Stop if another
timed workload, autoupdate, or known noisy job cannot be excluded.

## Verify the retained baseline

The retained PostgreSQL 18 diagnostic baseline is
`.bench/runs/20260808T225900Z-v1-v2/`. Verify it before using it as an
engineering reference:

```bash
(
  cd .bench/runs/20260808T225900Z-v1-v2
  sha256sum -c SHA256SUMS
  jq empty manifest.json comparison.json postgres-environment.json
  test "$(jq -r .v1_revision manifest.json)" = \
    bd64d910ef1bfe01b886fa584796275e5c533c8e
  test "$(jq -r .rounds manifest.json)" = 5
  test "$(jq -r .iterations_per_round manifest.json)" = 10
)
```

That artifact used disclosed activation, runtime, target-privilege, graph
setup, and analyze-normalization shims. It has five rounds of ten observations
on PostgreSQL 18.4 and cardinality/duplicate-shape checks, not the full final
matrix or exact-result contract. It is suitable for root-cause comparison and
checkpoint deltas only. Its retained `analyze/main.go` and `manifest.go`
hardcode that run's paths, case counts, and metadata; parameterize them before
using them for another artifact. Running them unchanged can overwrite or
mislabel evidence.

### Refresh the retained shimmed checkpoint

This procedure is available only to reproduce or refresh the current
bootstrap-layout diagnostic while WP0 is incomplete. It does not construct
`v2_migrated`, does not make the V2 build shippable, and must never be used for
the final gate.

Create immutable V1 and V2 source snapshots outside the shared worktree. Build
the V2 snapshot from an explicit Git inventory; never archive `.` because that
can capture ignored `.env`, credential, or unrelated files.

```bash
perf_diag_tmp="$(mktemp -d "${TMPDIR:-/tmp}/dawgs-v2-perf.XXXXXXXX")"
perf_diag_cleanup() {
  perf_diag_status=$?
  trap - EXIT HUP INT TERM
  case "$perf_diag_tmp" in
    "${TMPDIR:-/tmp}"/dawgs-v2-perf.*)
      rm -rf -- "$perf_diag_tmp" || return 1
      ;;
    *) return 1 ;;
  esac
  perf_diag_tmp=
  return "$perf_diag_status"
}
trap perf_diag_cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

perf_diag_source="$perf_diag_tmp/source"
perf_diag_bin="$perf_run_root/bin"
mkdir -p \
  "$perf_diag_source/v1" \
  "$perf_diag_source/v2" \
  "$perf_diag_tmp/logs" \
  "$perf_diag_tmp/secrets" \
  "$perf_diag_bin" \
  "$perf_run_root/source" \
  "$perf_run_root/patches"
chmod 700 "$perf_diag_tmp/logs" "$perf_diag_tmp/secrets"

git archive --format=tar "$perf_v1_revision" \
  > "$perf_run_root/source/v1-source.tar"
tar -xf "$perf_run_root/source/v1-source.tar" \
  -C "$perf_diag_source/v1"

git status --porcelain=v1 --untracked-files=all \
  > "$perf_run_root/source-status.txt"
git ls-files -z --cached --others --exclude-standard \
  > "$perf_run_root/source/v2-candidates.nul"
git ls-files --others --exclude-standard \
  > "$perf_run_root/source/untracked-files.txt"

python3 - \
  "$perf_run_root/source/v2-candidates.nul" \
  "$perf_run_root/source/v2-files.nul" <<'PY'
from pathlib import Path
import os
import stat
import sys

candidate_path, output_path = map(Path, sys.argv[1:])
approved = []
for raw_path in candidate_path.read_bytes().split(b"\0"):
    if not raw_path:
        continue
    path = os.fsdecode(raw_path)
    if os.path.isabs(path) or path == ".." or path.startswith("../") or "/../" in path:
        raise SystemExit(f"unsafe source inventory path: {path!r}")
    if not os.path.lexists(path):
        continue
    mode = os.lstat(path).st_mode
    if stat.S_ISLNK(mode):
        target = os.readlink(path)
        normalized = os.path.normpath(os.path.join(os.path.dirname(path), target))
        if os.path.isabs(target) or normalized == ".." or normalized.startswith("../"):
            raise SystemExit(f"escaping source symlink: {path!r} -> {target!r}")
    elif not stat.S_ISREG(mode):
        raise SystemExit(f"refusing non-file source inventory entry: {path!r}")
    approved.append(raw_path)
output_path.write_bytes(b"\0".join(approved) + b"\0")
PY
```

Before continuing, the evidence owner must review `source-status.txt`, the
NUL-delimited candidate/actual inventories, and `untracked-files.txt`; run the
approved repository secret scanner over every inventoried regular file.
Deleted tracked paths remain in `source-status.txt` and the candidate inventory
but are absent from the actual tar inventory. Abort if an ignored file is
needed, a filename/content may contain a secret, an unexpected generated
artifact is present, or an untracked input has not been explicitly approved.
Record both inventory digests and approval. `--exclude-standard` prevents
ignored files from entering implicitly; it is not a substitute for reviewing
non-ignored untracked files.

```bash
tar --null --verbatim-files-from --no-recursion \
  -T "$perf_run_root/source/v2-files.nul" \
  -cf "$perf_run_root/source/v2-source.tar"
tar -xf "$perf_run_root/source/v2-source.tar" \
  -C "$perf_diag_source/v2"

cp .bench/runs/20260808T225900Z-v1-v2/patches/* \
  "$perf_run_root/patches/"
(
  cd "$perf_run_root"
  sha256sum \
    source/*.tar \
    source/v2-candidates.nul \
    source/v2-files.nul \
    source/untracked-files.txt \
    patches/* > source/source-inputs.sha256
)
```

The retained `*.patch` files are disclosure deltas with omitted hunk ranges;
ordinary `patch`/`git apply` correctly refuses them. Apply their four exact
replacements to the disposable snapshots and fail unless every old fragment
occurs exactly once:

```bash
python3 - "$perf_diag_source" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])


def replace_once(relative_path: str, old: str, new: str) -> None:
    path = root / relative_path
    content = path.read_text()
    count = content.count(old)
    if count != 1:
        raise SystemExit(
            f"refusing {relative_path}: expected one old fragment, found {count}"
        )
    path.write_text(content.replace(old, new))


replace_once(
    "v2/drivers/pg/layout.go",
    "const activationReady = false",
    "const activationReady = true",
)

replace_once(
    "v2/drivers/pg/manager.go",
    """func (s *SchemaManager) assertSchema(tx graph.Transaction, schema graph.Schema) error {
\tif err := query.On(tx).CreateSchema(); err != nil {
\t\treturn err
\t}

\tif err := s.fetch(tx); err != nil {""",
    """func (s *SchemaManager) assertSchema(tx graph.Transaction, schema graph.Schema) error {
\tif err := s.fetch(tx); err != nil {""",
)

v1_load = """\t\tidMap, err := loadDataset(ctx, s.db, s.datasetDir, datasetName)
\t\tif err != nil {
\t\t\treturn nil, err
\t\t}
"""
replace_once(
    "v1/cmd/graphbench/postgres.go",
    v1_load,
    v1_load
    + """\t\tif err := s.pgDriver.OptimizeStorage(ctx); err != nil {
\t\t\treturn nil, fmt.Errorf("prepare PostgreSQL physical tables for %s: %w", datasetName, err)
\t\t}
""",
)

v2_vacuum = """func (s *postgresSQLRunner) vacuumAnalyzeAndMeasure(ctx context.Context) error {
\trows, err := s.maintenance.Query(ctx, `select graph_id,kind_id from public.dawgs_edge_kind_leaf"""
replace_once(
    "v2/cmd/graphbench/postgres.go",
    v2_vacuum,
    """func (s *postgresSQLRunner) vacuumAnalyzeAndMeasure(ctx context.Context) error {
\tnodeRelation := pgx.Identifier{"public", fmt.Sprintf("node_%d", s.graphID)}.Sanitize()
\tif _, err := s.maintenance.Exec(ctx, "VACUUM (ANALYZE) "+nodeRelation); err != nil {
\t\treturn err
\t}
\trows, err := s.maintenance.Query(ctx, `select graph_id,kind_id from public.dawgs_edge_kind_leaf""",
)
PY
```

The exact-replacement program is part of this runbook's hashed procedure. Stop
if a fragment is absent or duplicated. A changed shim must be reviewed,
renamed, hashed, and disclosed as a new diagnostic input; never loosen the
count check or use a fuzzy replacement.

Verify the benchmark inputs before building:

```bash
(
  cd "$perf_diag_source/v1"
  find benchmark/testdata/scale integration/testdata -type f -print0 |
    LC_ALL=C sort -z |
    xargs -0 sha256sum
) > "$perf_run_root/source/v1-fixtures.sha256"

(
  cd "$perf_diag_source/v2"
  find benchmark/testdata/scale integration/testdata -type f -print0 |
    LC_ALL=C sort -z |
    xargs -0 sha256sum
) > "$perf_run_root/source/v2-fixtures.sha256"

cmp "$perf_run_root/source/v1-fixtures.sha256" \
  "$perf_run_root/source/v2-fixtures.sha256"
```

Build the two suite binaries and the retained V2 bootstrap helper:

```bash
for perf_layout in v1 v2; do
  (
    cd "$perf_diag_source/$perf_layout"
    CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -o "$perf_diag_bin/$perf_layout-benchmark" ./cmd/benchmark
    CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -o "$perf_diag_bin/$perf_layout-graphbench" ./cmd/graphbench
  )
done

(
  cd "$perf_diag_source/v2"
  CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -o "$perf_diag_bin/v2-bootstrap" \
    "$perf_run_root/patches/v2-bootstrap-helper.go"
)

(
  cd "$perf_run_root"
  sha256sum bin/* > source/binaries.sha256
)
for perf_binary in "$perf_diag_bin/"*; do
  go version -m "$perf_binary"
done > "$perf_run_root/source/build-metadata.txt"
```

Run the `v1_frozen` suite commands below inside the nested temporary role
harness using the operator `CONNECTION_STRING`. For `v2_bootstrap`, run
`$perf_diag_bin/v2-bootstrap` first inside the same child, then run the V2
suite against `PG_TEST_TARGET_CONNECTION_STRING`. The helper performs the
disclosed accepted-state, target `TEMPORARY`, and graph-provisioning steps and
prints a non-secret summary; retain that summary with the cell. Give V2
GraphBench `PG_TEST_ADMIN_CONNECTION_STRING` as its separate maintenance
connection.

Run each suite/layout/round in its own `scripts/test-postgres run` invocation,
using the nesting in **Temporary PostgreSQL lifecycle**. Use the five-round
checkpoint schedule and ten iterations. Never compare a newly captured V2
side only to the old retained V1 numbers: rerun both sides because the host,
kernel, Go toolchain, and PostgreSQL build affect these sub-millisecond
measurements.

The following diagnostic helper is a complete example. Set
`perf_pg_bin`/`perf_expected_major` to either the PostgreSQL 16 or 18 pair. It
uses the role harness for both layouts and asserts the current non-durable
profile. The current harness has no supported frozen-V1 comparator-writer
grant step, so this historical diagnostic times V1 through its setup operator.
Record that role asymmetry as a blocker; final evidence must use the distinct
comparator writer described under **Construct `v1_frozen`**. Use a separate
diagnostic gate ID/run root for each major; the validation below assumes one
major per diagnostic artifact.

```bash
perf_pg_bin="$perf_pg18_bin"
perf_expected_major=18
perf_iterations=10

run_perf_diagnostic_cell() {
  local perf_cell_layout="$1"
  local perf_cell_suite="$2"
  local perf_cell_round="$3"
  local perf_cell_nonce
  local perf_cell_instance
  local perf_cell_dir
  local perf_cell_private_log
  local perf_cell_secret_file
  local perf_cell_status=0
  local perf_cell_residue=0

  case "$perf_expected_major" in
    16|18) ;;
    *) return 2 ;;
  esac
  perf_cell_nonce="$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"
  perf_cell_instance="${perf_gate_id}-pg${perf_expected_major}-${perf_cell_layout}-${perf_cell_suite}-r${perf_cell_round}-${perf_cell_nonce}"
  perf_cell_dir="$perf_run_root/raw/pg${perf_expected_major}/${perf_cell_layout}/${perf_cell_suite}/r${perf_cell_round}/attempt-${perf_cell_nonce}"
  perf_cell_private_log="$perf_diag_tmp/logs/$perf_cell_instance.raw.log"
  perf_cell_secret_file="$perf_diag_tmp/secrets/$perf_cell_instance.values"
  mkdir -p "${perf_cell_dir%/*}"
  test ! -e "$perf_cell_dir"
  test ! -e "$perf_pg_root/$perf_cell_instance"
  mkdir -m 700 "$perf_cell_dir"

  DAWGS_TEST_POSTGRES_ROOT="$perf_pg_root" \
  scripts/test-postgres --bin-dir "$perf_pg_bin" \
    run "$perf_cell_instance" -- \
    scripts/prepare-test-pg -- \
    env \
      PERF_CELL_LAYOUT="$perf_cell_layout" \
      PERF_CELL_SUITE="$perf_cell_suite" \
      PERF_CELL_DIR="$perf_cell_dir" \
      PERF_EXPECTED_MAJOR="$perf_expected_major" \
      PERF_ITERATIONS="$perf_iterations" \
      PERF_V1_SOURCE="$perf_diag_source/v1" \
      PERF_V2_SOURCE="$perf_diag_source/v2" \
      PERF_BIN_DIR="$perf_diag_bin" \
      PERF_SECRET_FILE="$perf_cell_secret_file" \
    bash -ceu '
      umask 077
      printf "%s\n" \
        "${CONNECTION_STRING:-}" \
        "${PG_TEST_ADMIN_CONNECTION_STRING:-}" \
        "${PG_TEST_TARGET_CONNECTION_STRING:-}" \
        "${PG_TEST_OLD_WRITER_CONNECTION_STRING:-}" \
        "${PG_TEST_PREPARATION_CONNECTION_STRING:-}" \
        "${PG_TEST_PREPARATION_PASSWORD:-}" \
        > "$PERF_SECRET_FILE"
      chmod 600 "$PERF_SECRET_FILE"

      perf_server_version_num="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show server_version_num")"
      perf_server_version="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show server_version")"
      perf_fsync="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show fsync")"
      perf_synchronous_commit="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show synchronous_commit")"
      perf_full_page_writes="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show full_page_writes")"
      test "$((perf_server_version_num / 10000))" -eq "$PERF_EXPECTED_MAJOR"
      test "$perf_fsync" = off
      test "$perf_synchronous_commit" = off
      test "$perf_full_page_writes" = off

      perf_shared_buffers="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show shared_buffers")"
      perf_work_mem="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show work_mem")"
      perf_effective_cache_size="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show effective_cache_size")"
      perf_max_connections="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show max_connections")"
      perf_max_locks="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show max_locks_per_transaction")"
      perf_block_size="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show block_size")"
      perf_server_encoding="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "show server_encoding")"
      perf_collation="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "select datcollate from pg_catalog.pg_database where datname=pg_catalog.current_database()")"
      perf_system_identifier="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "select (pg_catalog.pg_control_system()).system_identifier::text")"
      perf_database_oid="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "select oid from pg_catalog.pg_database where datname=pg_catalog.current_database()")"

      perf_exec_scrubbed() {
        local perf_app_name="$1"
        local perf_app_value="$2"
        local perf_maintenance_name="$3"
        local perf_maintenance_value="$4"
        local perf_saved_path="$PATH"
        local perf_saved_tmp="${TMPDIR:-/tmp}"
        local perf_env_name
        shift 4

        for perf_env_name in $(compgen -e); do
          unset "$perf_env_name"
        done
        export PATH="$perf_saved_path"
        export TMPDIR="$perf_saved_tmp"
        printf -v "$perf_app_name" %s "$perf_app_value"
        export "$perf_app_name"
        if test -n "$perf_maintenance_name"; then
          printf -v "$perf_maintenance_name" %s "$perf_maintenance_value"
          export "$perf_maintenance_name"
        fi
        exec "$@"
      }

      case "$PERF_CELL_LAYOUT" in
        v1_frozen)
          perf_source="$PERF_V1_SOURCE"
          perf_application_connection="$CONNECTION_STRING"
          ;;
        v2_bootstrap)
          perf_source="$PERF_V2_SOURCE"
          "$PERF_BIN_DIR/v2-bootstrap" > "$PERF_CELL_DIR/bootstrap.json"
          jq -e --arg target_role "$PG_TEST_TARGET_ROLE" \
            ".state == \"v2_accepted\" and
             .target_role == \$target_role and
             ((.migration_id // \"\") | length > 0) and
             ((.blockers // []) | index(\"activation_not_ready\") != null)" \
            "$PERF_CELL_DIR/bootstrap.json" >/dev/null
          perf_application_connection="$PG_TEST_TARGET_CONNECTION_STRING"
          ;;
        *)
          exit 2
          ;;
      esac

      cd "$perf_source"
      case "$PERF_CELL_LAYOUT:$PERF_CELL_SUITE" in
        v1_frozen:cmd)
          (
            perf_exec_scrubbed \
              CONNECTION_STRING "$perf_application_connection" "" "" \
              "$PERF_BIN_DIR/v1-benchmark" \
              -driver pg \
              -iterations "$PERF_ITERATIONS" \
              -dataset-dir "$perf_source/integration/testdata" \
              -explain \
              -format benchfmt \
              -output "$PERF_CELL_DIR/cmd.bench" \
              -json-output "$PERF_CELL_DIR/cmd.json"
          )
          ;;
        v2_bootstrap:cmd)
          (
            perf_exec_scrubbed \
              CONNECTION_STRING "$perf_application_connection" "" "" \
              "$PERF_BIN_DIR/v2-benchmark" \
              -driver pg \
              -iterations "$PERF_ITERATIONS" \
              -dataset-dir "$perf_source/integration/testdata" \
              -explain \
              -format benchfmt \
              -output "$PERF_CELL_DIR/cmd.bench" \
              -json-output "$PERF_CELL_DIR/cmd.json"
          )
          ;;
        v1_frozen:graphbench)
          (
            perf_exec_scrubbed \
              PG_CONNECTION_STRING "$perf_application_connection" "" "" \
              "$PERF_BIN_DIR/v1-graphbench" \
              -modes postgres_sql \
              -iterations "$PERF_ITERATIONS" \
              -dataset-dir "$perf_source/integration/testdata" \
              -corpus-root "$perf_source/benchmark/testdata/scale" \
              -jsonl-output "$PERF_CELL_DIR/graphbench.jsonl" \
              -summary "$PERF_CELL_DIR/graphbench.md" \
              -summary-json "$PERF_CELL_DIR/graphbench.json"
          )
          ;;
        v2_bootstrap:graphbench)
          (
            perf_exec_scrubbed \
              PG_CONNECTION_STRING "$perf_application_connection" \
              PG_MAINTENANCE_CONNECTION_STRING "$PG_TEST_ADMIN_CONNECTION_STRING" \
              "$PERF_BIN_DIR/v2-graphbench" \
              -modes postgres_sql \
              -iterations "$PERF_ITERATIONS" \
              -dataset-dir "$perf_source/integration/testdata" \
              -corpus-root "$perf_source/benchmark/testdata/scale" \
              -jsonl-output "$PERF_CELL_DIR/graphbench.jsonl" \
              -summary "$PERF_CELL_DIR/graphbench.md" \
              -summary-json "$PERF_CELL_DIR/graphbench.json"
          )
          ;;
        *)
          exit 2
          ;;
      esac

      perf_extensions="$(psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "select coalesce(pg_catalog.json_agg(extname||chr(61)||extversion order by extname)::text,chr(91)||chr(93)) from pg_catalog.pg_extension")"
      jq -n \
        --arg server_version "$perf_server_version" \
        --argjson server_version_num "$perf_server_version_num" \
        --arg profile fast-read \
        --arg capture_phase post_layout_post_timing \
        --arg fsync "$perf_fsync" \
        --arg synchronous_commit "$perf_synchronous_commit" \
        --arg full_page_writes "$perf_full_page_writes" \
        --arg shared_buffers "$perf_shared_buffers" \
        --arg work_mem "$perf_work_mem" \
        --arg effective_cache_size "$perf_effective_cache_size" \
        --arg max_connections "$perf_max_connections" \
        --arg max_locks_per_transaction "$perf_max_locks" \
        --arg block_size "$perf_block_size" \
        --arg server_encoding "$perf_server_encoding" \
        --arg collation "$perf_collation" \
        --arg system_identifier "$perf_system_identifier" \
        --arg database_oid "$perf_database_oid" \
        --argjson extensions "$perf_extensions" \
        "{server_version: \$server_version,
          server_version_num: \$server_version_num,
          profile: \$profile,
          capture_phase: \$capture_phase,
          fsync: \$fsync,
          synchronous_commit: \$synchronous_commit,
          full_page_writes: \$full_page_writes,
          shared_buffers: \$shared_buffers,
          work_mem: \$work_mem,
          effective_cache_size: \$effective_cache_size,
          max_connections: \$max_connections,
          max_locks_per_transaction: \$max_locks_per_transaction,
          block_size: \$block_size,
          server_encoding: \$server_encoding,
          collation: \$collation,
          system_identifier: \$system_identifier,
          database_oid: \$database_oid,
          extensions: \$extensions}" \
        > "$PERF_CELL_DIR/postgres-environment.json"
    ' > "$perf_cell_private_log" 2>&1 || perf_cell_status=$?

  if test -e "$perf_pg_root/$perf_cell_instance"; then
    printf '%s\n' 'automatic PostgreSQL teardown left instance residue' \
      >> "$perf_cell_private_log"
    DAWGS_TEST_POSTGRES_ROOT="$perf_pg_root" \
      scripts/test-postgres --bin-dir "$perf_pg_bin" \
      down "$perf_cell_instance" >> "$perf_cell_private_log" 2>&1 || true
    perf_cell_residue=1
  fi

  python3 - \
    "$perf_cell_private_log" \
    "$perf_cell_secret_file" \
    "$perf_cell_dir/run.log.sanitized" <<'PY'
from pathlib import Path
import os
import re
import sys
import urllib.parse

raw_path, secret_path, output_path = map(Path, sys.argv[1:])
content = raw_path.read_text(errors="replace") if raw_path.exists() else ""
secrets = []
if secret_path.exists():
    secrets = [value for value in secret_path.read_text().splitlines() if value]
derived = []
for secret in secrets:
    parsed = urllib.parse.urlsplit(secret)
    if parsed.scheme in ("postgres", "postgresql"):
        if parsed.password:
            derived.append(urllib.parse.unquote(parsed.password))
secrets.extend(value for value in derived if value)
for secret in sorted(secrets, key=len, reverse=True):
    content = content.replace(secret, "[REDACTED]")
content = re.sub(r"postgres(?:ql)?://[^\s\"']+", "[REDACTED-POSTGRES-URL]", content)
if any(secret in content for secret in secrets):
    raise SystemExit("literal secret remained after log redaction")
if re.search(r"postgres(?:ql)?://", content):
    raise SystemExit("PostgreSQL URL remained after log redaction")
temporary = output_path.with_suffix(output_path.suffix + ".tmp")
temporary.write_text(content)
os.chmod(temporary, 0o600)
os.replace(temporary, output_path)
PY
  rm -f -- "$perf_cell_private_log" "$perf_cell_secret_file"

  if test "$perf_cell_residue" -ne 0; then
    return 1
  fi
  return "$perf_cell_status"
}
```

Run the layouts in counterbalanced order. Every call below creates and tears
down a fresh cluster and randomized role set:

```bash
for perf_round in 1 2 3 4 5; do
  if test "$((perf_round % 2))" -eq 1; then
    run_perf_diagnostic_cell v1_frozen cmd "$perf_round"
    run_perf_diagnostic_cell v1_frozen graphbench "$perf_round"
    run_perf_diagnostic_cell v2_bootstrap cmd "$perf_round"
    run_perf_diagnostic_cell v2_bootstrap graphbench "$perf_round"
  else
    run_perf_diagnostic_cell v2_bootstrap cmd "$perf_round"
    run_perf_diagnostic_cell v2_bootstrap graphbench "$perf_round"
    run_perf_diagnostic_cell v1_frozen cmd "$perf_round"
    run_perf_diagnostic_cell v1_frozen graphbench "$perf_round"
  fi
done
```

The helper intentionally does not accept a baseline or fail threshold. First
validate every capture, then compare the two newly captured populations with a
versioned analyzer. Its source archive, patches, binaries, role symmetry, and
environment differ from the retained artifact and must be reported as a new
diagnostic run. In particular, `v2-bootstrap` deliberately grants target
`TEMPORARY` out of band and reports `activation_not_ready`; both violate the
final role/release contract and automatically make this output inadmissible to
the final gate. The helper asserts server/profile settings before layout work
and emits the extension-version inventory after the suite because today's
runners do not expose a pre-timing setup boundary; the file is labeled
`post_layout_post_timing`. Final evidence requires matching pre- and post-timing
environment captures. The current launcher also deletes the PostgreSQL server
log before an outer wrapper can publish it. These are explicit diagnostic
limits, not evidence omissions to waive.

The helper stages raw output and literal secret values only beneath
`perf_diag_tmp`, replaces exact secrets and PostgreSQL URLs, and atomically
publishes only `run.log.sanitized`. Run the approved repository secret scanner
over that log and every JSON/JSONL output before checksumming. Any match makes
the cell invalid and the private raw material must be destroyed, not retained.

When the diagnostic finishes, keep the source archives and build metadata but
remove the disposable extraction directory only after confirming it is the
exact `mktemp` path and contains no needed output:

```bash
perf_diag_cleanup
```

## Harness readiness check

Run this before any purported final comparison:

```bash
make -n benchmark_transition
scripts/test-postgres --help
sed -n '1,80p' docs/adr/0012-operational-budgets.md
```

Stop with disposition `blocked` if `benchmark_transition` expands only to
`make test_bench`, if the PostgreSQL launcher has no explicit `fast-read` and
`durable` profiles, or if ADR 0012 still says `Status: proposed` or lacks
approved numeric values.

The real transition harness required by WP0 must demonstrate all of these
capabilities in its smoke output and versioned manifest before an operator uses
it for confirmation:

- frozen-V1, canonical-bootstrap-V2, and migrated-V2 layout adapters;
- invocation-owned setup and teardown through the two repository scripts;
- independent database/role sets per layout, suite, and round;
- explicit PostgreSQL major and durability profile selection;
- immutable source, binary, adapter, fixture, case, analyze-protocol, and
  statistics-method hashes;
- a versioned allowed-difference map for the intentionally different V1 and V2
  adapters;
- identical logical fixture input and pre/post logical checksums;
- exact values, paths, order/multiplicity, endpoints, and graph-isolation
  validation;
- separate cold and warm samples, fixed warmup, phase timings, SQL, plans,
  buffers, and leaf sets;
- topology, custom/generic plan, concurrency, timeout, seed, and schedule
  labels on every observation;
- setup/analyze orchestration that closes privileged connections before a
  target-only V2 timed process starts;
- token-safe migrator invocation and secret-envelope cleanup with no DSN or
  confirmation in process arguments or retained output;
- counterbalanced non-overlapping execution;
- durable write/migration/capacity capture;
- shim inventory and a positive proof that promotion binaries contain no
  rehearsal seam; and
- automatic redaction, checksum generation, residue detection, and nonzero
  exit on any hard gate;
- a pre-teardown export hook for sanitized PostgreSQL server logs; and
- launcher cleanup that propagates `down` failure instead of the current
  `cleanup_run_instance` behavior that suppresses it.

Once it exists in both compared refs, `cmd/benchdiff` can be useful for
like-for-like committed-ref package benchmarks, but it is not the transition
harness. It is currently untracked and absent from frozen V1, so it cannot run
this comparison. It also requires identical adapters, uses a sequential
base-then-target schedule, accepts only two externally provided databases, and
does not run GraphBench. Do not use its `-fail-regression` result as the final
V1/V2 layout decision.

## Temporary PostgreSQL lifecycle

The canonical nesting is:

```bash
DAWGS_TEST_POSTGRES_ROOT="$perf_pg_root" \
scripts/test-postgres --bin-dir "$perf_pg_bin" run "$perf_instance" -- \
  scripts/prepare-test-pg -- \
  "$perf_layout_runner" \
    --expected-major "$perf_expected_major" \
    --layout "$perf_layout" \
    --profile "$perf_profile" \
    --round "$perf_round" \
    --artifact-dir "$perf_cell_dir"
```

`perf_layout_runner` denotes the WP0 layout adapter invoked by the supported
`benchmark_transition` orchestrator; it is not present yet. Do not replace it
with an ad hoc final-gate script. Its contract is specified here so that every
layout uses the same lifecycle:

1. `test-postgres run` creates and owns one cluster, sets the disposable marker
   and admin `CONNECTION_STRING`, and removes the cluster on success, child
   failure, HUP, INT, or TERM.
2. `prepare-test-pg` creates one randomized database and distinct `NOLOGIN`
   owner, restricted operator, target, old-writer, and preparation roles.
3. The layout adapter asserts the exact server major/profile, constructs only
   its named layout, verifies it, runs one suite, and writes artifacts outside
   the temporary root.
4. `prepare-test-pg` terminates remaining sessions and removes exactly its
   database and roles after checking the cluster system identifier.
5. Before teardown, the launcher copies and sanitizes the PostgreSQL server log
   into the cell artifact. `test-postgres` then verifies its invocation nonce
   and removes exactly its named instance directory.
6. The orchestrator fails if either cleanup layer reports an error or leaves an
   instance directory, database, or randomized role. Current closed lock files
   are expected metadata, not a live instance, but WP0 should remove them after
   releasing the lock.
7. After every cell in the gate finishes, verify that the gate-specific runtime
   root contains no instance directory or live process, remove only its known
   closed lock files, then remove that exact private root.

Use a unique instance name containing the gate ID, major, layout, suite, round,
and a random nonce. Never reuse a pre-existing named instance: `run` preserves
an instance it did not create rather than assuming ownership.

The current script suppresses a failure from `down_instance` in its EXIT trap
and leaves `.<instance>.lock`. A diagnostic wrapper must check the instance
directory after every call, as the example above does. Final evidence requires
the WP0 launcher fix and pre-teardown PostgreSQL-log export; an outer wrapper
cannot recover `postgres.log` after the instance directory has been deleted.

The final profile interface should be equivalent to:

```bash
scripts/test-postgres --bin-dir "$perf_pg_bin" \
  --profile fast-read run "$perf_instance" -- COMMAND

scripts/test-postgres --bin-dir "$perf_pg_bin" \
  --profile durable run "$perf_instance" -- COMMAND
```

This selector is a WP0 requirement, not a currently supported flag. Until the
script's `--help` lists it and the child records/asserts the live settings, only
the current fast diagnostic workflow is available.

### Credential-scrubbed timed launch

Connection flags and `/usr/bin/env NAME=value` operands expose a DSN in process
arguments. The layout adapter must instead use an in-process launcher or a
Bash built-in scrub-and-exec boundary. This reference function receives secrets
only as shell-function values, removes every inherited exported variable, and
then `exec`s the timed process with an explicit allowlist:

```bash
perf_exec_scrubbed() {
  local perf_app_name="$1"
  local perf_app_value="$2"
  local perf_maintenance_name="$3"
  local perf_maintenance_value="$4"
  local perf_saved_path="$PATH"
  local perf_saved_tmp="${TMPDIR:-/tmp}"
  local perf_env_name
  shift 4

  for perf_env_name in $(compgen -e); do
    unset "$perf_env_name"
  done
  export PATH="$perf_saved_path"
  export TMPDIR="$perf_saved_tmp"
  printf -v "$perf_app_name" %s "$perf_app_value"
  export "$perf_app_name"
  if test -n "$perf_maintenance_name"; then
    printf -v "$perf_maintenance_name" %s "$perf_maintenance_value"
    export "$perf_maintenance_name"
  fi
  exec "$@"
}
```

Invoke it in a subshell so `exec` cannot replace the orchestrator. Never export
the function or enable xtrace. The final V2 form has no maintenance value;
today's diagnostic GraphBench form explicitly allowlists both target and
maintenance DSNs.

## Construct `v1_frozen`

Build the V1 benchmark and GraphBench binaries from the frozen source archive,
not from the candidate worktree. Retain the archive hash, binary hashes, and
`go version -m` output. Run the binaries with their frozen fixtures and V1
adapter.

Inside the nested temporary harness, the V1 adapter must:

1. Assert PostgreSQL 16 or 18 and the requested profile.
2. Use the restricted operator `CONNECTION_STRING` created by
   `prepare-test-pg` to install/assert the V1 schema and load the deterministic
   fixture; do not give V1 a superuser-only advantage.
3. Verify the unmarked V1 roots, row counts, graph isolation, and logical
   checksum, then run the common analyze protocol against the V1 physical
   relations.
4. Establish the approved frozen-V1 application privileges for the distinct
   comparator writer represented by `PG_TEST_OLD_WRITER_CONNECTION_STRING`,
   close the operator resources, and use that directly authenticated writer
   for all cold/warm timed work. The separately randomized writer on the
   migration clone is the migration writer; the two credentials must not be
   interchangeable.
5. Take the cold observation, warm up, run the timed
   suite, and take the postflight exact-result/checksum observation.

Today `prepare-test-pg` grants the old-writer role only database `CONNECT`, so
it cannot yet satisfy step 4 or provide a real migration writer probe. WP0 must
add a versioned frozen-V1 setup adapter and tests that install the exact normal
V1 application grant matrix independently on the comparator and migration
clone, split schema/fixture setup from timed execution, revoke setup authority,
close the operator, and prove both writer credentials can perform the expected
V1 operation while neither can perform operator/owner actions. Do not repair
this with an undocumented broad grant in the runbook.

The V1 command suite invocation is:

```bash
(
  perf_exec_scrubbed \
    CONNECTION_STRING "$perf_v1_application_connection" "" "" \
    "$perf_v1_benchmark" \
    -driver pg \
    -iterations "$perf_iterations" \
    -explain \
    -dataset-dir "$perf_v1_source/integration/testdata" \
    -format benchfmt \
    -output "$perf_cell_dir/cmd.bench" \
    -json-output "$perf_cell_dir/cmd.json"
)
```

The frozen V1 GraphBench invocation is:

```bash
(
  perf_exec_scrubbed \
    PG_CONNECTION_STRING "$perf_v1_application_connection" "" "" \
    "$perf_v1_graphbench" \
    -modes postgres_sql \
    -iterations "$perf_iterations" \
    -dataset-dir "$perf_v1_source/integration/testdata" \
    -corpus-root "$perf_v1_source/benchmark/testdata/scale" \
    -jsonl-output "$perf_cell_dir/graphbench.jsonl" \
    -summary "$perf_cell_dir/graphbench.md" \
    -summary-json "$perf_cell_dir/graphbench.json"
)
```

The frozen GraphBench does not have the candidate's maintenance-connection
flag or native V2 analyze metadata. The versioned V1 adapter must perform the
same analyze preparation separately and record that allowed interface
difference. Do not patch the frozen source silently.

## Construct `v2_bootstrap`

Within the nested role harness, the V2 adapter owns mode-0600 operator, target,
and coordinator connection envelopes and the complete bootstrap evidence. It
must not print those files or their contents.

The canonical construction is:

```text
bootstrap
-> confirm bootstrap-handoff
-> complete-bootstrap with target, directly authenticated coordinator, and the
   canonical empty-install smoke fallback
-> fresh migrator status
-> supported graph provisioning
-> fresh directly authenticated target `pg.Driver.Readiness`
-> one exact real application operation on a new target backend
-> fixture load and exact logical checksum
```

Require the handoff report to name the randomized target role, the final state
to be `v2_accepted`, the operation ID to remain unchanged, and migrator status
to classify the same database incarnation and accepted scope. Migrator
`health` currently aliases `status`; it is not runtime readiness. Require the
smoke status to be the canonical empty-bootstrap fallback, provision the graph
through the accepted target's supported routine, then call
`pg.Driver.Readiness` through a fresh directly authenticated target and verify
its role, database incarnation, identity epoch, layout, and topology. Run one
real exact application operation on that fresh backend before timing. The
ordinary benchmark process starts only after operator resources are closed. It
receives only `PG_TEST_TARGET_CONNECTION_STRING`.

At the time of writing, an otherwise canonical bootstrap still cannot supply
final evidence because production activation is interlocked, accepted-target
`AssertSchema` replays owner-only installation DDL, shortest paths depend on an
unapproved target `TEMPORARY` grant, empty bootstrap needs supported graph
provisioning, and analyze normalization is not symmetric in the frozen runner.
Use a checksummed rehearsal build only for a labeled checkpoint; the final run
must contain none of those patches.

## Construct `v2_migrated`

Start from the same frozen V1 logical fixture and checksum used for
`v1_frozen`. Use distinct credentials and a distinct temporary database so
fencing the migration clone cannot affect the comparator.

Follow the complete sequence from the migration runbook:

```text
preflight
-> confirm migrate
-> migrate and install the fence
-> confirm verify-fence
-> verify-fence with negative database probes
-> externally inventory, retire, and probe all writers
-> confirm verify-fence again
-> verify the authoritative retirement receipt
-> independently review the authoritative fenced report and all deltas;
   renew any confirmation invalidated by a non-allowlisted change
-> confirm resume
-> resume bounded copy to pending
-> validate exhaustively
-> confirm accept with the validation digest
-> accept with target/coordinator handoff
-> confirm smoke
-> direct target smoke
-> fresh status and health
-> exact logical checksum
```

Retain every non-secret manifest, preflight, capacity, backup, retirement,
receipt, validation, acceptance, smoke, status, and health report. For raw
writer probes and confirmation reports, retain only their schema-defined
sanitized facts and digests; destroy the mode-0600 secret originals with the
instance. Never expose a confirmation in process arguments or reuse a token.
The timed application starts only after acceptance and smoke, with the
operator and coordinator connections closed.

`v2_migrated` steady-state timing occurs before cleanup so that it measures the
supported accepted migration state with retained V1 objects. Cleanup cost is a
separate durable lifecycle metric after the required soak/backup/restore and
fleet evidence; it is not a way to rewrite the steady-state population.

## Run the V2 live suites

Once either accepted V2 construction has passed preflight, run the exact
candidate binaries against the target connection. The command below reflects
today's diagnostic GraphBench interface, which receives a separate directly
authenticated same-database maintenance connection for authorization checks,
reset verification, leaf inventory, `VACUUM (ANALYZE)`, analyze freshness, and
storage measurement:

```bash
(
  perf_exec_scrubbed \
    CONNECTION_STRING "$PG_TEST_TARGET_CONNECTION_STRING" "" "" \
    "$perf_v2_benchmark" \
    -driver pg \
    -iterations "$perf_iterations" \
    -explain \
    -dataset-dir "$perf_v2_source/integration/testdata" \
    -format benchfmt \
    -output "$perf_cell_dir/cmd.bench" \
    -json-output "$perf_cell_dir/cmd.json"
)

(
  perf_exec_scrubbed \
    PG_CONNECTION_STRING "$PG_TEST_TARGET_CONNECTION_STRING" \
    PG_MAINTENANCE_CONNECTION_STRING "$PG_TEST_ADMIN_CONNECTION_STRING" \
    "$perf_v2_graphbench" \
    -modes postgres_sql \
    -iterations "$perf_iterations" \
    -dataset-dir "$perf_v2_source/integration/testdata" \
    -corpus-root "$perf_v2_source/benchmark/testdata/scale" \
    -jsonl-output "$perf_cell_dir/graphbench.jsonl" \
    -summary "$perf_cell_dir/graphbench.md" \
    -summary-json "$perf_cell_dir/graphbench.json"
)
```

These are suite invocations, not layout provisioning. Running them against an
empty database, the wrong role, or a rehearsal-patched accepted state does not
create final evidence. The supported transition harness must add exact-result
pre/postflight, phase timing, cold observation, forced plan mode, and topology
labels around the current runner flags. For the final target-only timing
contract, move load/analyze/storage preparation into orchestration, close its
maintenance connection, and start a timed GraphBench process that receives
only the target DSN. Until that refactor exists, GraphBench output is
diagnostic.

## Analyze and preparation protocol

Apply an identical versioned protocol immediately after each deterministic
load and before any cold/warm observation:

- verify the same logical fixture checksum;
- checkpoint only when the declared profile/protocol requires it;
- run `VACUUM (ANALYZE)` on the V1 roots or the V2 graph node partition and
  every active edge leaf, as appropriate;
- record relation names/OIDs in a sanitized physical manifest, row estimates,
  last-analyze facts, statistics targets, and the analyze completion time;
- assert fixed planner GUCs, pool size, session count, query mode, timeout,
  work memory, cache settings, and parallelism;
- force custom and generic plans in separate labeled cells; and
- close the maintenance connection before timed application work and prove the
  timed process has only the layout's application credential. If preparation
  is required between dataset loads, end the timed process, prepare through a
  separate process, close it, and start a fresh timed target process.

If analyze fails or any active leaf is omitted, mark the cell `invalid`. Do not
continue with whatever statistics happen to be present.

## Checkpoint protocol

Use checkpoint runs after each WP tranche for engineering feedback. They do not
need the entire final capacity matrix, but they must remain reproducible:

1. Freeze and hash the V1 revision, candidate source, binaries, fixtures,
   adapters, cases, analyze protocol, and environment.
2. Select PostgreSQL 18 `fast-read` unless the changed tranche affects a
   version-sensitive, write, migration, storage, or durability path.
3. Run at least five fresh-cluster rounds with ten warm observations per case,
   plus separately labeled cold observations when the harness supports them.
4. Alternate order by round and never overlap V1/V2 timed work:
   `V1,V2`; `V2,V1`; `V1,V2`; `V2,V1`; `V1,V2`.
5. For bootstrap-sensitive work, compare `v1_frozen` with `v2_bootstrap`. For
   migration/catalog/retained-object work, also run `v2_migrated`.
6. Validate exact logical results and pre/post database checksums before
   interpreting latency.
7. Compare against both the retained original V2 baseline and the immediately
   previous accepted tranche.
8. Label all shimmed, single-major, reduced-topology, non-durable, or
   cardinality-only output `diagnostic`.

An isolated diagnostic rerun may explain a failure or outlier, but it does not
replace that observation in the checkpoint population.

## Per-round procedure

The orchestrator repeats this procedure for every major, profile, topology,
plan mode, layout comparison, suite, and round:

1. Compute the predeclared seeded order. Six final rounds are preferred so
   each layout runs first three times. If only the plan's minimum five rounds
   are used, predeclare the starting side and balance it across matrix cells.
2. Create a unique temporary instance for the first layout and randomized role
   set inside it.
3. Assert the exact PostgreSQL major/profile and record sanitized environment
   facts.
4. Construct the named layout through its versioned adapter.
5. Verify role, database-incarnation, layout, topology, fixture checksum, and
   exact expected results.
6. Run the common analyze/preparation protocol and capture the complete
   post-layout, pre-timing environment/extension/toolchain facts.
7. Open a fresh backend and record the cold observation separately.
8. Perform the fixed untimed warmup and collect at least 30 warm final samples
   per case. Record every error and timeout in place.
9. Capture SQL, structured plans, planning/execution/buffer metrics, phase
   timings, allocations where applicable, and exact planned/removed/executed
   leaf sets.
10. Re-run exact results, logical checksum, graph-isolation, registry,
    layout-health, and environment checks; fail if a non-allowlisted fact
    differs from the pre-timing capture.
11. Close every client and let the nested harness remove the roles, database,
    and cluster. Verify no owned residue remains.
12. Repeat steps 2–11 for the second layout. Do not reuse the first cluster,
    cache, pool, fixture load, or roles.
13. Copy only sanitized reports and raw samples into the retained artifact
    tree, validate their schemas, and update the checksum inventory.

Every independently reloaded layout is the experimental unit. Do not treat 30
queries within one cluster as 30 independent clusters.

## Exact correctness checks

Timing is inadmissible unless the preflight and postflight checks both pass.
The final harness must compare more than row counts:

- scalar values with type/null distinctions;
- node and edge logical identity, labels/kinds, direction, endpoints, and
  properties;
- path node/edge sequence, direction, length, multiplicity, duplicate shape,
  and ordering where the contract defines order;
- graph isolation and absence of cross-graph identities;
- typed, multi-kind, and untyped result equivalence;
- the dangling-endpoint contract for untyped relationship counts;
- fixture row/checksum equality before timing and no unintended mutation after
  timing;
- exact static leaf union for typed hydration, with no unsound pruning for
  untyped work; and
- stable expected error code/class for cases intentionally excluded from
  timing.

The shared unsupported `length(path)` case may be a versioned expected-error
case excluded from timing, or it may be implemented and timed on both sides.
It must never count as a successful zero-duration observation.

For the retained/current diagnostic corpus only, validate that
`cmd/benchmark` did not silently log and skip a failed case while exiting zero:

```bash
for perf_layout in v1_frozen v2_bootstrap; do
  for perf_suite in cmd graphbench; do
    for perf_round in 1 2 3 4 5; do
      perf_cell_base="$perf_run_root/raw/pg${perf_expected_major}/${perf_layout}/${perf_suite}/r${perf_round}"
      test -d "$perf_cell_base"
      test "$(find "$perf_cell_base" -mindepth 1 -maxdepth 1 -type d -name 'attempt-*' | wc -l)" -eq 1
      perf_attempt_dir="$(find "$perf_cell_base" -mindepth 1 -maxdepth 1 -type d -name 'attempt-*' -print -quit)"
      test -f "$perf_attempt_dir/postgres-environment.json"
      test -f "$perf_attempt_dir/run.log.sanitized"
      if test "$perf_suite" = cmd; then
        test -f "$perf_attempt_dir/cmd.json"
      else
        test -f "$perf_attempt_dir/graphbench.jsonl"
      fi
    done
  done
done

perf_cmd_file_count="$(find "$perf_run_root/raw" -type f -name cmd.json | wc -l)"
test "$perf_cmd_file_count" -eq 10
while IFS= read -r perf_result; do
  jq -e '
    .iterations == 10 and
    (.results | length) == 25 and
    all(.results[]; (.samples | length) == 10)
  ' "$perf_result" >/dev/null
done < <(find "$perf_run_root/raw" -type f -name cmd.json | LC_ALL=C sort)
```

GraphBench also represents case failures in its result stream. The current
diagnostic corpus has 12 records: 11 successful timed cases and the shared
unsupported `length()` expected error:

```bash
perf_graphbench_file_count="$(find "$perf_run_root/raw" -type f -name graphbench.jsonl | wc -l)"
test "$perf_graphbench_file_count" -eq 10
while IFS= read -r perf_result; do
  jq -s -e '
    length == 12 and
    ([.[] | select(.status == "ok")] | length) == 11 and
    ([.[] | select(
      .status == "error" and
      (.error | contains("unknown cypher function: length"))
    )] | length) == 1 and
    all(.[] | select(.status == "ok"); .stats.iterations == 10)
  ' "$perf_result" >/dev/null
done < <(find "$perf_run_root/raw" -type f -name graphbench.jsonl | LC_ALL=C sort)
```

Require 20 matching environment captures and 10 accepted-but-blocked bootstrap
summaries for this two-layout/two-suite/five-round diagnostic:

```bash
test "$(find "$perf_run_root/raw" -type f -name postgres-environment.json | wc -l)" -eq 20
test "$(find "$perf_run_root/raw" -type f -name bootstrap.json | wc -l)" -eq 10

while IFS= read -r perf_result; do
  jq -e '
    .profile == "fast-read" and
    .capture_phase == "post_layout_post_timing" and
    (.server_version_num / 10000 | floor) as $major |
    ($major == 16 or $major == 18) and
    .fsync == "off" and
    .synchronous_commit == "off" and
    .full_page_writes == "off" and
    (.extensions | length) >= 1
  ' "$perf_result" >/dev/null
done < <(find "$perf_run_root/raw" -type f -name postgres-environment.json | LC_ALL=C sort)
```

Also require equal command row/distinct/duplicate shapes and equal GraphBench
status/row counts for every matched case and round. These fixed counts are
diagnostic assertions, not a future corpus contract. The final harness must
derive expected cases from its frozen manifest and enforce full exact results.
If a diagnostic cell has more than one attempt directory, preserve every
attempt but require a versioned admission manifest to identify the one
predeclared primary population; otherwise classify the gate as invalid rather
than selecting the fastest successful retry.

## Artifact layout

Write artifacts outside the temporary PostgreSQL root using this logical
structure:

```text
.bench/runs/<gate-id>/
  manifest.json
  evidence-manifest.json
  evidence-manifest.sha256
  source-status.txt
  allowed-differences.json
  environment/
    host.json
    pg16-fast-read.json
    pg16-durable.json
    pg18-fast-read.json
    pg18-durable.json
  source/
    v1.sha256
    candidate.sha256
    build-metadata.json
    shim-inventory.json
  raw/<major>/<profile>/<estimand>/<topology>/<plan-mode>/<round>/
    cmd.json
    cmd.bench
    graphbench.jsonl
    graphbench.json
    phase.jsonl
    correctness.json
    checksum.json
    layout.json
    postgres.log.sanitized
  plans/<major>/<estimand>/<topology>/<plan-mode>/
  lifecycle/<major>/<profile>/
  reports/
    comparison.json
    comparison.md
    gate-record.json
  approvals/
  SHA256SUMS
```

The exact schema/version may evolve with WP0, but the run/evidence manifests
must include:

- gate ID, timestamps, owners, required approval roles, and provisional gate
  calculation, but not the approval records themselves;
- V1 revision and schema hash; candidate revision/source archive and sanitized
  status; every binary hash and `go version -m` record;
- fixture, case, corpus, adapter, allowed-difference, analyze, statistics,
  budget, ADR, and runbook hashes;
- PostgreSQL binary and server versions, profile/live settings, extensions,
  hardware/provider facts, and database system identity in non-secret form;
- layout construction, migration/acceptance/smoke receipt digests, topology,
  plan mode, seed, order, warmup, sample count, concurrency, and timeouts;
- raw sample/plan/result/checksum locations and hashes;
- every expected and unexpected error, outlier investigation, retry, pause,
  abort, and cleanup result; and
- proof that the final promotion artifact contains no benchmark-only shim.

Generate the checksum file only after redaction and schema validation. Exclude
connection envelopes, passwords, confirmation tokens, and other secrets from
the retained tree. The harness first creates a canonical evidence manifest
covering the frozen evidence while excluding `approvals/`, the final gate
record, and the outer checksum. It records that manifest's digest, and every
approval references the digest from outside the immutable evidence set. The
final gate record links the evidence digest and approvals. After both are
present, create the outer packaging inventory:

```bash
(
  cd "$perf_run_root"
  find . -type f ! -name SHA256SUMS -printf '%P\0' |
    LC_ALL=C sort -z |
    xargs -0 sha256sum > SHA256SUMS
  sha256sum -c SHA256SUMS
)
```

## Final performance gate

### 1. Open and freeze the gate

Create a new gate ID. Name the required owners and freeze:

- the V1 revision and frozen schema;
- the exact V2 promotion candidate source and binaries;
- case/corpus/fixture manifests and logical parameters;
- V1/V2 adapters and their only allowed differences;
- PostgreSQL 16/18 binary versions, profiles, hardware/deployment classes,
  topology/scale/skew/constraint/concurrency matrix, and plan modes;
- analyze, cold/warm, timeout, retry, statistics, and outlier protocols; and
- the approved numeric ADR 0012 budgets and their prior approval digest/date.

Any threshold chosen or widened after confirmatory results are visible
invalidates the decision. Any candidate bit, fixture, adapter, role contract,
schema, or protocol change after freezing creates a new gate or reruns every
affected downstream cell.

### 2. Verify entry conditions

The gate remains `blocked` unless all are true:

- every applicable WP0–WP6 exit gate in the performance plan passes;
- `benchmark_transition` is the real, tested transition orchestrator and not
  the current package-benchmark alias;
- the launcher supports and asserts `fast-read` and `durable` profiles;
- the harness teardown tests pass on success, failure, timeout, interrupt, and
  stale-instance cases;
- accepted target schema assertion, graph provisioning, target temporary-work
  policy, and analyze normalization are supported without ad hoc patches;
- the exact-result, phase, cold/warm, plan/leaf, topology, and statistics
  evidence fields are present and schema-validated;
- ADR 0012 is approved with numeric per-class and operational budgets before
  confirmation;
- PostgreSQL 16 and 18 binaries and every required deployment/topology fixture
  are available; and
- the rehearsal build proves its seam is non-shipping, and the parent review
  has predeclared the narrow disposable-confirmation authorization path. Before
  the `v2_promoted` substage starts, the exact promotion build must separately
  prove that the seam is absent.

Run formatting, generated-worktree, build, unit/race, parser/translator
mutation/template, PostgreSQL semantic/physical, migration, rollback,
restore/rebind, containment, and artifact tests required by the parent plan
before timing. Use separate disposable databases for semantic validation; do
not reuse a benchmark database.

### 3. Run the independent matrix

Run at least five independently provisioned rounds; six counterbalanced rounds
are the default. For both PostgreSQL 16 and 18, run:

- `v1_frozen` versus `v2_bootstrap` under `fast-read` for the complete read,
  guard, translation, planning, pruning, and topology matrix;
- `v1_frozen` versus `v2_migrated` under `fast-read` for the same matrix;
- the approved comparable V1/V2 durable write, provisioning, registry,
  throughput, WAL, disk, lock, storage, and capacity cells;
- V2-only acceptance, migration, rollback, restore/rebind, cleanup, crash, and
  other lifecycle cells against their preapproved absolute operational budgets
  rather than a fabricated V1 ratio;
- forced custom and generic plans at every required topology;
- every approved deployment/hardware class independently; and
- both V2 constructions again with the exact `v2_promoted` artifact and no
  rehearsal seam, after the parent review issues its narrowly scoped
  disposable-confirmation authorization.

The canonical final entry point, once WP0 replaces the placeholder, takes
PostgreSQL binaries and a frozen non-secret run specification and creates all
databases internally:

```bash
BENCHMARK_TRANSITION_SPEC="$perf_run_root/run-spec.json" \
BENCHMARK_TRANSITION_OUTPUT="$perf_run_root" \
BENCHMARK_PG16_BIN_DIR="$perf_pg16_bin" \
BENCHMARK_PG18_BIN_DIR="$perf_pg18_bin" \
make benchmark_transition
```

This is the WP0 interface contract, not a usable final workflow today. The
implemented target must invoke `scripts/test-postgres` and
`scripts/prepare-test-pg` itself. It must reject an ambient admin/application
DSN by default. The older parent-plan example using
`BENCHMARK_TEST_CONNECTION_STRING` must be updated when WP0 lands; an external
database mode is noncanonical and inadmissible unless a separately tested CI
wrapper proves equivalent exclusive ownership, profiles, role isolation, and
cleanup. The retained manifest, not ambient environment, must identify any
such mode.

### 4. Validate the comparison envelope

Before calculating a ratio, require exact equality of case ID, logical
operation, fixtures, parameters, seeds, units, warmup, iterations, schedule,
timeouts, concurrency, plan mode, analyze protocol, PostgreSQL profile/version,
hardware class, and statistics method. Only the declared layout/runtime
adapter fields may differ.

Classify the outcome rather than treating all bad cells as interchangeable.
The population is `invalid` when required raw samples/phase data/SQL/plans or
environment facts are missing; an input hash differs; V1/V2 profiles or analyze
are asymmetric; teardown leaves residue; a secret enters evidence; or the
frozen protocol changes after confirmation begins. Correct the protocol and
rerun the affected cell without erasing the invalid attempt.

A valid protocol produces `evidence_complete_fail`, not `invalid`, when it
detects a correctness/checksum/leaf mismatch; stale-acceptance operation;
duplicate/orphan identity; unexplained error, timeout, or deadlock; security or
role-contract violation; or approved numeric/capacity breach. An accidental
out-of-contract privilege or shim introduced by the harness is invalid setup;
a candidate that needs, creates, or fails to reject that privilege under the
frozen valid protocol is a hard failure.

### 5. Compute the declared statistics

For every case, class, major, topology, plan mode, profile, and deployment
class:

1. Preserve raw cold observations separately.
2. Compute the declared median and p95 for warm samples within each independent
   round/layout.
3. Match V1 and V2 round summaries according to the predeclared schedule.
4. Use the frozen seed to compute 95% bootstrap confidence intervals over
   matched round medians; use the separately documented frozen method for p95.
5. Report V2/V1 ratios and absolute V2 values. For throughput, report V2/V1
   and the absolute lower bound.
6. Report phase, plan, execution, buffer, storage, WAL, lock, relation, and
   capacity results alongside end-to-end latency.
7. Keep each failed/outlier observation in the population and link any
   diagnostic rerun without substituting it.

Summary geomeans are descriptive only. They cannot override a case, class,
major, topology, correctness, or capacity failure.

### 6. Apply the hard gates

| Gate | Passing rule |
|---|---|
| Exact correctness | Zero result, path, duplicate, endpoint, graph-isolation, checksum, expected-error, or identity mismatches. |
| Readiness/security | Zero stale/wrong-connection application operations; next-operation drift tests fail closed; target privileges and namespace match the approved role contract. |
| Typed pruning | Typed counts/traversals retain healthy pruning, and typed path hydration touches exactly the statically known leaf union. |
| Untyped correctness | No unsound pruning; dangling-endpoint semantics remain exact; approved maximum-topology budget passes. |
| Median latency | Upper 95% confidence bound of V2/V1 is at or below the preapproved class/case ratio and the absolute upper bound meets its SLO. |
| P95 latency | Upper 95% confidence bound of V2/V1 is at or below the preapproved tail ratio and the absolute upper bound meets its SLO. |
| Throughput | Lower 95% confidence bound and V2/V1 ratio are at or above the approved floors. |
| Write/migration capacity | Maximum observed WAL, disk, temporary space, locks, relations, pauses, and duration plus the approved safety margin fits every budget. |
| Reliability | Normal confirmation cells have zero unexplained errors, deadlocks, lost receipts, failed cleanup, or unplanned threshold breach. Dedicated fault-injection cells stop at the intended threshold before reserve is consumed and leave coherent, resumable/recoverable state. |
| Cross-version | Every applicable gate passes independently on PostgreSQL 16 and PostgreSQL 18. |
| Evidence | All required artifacts are schema-valid, sanitized, immutable, checksummed, reproducible, and approved against one digest. |

The fixed-floor and translation goals in WP2/WP3 are engineering checkpoints,
not substitutes for the approved ADR 0012 values.

Do not count an intentional fault-injection trigger as a normal-cell breach or
as a performance sample. Its pass condition is the expected safe pause/abort,
durable receipt/status, preserved reserve, bounded recovery, and absence of
additional work after the trigger. A missed/late trigger or incoherent state is
a hard failure.

### 7. Complete the WP8 handoff

After applying the hard gates, calculate a provisional disposition but do not
record `evidence_complete_pass` yet. Complete WP8 against the same candidate:

- run the full parent-plan validation with separately supplied semantic-test
  databases, including formatting, generated-worktree, unit/race, physical,
  migration, rollback, restore/rebind, artifact, mutation/template, and
  backend-equivalence checks;
- add/verify the PostgreSQL 16/18 transition-performance CI jobs and the small
  deterministic ordinary-CI guard/pruning subset;
- update benchmark/GraphBench documentation, the migration runbook, ADR 0012,
  and the G23 traceability row with the immutable evidence location and
  provisional result;
- retain the promotion source, binaries, fixtures, plans, raw samples,
  reports, environment, and schemas under the release evidence policy; and
- prove a clean reader can verify and reproduce the evidence manifest.

These handoff edits may link evidence but may not change the candidate,
fixture, budget, role/schema contract, run protocol, or decision rule. Any such
change invalidates the affected confirmation and re-enters the earliest
affected step. A provisional failure still receives a root-cause report and
explicit ADR/G23 blocker; it is never rewritten into a pass during handoff.

### 8. Record the decision

Run schema validation for every JSON/JSONL artifact, then produce and verify
the canonical evidence manifest. Have the evidence owner, security reviewer,
operator approver, and decision owner sign or otherwise attest the same
evidence-manifest digest in that order. Add those records, then generate and
verify the outer `SHA256SUMS` packaging inventory.

Record exactly one disposition:

- `evidence_complete_pass` only when every hard gate and matrix cell passes;
- `evidence_complete_fail` when a valid population breaches any hard gate;
- `blocked` when a prerequisite, budget, matrix cell, or approval is absent;
  or
- `invalid` when comparison integrity or execution protocol failed.

On pass, link the immutable artifact from ADR 0012 and the G23 traceability row
and hand it to the separate activation review. On fail, retain the full
population and root-cause report without weakening a contract or widening a
budget. On blocked/invalid, retain what ran, correct the prerequisite or
protocol, and use a new or explicitly superseding gate record.

## Interrupt, failure, and residue recovery

For ordinary failures and Ctrl-C, wait for the nested cleanup traps and preserve
their exit status. Do not manually delete a cluster directory or guess role
names.

After an untrappable kill or host failure, use the exact original root, binary
directory, and instance name:

```bash
perf_recovery_pg_root=/exact/recorded/gate-specific/postgres/root
perf_recovery_pg_bin=/exact/recorded/postgresql/bin
perf_recovery_instance=exact-recorded-instance-name
test -d "$perf_recovery_pg_root"
test -x "$perf_recovery_pg_bin/postgres"

DAWGS_TEST_POSTGRES_ROOT="$perf_recovery_pg_root" \
scripts/test-postgres --bin-dir "$perf_recovery_pg_bin" \
  down "$perf_recovery_instance"
```

`down` validates the instance name, runtime-root containment, state, and lock
before stopping and deleting the instance. If it refuses because ownership or
identity is ambiguous, stop and preserve the root for investigation; do not
bypass the guard with `rm`.

Before closing a run, verify:

- every expected temporary instance directory is absent;
- randomized benchmark databases and roles are gone;
- all database clients and maintenance connections are closed;
- no connection envelope or one-use confirmation was copied into retained
  artifacts;
- every expected report was copied outside the temporary root before teardown;
- failed cells and logs are present and sanitized;
- no source copy or process with a rehearsal activation seam remains active;
  and
- the final `SHA256SUMS` verifies from a clean reader context.

After all cells are closed, remove only the known closed lock metadata and
gate-specific empty PostgreSQL root. Refuse any unexpected entry:

```bash
test "$perf_pg_root" = "$PWD/.bench/postgres/$perf_gate_id"
test -d "$perf_pg_root"
test -z "$(find "$perf_pg_root" -mindepth 1 -maxdepth 1 -type d -print -quit)"
test -z "$(find "$perf_pg_root" -mindepth 1 -maxdepth 1 -type f ! -name '.*.lock' -print -quit)"
find "$perf_pg_root" -mindepth 1 -maxdepth 1 -type f -name '.*.lock' -delete
rmdir "$perf_pg_root"
```

For migration-state failures, use durable `status`/`receipt` evidence and the
recovery table in the migration runbook. Never replay a consumed confirmation,
perform post-acceptance in-place rollback, or manually drop retained objects.

## Operator closeout checklist

- [ ] Gate/checkpoint ID, purpose, owners, and intended disposition were named before execution.
- [ ] V1 and candidate source, binaries, fixtures, adapters, protocols, budgets, and runbook were frozen and hashed.
- [ ] PostgreSQL 16/18 binaries and exact live server versions were asserted.
- [ ] Every database was created and removed by the nested temporary harness.
- [ ] `v1_frozen`, `v2_bootstrap`, and `v2_migrated` were labeled and constructed independently.
- [ ] Both V2 constructions used the exact candidate; final confirmation used no rehearsal seam.
- [ ] V1/V2 roles, analyze, profile, topology, plan mode, schedule, samples, and environment were compatible.
- [ ] Cold observations were separate; warm observations met the frozen count and round protocol.
- [ ] Exact results and logical checksums passed before and after timing.
- [ ] Raw samples, phases, SQL, plans, buffers, leaf sets, errors, outliers, storage, locks, WAL, and capacity facts were retained.
- [ ] Both majors and every required topology/deployment cell passed independently.
- [ ] Approved budgets predated confirmation and no limit or population changed afterward.
- [ ] JSON/schema validation, redaction, residue checks, and `SHA256SUMS` verification passed.
- [ ] Required reviewers approved the same canonical evidence-manifest digest.
- [ ] The gate record says `blocked`, `invalid`, `evidence_complete_fail`, or `evidence_complete_pass` without implying activation.
