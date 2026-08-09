# ADR 0002: V2-only runtime lineage and V1 retirement

Status: accepted for implementation; activation gated.

The application runtime recognizes V1 only to return a migration-required
error. It never bootstraps, repairs, or operates V1. V1-to-V2 work belongs to a
version-pinned offline artifact, and pre-acceptance rollback is the only path
that reconstructs V1. Runtime V1 code may be removed only after the fleet
ledger proves every supported installation accepted V2 or was decommissioned.
The frozen migrator and V1 fixtures remain until the separately approved
late-upgrade horizon expires.
