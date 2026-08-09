# ADR 0001: Edge-kind partitioning contracts

Status: accepted for implementation; activation remains gated.

The PostgreSQL runtime supports only the V2 lineage after activation. V1 is an
offline migration and pre-acceptance rollback format, never a runtime fallback.
The authoritative state machine is durable database state plus append-only
receipts; relation names and a version marker are insufficient.

Each database has an incarnation identifier and identity epoch. Runtime access
requires the directly authenticated non-owner target role recorded for the
current epoch and a passed current smoke receipt. Owner, operator, target,
old-writer, and rollback-preparation identities are distinct. The writer fence
is durable database enforcement plus independent direct-auth probes and
credential/provider retirement evidence.

Edge IDs are unique within a graph across all kind leaves. `id`, `graph_id`, and
`kind_id` are immutable routing columns. Property uniqueness is graph-wide,
registry-backed, uses explicit collation/extraction semantics, and has pending,
active, draining, and tombstoned lifecycle states.

All managed objects live in `public`; executable SQL qualifies managed and
`pg_catalog` objects, uses bounded hashed identifiers, and SECURITY DEFINER
routines have a fixed safe search path and narrow grants. Application schemas
are normalized and conflict-checked before mutation.

PostgreSQL 16 and 18 are supported. PostgreSQL 17 is supported only after the
same physical, migration, recovery, role, and performance matrix passes; until
then it is explicitly unsupported for activation.

Published budgets and evidence gate topology size, lock duration, WAL/disk
headroom, planning latency, registry contention, and throughput regression.
Rollback ends at acceptance. Cleanup requires current identity/smoke, soak,
backup/restore, fleet, and OID-map evidence. V1 runtime retirement, late-upgrade
horizon, and immutable archival are separate completion milestones.
