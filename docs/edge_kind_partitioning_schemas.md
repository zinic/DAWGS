# Edge-kind transition schema reference

All JSON inputs are UTF-8, contain exactly one value, reject duplicate and
unknown keys, and use an explicit integer `version`. SHA-256 values are 64
hexadecimal characters. Timestamps use RFC 3339 and evidence expiration is
checked against the database-bound operation. Reports are atomically replaced
with mode 0600 and contain no DSNs or graph property values.

| Envelope | Version | Purpose and required binding |
|---|---:|---|
| connection | 1 | mode-0600 secret containing one connection string |
| manifest | 1 | canonical graph/kind contract and target role |
| capacity | 1 | database/system identity, observed/expiry times, available/required/WAL bytes |
| backup | 1 | database/system identity, completion and restore-test times, expiry, artifact checksum |
| restore | 1 | source and target incarnation, epoch, resource IDs, artifact, provider observation and target role |
| cleanup | 1 | current database/epoch/scope/map/smoke, soak, fresh backup/restore, fleet totals, V1 retirement and approval |
| report | 1 | action/state, scope, confirmation/receipt pointers, identity, layout/topology, smoke/cleanup and recovery |
| fleet ledger | 1 | one installation row, artifact/evidence/receipt digests, closure or formal decommission record |
| archive manifest | 1 | retained artifact paths, classes, sizes, checksums, owners, cadence, supported majors and EOL |

The machine-readable fleet schema is
[`schemas/fleet-ledger-v1.schema.json`](schemas/fleet-ledger-v1.schema.json).
The Go decoder remains authoritative because it additionally enforces
cross-field rules: declared PostgreSQL major membership, unique installation
IDs, accepted-scope receipts, old-writer retirement, decommission evidence,
and frozen-ledger closure.

The machine-readable archive schema is
[`schemas/archive-manifest-v1.schema.json`](schemas/archive-manifest-v1.schema.json).
The offline `validate-archive` command additionally rejects unsafe paths,
requires all retained artifact classes and verifies every retrieved file.

Retriever transition manifests remain `retriever-jsonl-collection-v1` for
wire compatibility, but PostgreSQL dumps now include a `source.postgres`
layout-health object and `source.postgres_sha256`. It records layout/state,
topology fingerprint, graph/kind/leaf counts, database identity and epoch,
owner/target/smoke facts, and `server_version_num`. Resume checkpoints bind the
same digest. This transition exporter is V2-only. The pinned V1 exporter source
and module metadata are validated and built as a separately checksummed static
artifact by `make verify_frozen_v1_client`.
