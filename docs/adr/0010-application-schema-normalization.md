# ADR 0010: Canonical application-schema normalization

Status: accepted for implementation.

Schema input is normalized before any catalog mutation. Default and named
graph definitions are merged, identical duplicates collapse, conflicting
duplicates fail with a stable error, and graphs, kinds, indexes, and
constraints are canonically sorted. Normalization validates positive int32
graph IDs and every supported signed int16 kind. No partial DDL or cache
publication occurs when normalization fails.
