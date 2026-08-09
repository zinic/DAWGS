# Export

The transition exporter requires `PGCONN`; it never falls back to an
application credential. It runs PostgreSQL readiness before opening an output,
refuses to overwrite an existing path, writes the export with mode 0600, and
publishes `<output>.metadata.json` atomically. The sidecar checksums the export
and records layout version/state, topology fingerprint, graph/kind/leaf counts,
database identity/epoch, owner/target/smoke state, and PostgreSQL server facts.

This exporter is V2-only. The checksum-pinned V1-compatible source retained at
`drivers/pg/migrate/testdata/v1_export_client.go.txt` is built from the pinned
V1 revision by `make verify_frozen_v1_client`; do not use a transition binary
against V1.

Exports a graph from a PostgreSQL database to an OpenGraph JSON file. This is how local test datasets (like `phantom.json`) are captured from a running BloodHound instance.

## Usage

```bash
# Default connection (bloodhound local dev) and output (graph_export.json)
go run ./cmd/export

# Custom output file
go run ./cmd/export my_graph.json

# Custom connection
PGCONN="postgresql://dawgs:dawgs@localhost:5432/dawgs" go run ./cmd/export
```

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `PGCONN` | `postgresql://bloodhound:bloodhoundcommunityedition@localhost:5432/bloodhound` | PostgreSQL connection string |

## Output

Writes an OpenGraph JSON document with all nodes and edges from the default graph:

```json
{
  "graph": {
    "nodes": [{"id": "1", "kinds": ["User", "Base"], "properties": {...}}, ...],
    "edges": [{"start_id": "1", "end_id": "2", "kind": "MemberOf"}, ...]
  }
}
```

The output can be placed in `integration/testdata/local/` for use with the benchmark tool and integration tests.
