# ADR 0007: Graph-local edge identity and immutable routing

Status: accepted for implementation.

`(graph_id, edge_id)` is unique across all kind leaves. A registry backed by a
deferred cleanup relationship restores that identity independently of
PostgreSQL partition-key limitations. `id`, `graph_id`, and `kind_id` are
immutable after insert. Root, graph-parent, direct-leaf, COPY, batch, Cypher,
and function writes use the same authority and deterministic claim ordering.
ID-only operations resolve graph and kind through the registry and preserve
input ordinality and duplicates.
