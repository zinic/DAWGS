# ADR 0004: Database incarnation and identity epoch

> Runtime enforcement at the service boundary is superseded by ADR 0014.

Status: accepted for implementation.

Live actions bind PostgreSQL system identifier, database OID and name, plus an
OID-independent topology fingerprint. Their canonical digest is the database
incarnation. Restore or database replacement requires rebind; it never silently
inherits readiness. Each successful rebind increments a positive identity
epoch. Confirmations, target identity, smoke, cleanup maps, and receipts bind
the current incarnation and epoch, making pre-restore authority stale.
