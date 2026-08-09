# Edge-kind partitioning traceability

This ledger is the release authority for the edge-kind partitioning transition.
The reviewed V1 source is commit `bd64d910ef1bfe01b886fa584796275e5c533c8e`.
Its embedded `schema_up.sql` SHA-256 is
`8bea76272e005639d34643f8311599440e1eccc8c3152ca4da1e6dda42c87e0f` and
its `schema_down.sql` SHA-256 is
`333194c7c33a24ae134f01186d2f6d9ed923a76bbfb155b51c98dc1ebb14b2a9`.

`gaps_plan.md` and `edge_kind_partitioning_plan.md` define the contracts and
exit gates. A gap is closed only when its code, automated evidence, and any
required retained operational evidence are linked here. An empty evidence cell
is a release blocker.

| Gap | Contract | Implementation | Automated evidence | Retained evidence |
|---|---|---|---|---|
| G01 | Bootstrap/acceptance identity and smoke | pending-handoff bootstrap, direct target confirmation, accepted scope and smoke receipts in `drivers/pg/migrate/bootstrap.go` and `acceptance.go`; activation remains interlocked | bootstrap and migration integration tests; PG18 local pass | pending distinct durable owner handoff and retained PG16/18 artifacts |
| G02 | Uniform runtime guard | same-connection classification/readiness gate on transactions, batch, maintenance, Raw paths and manager methods; public read-only `Driver.Readiness` | layout unit tests; bootstrap integration proves accepted target remains `activation_not_ready`; PG18 local pass | pending full client/state built-artifact matrix |
| G03 | Typed physical classifier and health | `drivers/pg/internal/catalog`; `drivers/pg/internal/layout`; `drivers/pg/driver.go` | layout unit tests; `TestPGPhysicalCatalogSnapshotDecodesManagedFacts`; PG16/18 physical CI | pending full drift corpus |
| G04 | Structural translator rewriting | `cypher/models/pgsql/translate` | translator tests | n/a |
| G05 | Graph-aware helpers | `drivers/pg/internal/schema/sql/schema_up.sql` | integration graph-isolation tests | n/a |
| G06 | Qualified, bounded canonical SQL | canonical schema owned by `drivers/pg/internal/schema`; structural graph-parent translation, schema-qualified persistent SQL and injection-safe managed identifier formatters | formatter/model/translator and schema/statement qualification tests | pending retained PG16 hostile-search-path evidence |
| G07 | Positive int32 graph IDs and deletion guard | graph fact validation; `dawgs_refuse_graph_delete` | migration integration graph-ID and owner/target delete refusal | n/a |
| G08 | Commit-safe caches | transaction-local graph/kind/default/leaf staging and post-commit publication in `drivers/pg/transaction.go` and `manager.go`; unknown outcomes invalidate all caches | transaction cache success, generic commit-error and `pgx.ErrTxCommitRollback` tests under race | n/a |
| G09 | Relationship identity upsert | graph-wide edge identity registry/trigger replaces expression conflict target | relationship mutation translator tests and PG physical root/direct-leaf/COPY cases | pending retained PG16/18 results |
| G10 | Registry-backed property uniqueness | `drivers/pg/query/query.go`; property registries/triggers | lifecycle plus concurrent cross-kind claimant physical tests; PG18 local race pass; PG16/18 physical CI | pending retained contention budgets and PG16 result |
| G11 | Edge identity and immutable routing | edge identity registry and routing triggers | root/direct-leaf/COPY plus concurrent graph-wide ID claimant physical tests; PG18 local race pass | pending retained contention budgets and PG16 result |
| G12 | Independently verified writer fence | `drivers/pg/migrate/fence.go`; direct-role ACL/default-ACL fence and `NOLOGIN PASSWORD NULL` retirement | migration integration covers fence-commit, probe, retirement-before-receipt recovery, root/direct-child/COPY/DML/DDL denial; PG18 local pass; PG16/18 CI configured | pending CI retirement artifact and external-writer evidence |
| G13 | Coherent one-use preflight | repeatable-read/read-only preflight; typed OID-independent catalog/dependency fingerprint; DB/manifest/writer/capacity/backup/role-bound confirmation | catalog/evidence drift invalidates confirmation; journal self-exclusion and one-use migration test; strict evidence tests; PG18 local pass | pending external-writer/provider/drain inventory and retained evidence |
| G14 | Resumable bounded copy | per-graph durable high-water and copied-count chunks in `drivers/pg/migrate/engine.go` | chunk-size-one crash/retry proves no duplicate copy; PG18 local pass | pending scale/WAL/lock budget artifact |
| G15 | Exhaustive validation | source/target count+typed row digest and bidirectional `EXCEPT ALL`, retained OID, topology/bounds, identity/property registries and sequence validation with acceptance revalidation | row corruption, exact-row inequality, topology drift, stale validation and sequence-lag integration cases | pending retained PG16/18 validation artifacts and broader ACL/index/trigger/dependency drift corpus |
| G16 | Role separation and handoff | role-aware harness; direct writer/target/operator/preparation checks; coordinator retirement; atomic transfer to the database's membership-free NOLOGIN owner; restricted SECURITY DEFINER graph/leaf provisioning; V1 preparation handoff | randomized owner/operator/target/writer/preparation tests; physical owner equality, target DDL/registry denial, wrong-role runtime, bootstrap, migration and rollback PG18 local passes | pending complete default-ACL/role-setting drift corpus and retained PG16 evidence |
| G17 | Frozen-contract rollback | reverse cutover reattaches retained V1 partitions, restores root key and sequence bounds, replays checksum-pinned V1 helpers/triggers, removes V2 runtime objects, and remains fenced until preparation handoff; checksum-pinned frozen V1 client source is built against the pinned revision | pre/post-commit failpoints; complete OID-independent relation/index/constraint/comment/trigger/routine/sequence/dependency equality (excluding unstable internal RI trigger identities); explicit preparation ACL/sequence checks; built-client V1/fence/handoff matrix; PG18 local pass | pending retained PG16 artifact |
| G18 | Identity-mapped cleanup/restore | immutable OID/fingerprint/logical-fact cleanup map, state/identity/smoke/soak/backup/fleet evidence gate, durable receipt; same-cluster and accepted independent restore/rebind plus post-cleanup restoration | cleanup evidence/map/unit and migration/restore/independent-restore integration suites; PG18 local pass | pending retained PG16/18 cleanup and restore reports |
| G19 | Confirmations, receipts, reports | catalog/DB/facts-bound one-use confirmation; append-only fence/probe/retirement/accept/smoke receipts; atomic mode-0600 reports; receipt regeneration | strict envelope tests, catalog-drift invalidation, replay/crash integration cases | pending confirmation coverage for every remaining mutator and archive artifact |
| G20 | Operational caller readiness | public read-only `Driver.Readiness`; Retriever, GraphBench, benchmark, plan corpus, export and dawgrun inspect readiness before work; frontend plus post-translation mutation rejection; structured JSON EXPLAIN and topology/server metadata; V2 export is private/no-replace and checksummed | frontend mutation, structured EXPLAIN/operator, GraphBench storage/reset/baseline, plan-corpus safety, Retriever metadata/checkpoint and interleaved-leaf ordering tests | pending retained tool runs on accepted artifacts after activation |
| G21 | Complete test and CI matrix | PG16/18 containment, physical, migration/bootstrap/rollback/restore and independent restore jobs; frozen fixture/generated-worktree checks; frozen built V1 client and reproducible migrator artifact evidence | full Go suite; local PG18 containment/migration/bootstrap/rollback/restore/physical/harness; matrix configured | accepted-target semantic job is intentionally withheld while activation is interlocked; pending retained PG16/18 CI, built-runtime matrix, coverage floors and performance jobs |
| G22 | Failure-safe PostgreSQL harness | `scripts/test-postgres`; `scripts/prepare-test-pg` | `make test_postgres_harness`; role-aware PG16/18 migration/physical CI jobs | n/a |
| G23 | Pruning/performance/artifacts/runbooks | structured JSON plans/operators and leaf summaries; bounded-cardinality readiness/leaf observation sink and threshold policy; strict `benchdiff` envelope; per-leaf GraphBench maintenance/storage facts; reproducible static migrator build; migration/recovery/schema/archive runbooks | observation redaction/concurrency, explain/plan-corpus/GraphBench/benchdiff tests; clean-snapshot migrator and frozen-client reproducibility pass | activation remains blocked pending measured PG16/18 topology matrix and approved absolute/regression budgets |
| G24 | Fleet and retirement ledger | strict fleet/decommission model and schema; offline closure validator; strict immutable archive manifest and offline retrieval checksum validator; cadence/EOL/ownership policy | fleet/archive strict decode, semantic closure, path/tamper and CLI tests | pending real immutable fleet ledger, EOL date, archive coordinates and clean-environment retrieval drill |

## Completion milestones

- Code complete: G01-G23 have passing local and CI evidence; G24 tooling and
  schemas are complete. This does not activate the layout.
- Fleet complete: every supported installation has a current accepted/smoke
  receipt and the V1 runtime is retired.
- Transition complete: the approved late-upgrade horizon has elapsed and the
  immutable evidence archive is verified.
