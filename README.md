# DAWGS

Database Abstraction Wrapper for Graph Schemas

![A Corgi Treat](logo_small.png)

## Purpose

DAWGS is a collection of tools and query language helpers to enable running property graphs on vanilla PostgreSQL
without the need for additional plugins.

At the core of the library is an abstraction layer that allows users to swap out existing database backends (currently
Neo4j and PostgreSQL) or build their own with no change to query implementation. The query interface is built around
openCypher with translation implementations for backends that do not natively support the query language.

## Development Setup

For users making changes to `dawgs` and its packages, the [go mod replace](https://go.dev/ref/mod#go-mod-file-replace)
directive can be utilized. This allows changes made in the checked out `dawgs` repo to be immediately visible to
consuming projects.

**Example**

```
replace github.com/specterops/dawgs => /home/zinic/work/dawgs
```

### Building and Testing

The [Makefile](Makefile) drives build and test automation. The default `make` target should suffice for normal
development processes.

```bash
make
```

#### Integration Tests

Integration tests are excluded from the default `make` target and require a running database instance. They are
selected via Go build tags and configured through environment variables.

##### PostgreSQL Integration Tests

The following tests require a live PostgreSQL instance:

- `cypher/models/pgsql/test/translation_integration_test.go`
- `cypher/models/pgsql/test/semantic_integration_test.go`

Set the `PG_CONNECTION_STRING` environment variable to a valid PostgreSQL connection string (e.g. `user=postgres dbname=bhe password=bhe4eva host=localhost`), then run:

```bash
PG_CONNECTION_STRING="<connection-string>" make test_pg
```

To run a specific test directly:

```bash
PG_CONNECTION_STRING="<connection-string>" go test -tags pg_integration ./cypher/models/pgsql/test/...
```

##### Neo4j Integration Tests

The following test requires a live Neo4j instance:

- `drivers/neo4j/batch_integration_test.go`

Set the `NEO4J_CONNECTION_STRING` environment variable to a valid Neo4j connection string (e.g.
`neo4j://user:password@host:port`), then run:

```bash
NEO4J_CONNECTION_STRING="<connection-string>" make test_neo4j
```

To run the batch integration test directly:

```bash
NEO4J_CONNECTION_STRING="<connection-string>" go test -tags neo4j_integration ./drivers/neo4j/...
```
