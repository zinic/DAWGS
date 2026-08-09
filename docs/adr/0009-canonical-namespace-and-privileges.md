# ADR 0009: Canonical namespace and privilege policy

Status: accepted for implementation; hostile-search-path evidence required.

Managed objects live in `public`; executable references are schema-qualified
and built-ins use `pg_catalog` where PostgreSQL syntax permits. Identifiers are
trusted, quoted, and bounded by deterministic hashes; logical names remain
data. `SECURITY DEFINER` routines have a fixed `pg_catalog,public,pg_temp`
search path, narrow ownership, explicit signatures, PUBLIC revocation, and
only required target execution grants. Current and future leaf ACLs must match
the recorded role policy, while the target lacks arbitrary schema CREATE and
direct enforcement-registry DML.
