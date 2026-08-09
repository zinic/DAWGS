# ADR 0005: Migration role model

Status: accepted for implementation; PG16/18 privilege evidence required.

Owner, operator, target, old writer, and rollback-preparation are distinct role
OIDs. The owner is `NOLOGIN`; the operator is directly authenticated and owns
the migration-created objects but is not database owner or superuser. The
target is a directly authenticated non-owner with no inherited membership.
Every old writer is directly probed. The installation-dedicated preparation
role remains `NOLOGIN PASSWORD NULL` with no membership or managed access until
rollback handoff. Application pools never use owner or operator credentials.
