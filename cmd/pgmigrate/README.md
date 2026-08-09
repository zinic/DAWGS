# DAWGS PostgreSQL migrator

`dawgs-pg-migrate` is unreleased transition scaffolding for the standalone,
offline V1-to-V2 migration tool. It is restricted to identity-guarded
disposable databases until every activation gate in `gaps_plan.md` passes. It uses
raw pgx connections and never opens the application driver. Run `preflight`,
preserve its report, run `confirm --action migrate`, then pass that state-bound
token to `migrate`. Migration and bootstrap mutators additionally require
`--unreleased-disposable`; this flag is a development containment control, not
production authorization.

The current build remains activation-interlocked even after an acceptance,
restore, smoke, and cleanup rehearsal. Restore rebind and cleanup mutators are
restricted by `--unreleased-disposable`; this is not production authorization.

Connection secrets belong in a mode-0600 JSON file:

```json
{"version":1,"connection_string":"postgresql://..."}
```

Reports are written atomically with mode 0600 and never contain the connection
string or stored graph property values.

Writer probes are also mode-0600 secret envelopes:

```json
{"version":1,"probe_id":"primary-writer","role_name":"legacy_writer","connection_string":"postgresql://...","retirement":"postgres_role_nologin"}
```

The mandatory preliminary sequence is:

```text
preflight -> confirm migrate -> migrate (fence only)
-> confirm verify-fence -> verify-fence (negative database probes)
-> retire external credentials
-> confirm verify-fence -> verify-fence (role retirement receipt)
-> confirm resume -> resume (bounded resumable copy)
-> validate -> confirm accept -> accept
-> confirm smoke -> smoke
```

Supply the same `--manifest`, `--target-connection-file`, repeated
`--writer-probe-file`, `--preparation-role`, `--capacity-evidence`, and
`--backup-evidence` inputs to preflight, migrate, and their confirmation. A
catalog, role, evidence, database-incarnation, graph, or manifest change makes
the token unusable. PostgreSQL 16 and 18 are admitted; every other major is
refused before mutation.

`complete-bootstrap`, writer retirement, and migration `accept` additionally
require `--retirement-connection-file` for the directly authenticated
coordinator. During handoff it atomically transfers managed relations and
routines from the restricted operator to the database's distinct membership-
free `NOLOGIN` owner, records that owner identity, and leaves the target without
schema `CREATE` or direct registry mutation privileges.

`status`/`health` report the authoritative fence phase, blockers, and per-graph
high water. `receipt --migration-id ID` regenerates a report from append-only
database facts after a file-write or client-outcome failure.

Rollback is separately confirmed and remains
`rolled_back_handoff_pending`. It does not re-enable an old writer. After an
external provider makes the preflighted preparation credential directly
usable, confirm `complete-v1-handoff` and provide its mode-0600 connection
file. Accepted recovery never uses in-place rollback.

An accepted restored database is deliberately nonoperational until its copied
source identity is rebound. Supply provider-produced, fresh restore evidence
that identifies both source and target database incarnations and the restored
artifact:

```text
confirm --action rebind-restore --restore-evidence restore.json \
  --target-connection-file target.json
rebind-restore --restore-evidence restore.json --confirm TOKEN \
  --target-connection-file target.json \
  --retirement-connection-file coordinator.json --unreleased-disposable
confirm --action smoke --migration-id ACCEPTED_SCOPE_ID \
  --target-connection-file target.json
smoke --migration-id ACCEPTED_SCOPE_ID --confirm TOKEN \
  --target-connection-file target.json
```

Rebind validates the historical accepted scope, current structural topology,
new database identity, owner/target roles, and the OID-independent cleanup map.
It advances the identity epoch, consumes copied confirmations, clears the old
smoke pointer, and records a rebind receipt. Accepted V2 is restored only after
the new target smoke (or the canonical empty-bootstrap fallback) passes.

Cleanup is legal only for an accepted migration scope with retained legacy
objects. Its strict evidence envelope binds the current database/epoch/scope,
cleanup-map and smoke digests, accepted timestamp and configured soak, a fresh
backup plus successful restore test, a complete fleet ledger, V1 runtime
retirement, approval, and expiry. Rehearsal syntax is:

```text
confirm --action cleanup --migration-id MIGRATION_ID \
  --cleanup-evidence cleanup.json --cleanup-soak 168h
cleanup --migration-id MIGRATION_ID --confirm TOKEN \
  --cleanup-evidence cleanup.json --cleanup-soak 168h \
  --retirement-connection-file coordinator.json --unreleased-disposable
```

The cleanup transaction revalidates each retained object by current epoch OID,
exact name, relkind, dependencies, and logical fingerprint, drops it with
`RESTRICT`, and records an append-only completion/receipt while preserving the
historical map. Fresh-bootstrap cleanup is not applicable. Never drop retained
V1 objects manually.

Fleet closure is validated offline and does not require database credentials:

```text
dawgs-pg-migrate validate-fleet --fleet-ledger fleet.json
```

The command strictly validates supported PostgreSQL majors, artifact/evidence
digests, accepted-scope receipts, formal decommission records, unique
installation IDs, and frozen-ledger closure. See the
[operator runbook](../../docs/edge_kind_partitioning_runbook.md) and
[schema reference](../../docs/edge_kind_partitioning_schemas.md).
