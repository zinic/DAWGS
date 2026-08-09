# ADR 0012: Operational and performance budgets

Status: proposed; activation blocked pending measured PG16/18 baselines.

The retained benchmark artifact must set absolute and V1-regression limits for
topology/relation count, planning latency, leaf-provision lock wait, registry
contention, write latency/throughput, migration atomic lock duration, WAL,
temporary/durable disk, and `max_locks_per_transaction` headroom. Capacity must
retain enough verified free space for source, target, indexes, WAL/archive,
temporary work, and rollback. Numeric pause/abort thresholds will be accepted
only from reproducible published fixtures on PG16 and PG18; no placeholder
number authorizes activation.
