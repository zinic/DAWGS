# Edge-kind transition artifact retention policy

Publication owner: DAWGS release engineering. Retention owner: DAWGS database
operations. Incident and scheduled-cadence owner: DAWGS PostgreSQL maintainers.

The exact migrator/runtime binaries, binary and input checksums, source/module
identity, frozen V1 artifacts, supported-major matrix, versioned schemas,
operator runbook, and every phase/fleet/archive report are retained in the
immutable release evidence store. Write and retention administration must be
separate from migration operators. Evidence must be content-addressed,
versioned, access-audited, and protected from overwrite or shortened retention.

The EOL date is not a placeholder embedded in source. A release must publish an
approved calendar date that is no earlier than the contracted late-upgrade
horizon and record it in the immutable release metadata. Until that date, the
retained workflow runs at least weekly on PG16 and PG18; failure pages the
incident owner. Removal requires fleet closure, V1-runtime retirement, elapsed
horizon, independent approval, and a successful clean-environment archive
retrieval/checksum/execution drill on every retained PostgreSQL major.

The retrieval inventory uses
`docs/schemas/archive-manifest-v1.schema.json`. In a clean retrieval directory,
run `dawgs-pg-migrate validate-archive --archive-manifest <file>
--archive-root <directory>` before executing an archived binary. This offline
check rejects unknown fields and unsafe paths, enforces separated publication
and retention owners, requires the PG16/PG18 matrix and mandatory artifact
classes, and verifies every byte count and SHA-256.
