# ADR 0014: Service-start validation boundary

Status: accepted

## Context

The original V2 runtime contract classified the complete PostgreSQL layout on
every transaction, batch, delete, and maintenance acquisition. That made a
trivial operation pay roughly the same catalog and identity cost as startup and
dominated warm latency. V1 performed schema assertion once before serving.

## Decision

Every PostgreSQL driver instance starts closed. A mutating service calls
`AssertSchema`; a read-only service calls `AssertRuntime`. These calls serialize
within the process, and schema assertion also takes a transaction-scoped
PostgreSQL advisory lock across processes. Failure, cancellation, or an unknown
commit outcome leaves the instance closed and clears its schema caches.

Startup performs exhaustive layout classification, typed operational-state
checking, optional graph/kind/leaf/default-graph reconciliation, a pool reset
after DDL, fresh-backend composite registration, an exhaustive postcondition
check, and kind-cache hydration. Only then is readiness published atomically.

Ordinary transactions consult local readiness/activation state and acquire a
pooled connection. They do not classify topology or query the layout marker.
The first use of each physical backend performs one narrow database-incarnation
and direct-runtime-role check and registers composite types; subsequent uses are
an in-memory lookup. Runtime role and session-authorization changes are rejected.

`Readiness` remains a fresh, exhaustive, read-only diagnostic. It never opens a
driver instance. Database constraints, triggers, security-definer provisioners,
and ACLs remain authoritative for invariants enforced by PostgreSQL itself.

## Operational contract

Online DDL, ACL, trigger, migration, restore/rebind, identity, or runtime-role
changes require draining every service instance and restarting/reasserting it.
A layout change after startup is detected by explicit `Readiness`, a new service
assertion, or a new physical backend when identity changed; detection by the
next ordinary transaction is not promised. Externally constructed pools must
not change session identity after validation.

## Consequences

The warm path loses the fixed exhaustive guard round trip. In exchange,
post-start catalog or privilege drift can exist until the next explicit audit
or restart, so drain-and-reassert is required for every managed layout change.

This ADR supersedes per-operation and “next operation” guard requirements in
ADRs 0003, 0004, 0005, 0009, and 0013 and in the V2 planning documents.
