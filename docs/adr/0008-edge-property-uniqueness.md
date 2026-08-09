# ADR 0008: Graph-wide edge property uniqueness

Status: accepted for implementation; collation and concurrency evidence pending.

Logical constraints are registry rows keyed by graph and property, with
pending, active, draining, and tombstoned states. Values use the documented
JSON text extraction and an explicit stored collation identity. Activation
locks the graph write surface, backfills deterministic claims, validates, then
publishes active in one transaction. Removal drains claims before tombstoning.
Conflicting semantics are errors, never zero-row success. Relationship upsert
uses this authority rather than an expression `ON CONFLICT` arbiter.
