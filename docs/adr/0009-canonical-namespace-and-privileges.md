# ADR 0009: Canonical namespace and privilege policy

> Runtime enforcement at the service boundary is superseded by ADR 0014.

Status: accepted.

Managed objects live in `public`; executable references are schema-qualified
and built-ins use `pg_catalog` where PostgreSQL syntax permits. Identifiers are
trusted, quoted, and bounded by deterministic hashes; logical names remain
data. `SECURITY DEFINER` routines have a fixed `pg_catalog,public,pg_temp`
search path, narrow ownership, explicit signatures, PUBLIC revocation, and
only required target execution grants. Current and future leaf ACLs must match
the recorded role policy, while the target lacks arbitrary schema CREATE and
direct enforcement-registry DML.

The accepted target role has database `CONNECT` and `TEMPORARY`, but not
database or schema `CREATE`. `TEMPORARY` is required by the existing
invoker-security shortest-path helpers, whose scratch relations must remain
owned by and isolated to the calling backend. Those helpers execute
caller-derived query fragments, so converting them to `SECURITY DEFINER` would
expand privilege rather than reduce it. Runtime `Raw` SQL rejects application
attempts to create temporary objects, change role or authorization, or persist
session settings; canonical helpers may still create their internal scratch
relations. Startup validates the database/schema privilege matrix and every
security-definer routine's owner, execute ACL, and fixed search path. Live
rehearsal covers shortest-path execution plus hostile temporary-object and
search-path cases on the same target identity.
