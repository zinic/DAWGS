# ADR 0006: Writer fence and credential retirement

Status: accepted for implementation; activation blocked until rehearsed.

Migration first journals an inverse catalog/ACL/default-ACL/sequence snapshot,
then installs database-local rejection guards and revokes every discovered V1
write path in the same transaction. Fresh directly authenticated old-writer
connections must fail root, child, COPY, DML, DDL, function, and sequence
probes with an approved authorization or managed-fence SQLSTATE. Only after
that durable receipt may credential/provider retirement occur. A separate
verification binds exact retirement evidence and advances `fence_verified`.
No data, root key, registry, or partition mutation is reachable earlier.
