# Edge-kind partitioning operator runbook

This runbook describes the unreleased V1-to-V2 transition protocol. The code is
activation-interlocked and every mutator is limited to an identity-guarded
disposable database. Do not use these commands on a production installation
until the Phase 10 activation release has passed all gates in `gaps_plan.md`.

## Roles and secret handling

Use distinct directly authenticated roles: cluster/database coordinator,
restricted migration operator, V1 writer, dormant V1 preparation role,
membership-free `NOLOGIN` owner, and V2 target. Never give the target membership
in the operator or owner. Store every connection string in a mode-0600 v1
connection envelope and keep it out of reports, logs, and evidence archives.
Close and destroy the operator envelope before exposing the target envelope to
an application or diagnostic tool.

## Forward transition

Preserve every JSON report and append-only receipt digest at each arrow:

```text
preflight -> confirm migrate -> migrate
-> confirm verify-fence -> verify-fence
-> externally retire all writers -> confirm verify-fence -> verify-fence
-> confirm resume -> resume -> validate
-> confirm accept -> accept -> confirm smoke -> smoke
-> soak -> fresh backup/restore rehearsal
-> confirm cleanup -> cleanup
```

Preflight requires current capacity and backup/restore-test evidence, the exact
manifest, all writer probes, preparation role, and target identity. Repeat the
same evidence for confirmation and mutation. A changed database incarnation,
catalog, role, manifest, inventory, or expired evidence invalidates the token.
`migrate` installs only the fence. Do not copy until database probes fail and
the provider-side writer-retirement receipt is durable.

`validate` performs exact multiset comparison and registry, sequence, topology,
and retained-object checks. Acceptance transfers managed ownership and removes
operator privileges. Only the directly authenticated target may perform smoke.
Cleanup requires the accepted migration scope, current smoke, configured soak,
fresh backup and restore test, complete fleet evidence, V1 retirement, and an
exact immutable cleanup map. Bootstrap scopes use the canonical empty map and
must never call cleanup.

## Recovery matrix

| Reported state | Permitted next operation | Never do |
|---|---|---|
| exact V1, no fence | refresh evidence and preflight | initialize V2 through an application DSN |
| fence installed/unverified | verify database probes | copy or reconnect an old writer |
| retirement pending | retire every external credential, then verify again | treat SQL ACL denial alone as retirement |
| fence verified/copying | resume with a fresh one-use confirmation | run two migrators or unbounded copy |
| pending validation/acceptance | validate again or confirmed pre-acceptance rollback | accept a stale digest |
| pending smoke | directly authenticate target and repeat confirmed smoke | expose application traffic |
| accepted V2 | health, backup, restore/rebind, and eventually cleanup | in-place rollback |
| rolled-back handoff pending | complete V1 preparation-role handoff | re-enable the historical writer |
| restore/rebind required | provide fresh restore evidence, rebind, then repeat state-specific smoke/handoff | operate or clean up before rebind |
| drift/unknown/mixed | stop, retain status/catalog reports, escalate | repair objects manually |

For an uncertain client outcome, run `status` and regenerate the newest durable
receipt with `receipt`; never replay a consumed token. A pre-commit failure may
be retried only after status proves the transaction did not commit. A
post-commit report-write failure is recovered from the receipt.

Rolled-back cross-cluster restore is unsupported and returns the stable
`rebind_restore_cross_cluster_unsupported_state` error. Preserve the source or
restore to the same cluster for the preparation-role handoff.

## Fleet waves and archive

Keep one row per installation in a v1 fleet ledger. Unknown, unreachable, V1,
migrating, pending, mixed, drifted, and unverified rows remain `open` blockers;
silence is never decommissioning. A decommission requires approver identity,
reason, ownership proof, last-known state, writer-retirement proof, time, and a
supporting-evidence digest. Validate before every wave and before freezing:

```bash
dist/dawgs-pg-migrate validate-fleet --fleet-ledger fleet.json
```

Pause a wave on any correctness mismatch, identity/catalog drift, writer probe
success, deadlock, unknown commit, capacity/WAL threshold breach, smoke failure,
or regression beyond the approved ADR budget. Freeze only a nonempty ledger
with zero open rows. Fleet closure and V1-runtime retirement are separate
approvals.

## Observation and thresholds

Attach a `pg.ObservationSink` to transition runtimes that exercise readiness or
leaf provisioning. Its fixed schema exposes only event/result enums, typed
layout error code/state, topology digest, aggregate counts, PostgreSQL major,
operation count, and duration. Do not turn report scope IDs, database/role
names, graph/kind IDs, or relation/query text into metric labels. Migration
status reports provide heartbeat, bounded copy counts/high-water, state,
identity epoch, capacity evidence and sequence health; structured EXPLAIN
records provide planned/removed/executed aggregate leaf counts and buffer/time
costs.

Any correctness, identity, smoke, fence, sequence, or unknown-outcome error is
an immediate abort. Pause on any deadlock, writer-probe success, capacity/WAL
shortfall, or numeric lock/planning/provisioning/registry/performance limit from
the approved ADR 0012 evidence. Before those PG16/PG18 limits are published,
the absence of a threshold is itself an activation blocker and never means
unlimited operation.

Retain the exact static migrator and checksum, runtime checksum, source/module
metadata, frozen V1 schema and fixtures, versioned schemas, reports, receipts,
supported PostgreSQL matrix, this runbook, publication/retention owners, and
EOL dates for the published late-upgrade horizon. Run the retained workflow on
PG16 and PG18 at the scheduled cadence. At horizon end, perform a clean-room
retrieval, checksum, and execution drill before removing migration code.
