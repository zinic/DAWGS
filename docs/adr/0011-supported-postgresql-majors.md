# ADR 0011: Supported PostgreSQL majors

Status: accepted for implementation.

PostgreSQL 16 and 18 are mandatory activation majors and independently run the
physical, role, migration, recovery, artifact, and performance suites while
asserting `server_version_num`. PostgreSQL 17 remains explicitly unsupported
for activation until it passes the identical matrix; passing unit or semantic
integration tests alone cannot admit it. The migrator reports and refuses an
unapproved major before mutation.
