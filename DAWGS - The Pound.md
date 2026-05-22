# Unified Data Model Ideas

## BloodHound Integration

Regardless of datasource, BloodHound should render it.

- Seek experimental, non-production affecting paths
  - Enable early looks without moving the BHE fleet (CE focused at first)
- Renders paths - driver returns paths
- Renders tables - driver returns lists, or tables
- Syntax highlighting for combined query languages
- Configuration paths for drivers
  - Separate config file or surface to keep BH config cleaner

### BloodHound Enterprise Extensibility

- Event log accessibility via additional DAWGS drivers
  - Enahnces findings, node and relationship information, highlights movement
  - Potential time-travel applications (watching actor movement)
  - Detections and potential real-time investigation or IR flows

- SP is over privleaged because certain permissions haven't been exercised
  - Policy lockdown

## Language Integration

- KQL
- GQL
- Cypher

## Schema Driven

- Less interesting for just-in-time explore.
- Would require a lot of up front work. Limits exploration by the user.
- Per-data schema. Heavyweight UX for users.
- Too much cognative load for nomral workflows.

## Join Driven

- More interesting for just-in-time explore.
- JOIN semantics could be heavier per-query.
- Format on projection - join at materialization time.

## Backend Routing

- ADX
- Defender/XDR
- Sentinel
- PgSQL
- Neo4j

### Example Routing

## Query Wrapper Language

- Grammar
- Syntax highlighting
- Rich error surface

### Combined Datasets Without Entity Merging

```
FROM cypher_backend
MATCH p (n)-[:MemberOf*..]->(:Group)
RETURN P

FROM adx_backend
-- KQL
-- Cypher
-- GQL
```

### Combined Datasets With Entity Merging

```
FROM cypher_backend AS c
MATCH p (n)-[:MemberOf*..]->(:Group)
RETURN P

FROM adx_backend AS a
-- KQL
-- Cypher
-- GQL

JOINS
a.nodeid = c.objectid
```
