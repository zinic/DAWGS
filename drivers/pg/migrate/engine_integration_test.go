//go:build migration_integration

package migrate

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgdriver "github.com/specterops/dawgs/drivers/pg"
	pgmodel "github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/v1_schema.sql
var frozenV1Schema string

func TestPreflightConfirmationIsDatabaseAndFactsBoundAndOneUse(t *testing.T) {
	connectionString := os.Getenv("CONNECTION_STRING")
	if connectionString == "" {
		t.Skip("CONNECTION_STRING is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, connectionString)
	require.NoError(t, err)
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = connection.Conn().PgConn().Exec(ctx, frozenV1Schema).ReadAll()
	require.NoError(t, err)
	_, err = connection.Conn().PgConn().Exec(ctx, `
insert into public.graph(id,name) values(1,'migration_test');
insert into public.kind(id,name) values(1,'MIGRATES_TO');
create table public.node_1 partition of public.node for values in (1);
create table public.edge_1 partition of public.edge for values in (1);
insert into public.edge_1(id,graph_id,start_id,end_id,kind_id,properties)
values(1,1,10,11,1,'{"identity":"edge-1"}'::jsonb),
      (2,1,12,13,1,'{"identity":"edge-2"}'::jsonb);`).ReadAll()
	connection.Release()
	require.NoError(t, err)
	constraintName := pgmodel.ConstraintName(pgmodel.EdgePartitionTableName(1), graph.Constraint{Name: "identity", Field: "identity", Type: graph.BTreeIndex})
	_, err = pool.Exec(ctx, fmt.Sprintf(`create unique index %s on public.edge_1 ((properties ->> 'identity'))`, pgx.Identifier{constraintName}.Sanitize()))
	require.NoError(t, err)
	oldWriterRole := os.Getenv("PG_TEST_OLD_WRITER_ROLE")
	oldWriterConnectionString := os.Getenv("PG_TEST_OLD_WRITER_CONNECTION_STRING")
	adminConnectionString := os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING")
	targetRole := os.Getenv("PG_TEST_TARGET_ROLE")
	targetConnectionString := os.Getenv("PG_TEST_TARGET_CONNECTION_STRING")
	preparationRole := os.Getenv("PG_TEST_PREPARATION_ROLE")
	if oldWriterRole == "" || oldWriterConnectionString == "" || adminConnectionString == "" || targetRole == "" || targetConnectionString == "" || preparationRole == "" {
		t.Skip("role-aware migration test requires writer, target, preparation, and admin coordinator inputs")
	}
	quotedWriter := pgx.Identifier{oldWriterRole}.Sanitize()
	_, err = pool.Exec(ctx, fmt.Sprintf(`grant usage on schema public to %[1]s;
grant select,insert,update,delete,truncate on all tables in schema public to %[1]s;
grant usage,update on all sequences in schema public to %[1]s;
grant execute on all functions in schema public to %[1]s`, quotedWriter))
	require.NoError(t, err)

	manifestJSON := fmt.Sprintf(`{"version":1,"installation_id":"integration-v1","source_configuration":{"artifact":"drivers/pg/migrate/testdata/v1_schema.sql","sha256":"8bea76272e005639d34643f8311599440e1eccc8c3152ca4da1e6dda42c87e0f"},"owner_attestation":{"owner_id":"integration-test","attested_at":"2026-08-01T00:00:00Z","statement_version":1},"graphs":[{"name":"migration_test","edge_kinds":["MIGRATES_TO"],"edge_constraints":[{"logical_name":%q,"property_name":"identity","constraint_type":"btree","collation_name":"default"}]}],"excluded_unmaterialized_graphs":[]}`, constraintName)
	manifest, err := DecodeManifest(strings.NewReader(manifestJSON))
	require.NoError(t, err)
	databaseIdentity, err := currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	evidenceTime := time.Now().UTC()
	capacity := &CapacityEvidence{Version: 1, EvidenceID: "integration-capacity", DatabaseName: databaseIdentity.Name,
		SystemIdentifier: databaseIdentity.SystemIdentifier, ObservedAt: evidenceTime, ExpiresAt: evidenceTime.Add(time.Hour),
		AvailableBytes: 1 << 30, RequiredBytes: 1 << 20, EstimatedWALBytes: 1 << 20}
	backup := &BackupEvidence{Version: 1, EvidenceID: "integration-backup", DatabaseName: databaseIdentity.Name,
		SystemIdentifier: databaseIdentity.SystemIdentifier, CompletedAt: evidenceTime.Add(-time.Minute),
		RestoreTestedAt: evidenceTime, ExpiresAt: evidenceTime.Add(time.Hour), ArtifactSHA256: strings.Repeat("a", 64)}
	engine := &Engine{
		Pool: pool, AllowUnreleasedDisposable: true,
		WriterProbes: []WriterProbe{{Version: 1, ProbeID: "integration-old-writer", RoleName: oldWriterRole,
			ConnectionString: oldWriterConnectionString, Retirement: "postgres_role_nologin"}},
		RetirementConnectionString: adminConnectionString,
		TargetConnectionString:     targetConnectionString,
		PreparationRole:            preparationRole,
		CapacityEvidence:           capacity,
		BackupEvidence:             backup,
		CopyChunkSize:              1,
	}
	omittedConstraintManifest := manifest
	omittedConstraintManifest.Graphs = append([]ManifestGraph(nil), manifest.Graphs...)
	omittedConstraintManifest.Graphs[0].EdgeConstraints = nil
	_, err = engine.Preflight(ctx, omittedConstraintManifest)
	require.ErrorContains(t, err, "declares 0 edge property constraints")
	preflight, err := engine.Preflight(ctx, manifest)
	require.NoError(t, err)
	require.Empty(t, preflight.ConfirmationToken)
	require.Len(t, preflight.PreflightDigest, 64)
	var confirmationTableExists bool
	require.NoError(t, pool.QueryRow(ctx, `select pg_catalog.to_regclass('public.dawgs_operation_confirmation') is not null`).Scan(&confirmationTableExists))
	require.False(t, confirmationTableExists)
	confirmation, err := engine.ConfirmMigration(ctx, manifest)
	require.NoError(t, err)
	require.NotEmpty(t, confirmation.ConfirmationToken)
	require.Equal(t, preflight.PreflightDigest, confirmation.PreflightDigest)

	_, err = pool.Exec(ctx, `comment on table public.edge is 'catalog drift after confirmation'`)
	require.NoError(t, err)
	_, err = engine.Migrate(ctx, manifest, confirmation.ConfirmationToken)
	require.ErrorContains(t, err, "bound to different facts")
	_, err = pool.Exec(ctx, `comment on table public.edge is null`)
	require.NoError(t, err)
	confirmation, err = engine.ConfirmMigration(ctx, manifest)
	require.NoError(t, err)
	require.Equal(t, preflight.PreflightDigest, confirmation.PreflightDigest)
	capacity.AvailableBytes++
	_, err = engine.Migrate(ctx, manifest, confirmation.ConfirmationToken)
	require.ErrorContains(t, err, "bound to different facts")
	capacity.AvailableBytes--
	confirmation, err = engine.ConfirmMigration(ctx, manifest)
	require.NoError(t, err)
	require.Equal(t, preflight.PreflightDigest, confirmation.PreflightDigest)

	engine.Failpoint = func(name string) error {
		if name == "after_fence_commit" {
			return context.Canceled
		}
		return nil
	}
	report, err := engine.Migrate(ctx, manifest, confirmation.ConfirmationToken)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "fence_installed_unverified", report.State)
	require.NotEmpty(t, report.MigrationID)
	require.NotEmpty(t, report.ReceiptDigest)
	regenerated, err := engine.Receipt(ctx, report.MigrationID, "fence_installation", "")
	require.NoError(t, err)
	require.Equal(t, report.ReceiptDigest, regenerated.ReceiptDigest)
	fenceStatus, err := engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "fence_installed_unverified", fenceStatus.State)
	require.Equal(t, []string{"writer_probe_required"}, fenceStatus.Blockers)

	// Fence-only V1 has no layout marker yet. A same-cluster restore still
	// requires an identity rebind before its writer probes can resume.
	fenceCloneSource, err := currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	fenceCloneName := fenceCloneSource.Name + "_fence_restore"
	pool.Close()
	fenceCloneRootURL := connectionForDatabase(t, adminConnectionString, "postgres")
	fenceCloneRoot, err := pgx.Connect(ctx, fenceCloneRootURL)
	require.NoError(t, err)
	qFenceClone := pgx.Identifier{fenceCloneName}.Sanitize()
	qFenceSource := pgx.Identifier{fenceCloneSource.Name}.Sanitize()
	qFenceOwner := pgx.Identifier{os.Getenv("PG_TEST_OWNER_ROLE")}.Sanitize()
	qFenceOperator := pgx.Identifier{os.Getenv("PG_TEST_OPERATOR_ROLE")}.Sanitize()
	qFenceTarget := pgx.Identifier{targetRole}.Sanitize()
	qFenceWriter := pgx.Identifier{oldWriterRole}.Sanitize()
	_, err = fenceCloneRoot.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`, qFenceClone, qFenceSource, qFenceOwner))
	require.NoError(t, err)
	_, err = fenceCloneRoot.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect,create,temp on database %s to %s; grant connect on database %s to %s,%s`,
		qFenceClone, qFenceClone, qFenceOperator, qFenceClone, qFenceTarget, qFenceWriter))
	require.NoError(t, err)
	fenceCloneOperator := connectionForDatabase(t, connectionString, fenceCloneName)
	fenceCloneTarget := connectionForDatabase(t, targetConnectionString, fenceCloneName)
	fenceCloneWriter := connectionForDatabase(t, oldWriterConnectionString, fenceCloneName)
	fenceCloneAdmin := connectionForDatabase(t, adminConnectionString, fenceCloneName)
	fenceCloneEngine, err := New(ctx, fenceCloneOperator)
	require.NoError(t, err)
	fenceCloneEngine.AllowUnreleasedDisposable = true
	fenceCloneEngine.RetirementConnectionString = fenceCloneAdmin
	fenceCloneEngine.WriterProbes = []WriterProbe{{Version: 1, ProbeID: "integration-old-writer",
		RoleName: oldWriterRole, ConnectionString: fenceCloneWriter, Retirement: "postgres_role_nologin"}}
	fenceCloneIdentity, err := currentDatabaseIdentity(ctx, fenceCloneEngine.Pool)
	require.NoError(t, err)
	fenceCloneNow := time.Now().UTC()
	fenceCloneEvidence := RestoreEvidence{Version: 1, EvidenceID: "fence-only-restore", ScopeKind: "migration",
		ScopeID: report.MigrationID, SourceDatabaseOID: fenceCloneSource.OID, SourceDatabaseName: fenceCloneSource.Name,
		SourceSystemIdentifier: fenceCloneSource.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: fenceCloneIdentity.OID, TargetDatabaseName: fenceCloneIdentity.Name,
		TargetSystemIdentifier: fenceCloneIdentity.SystemIdentifier,
		SourceResourceID:       "same-cluster/source/" + fenceCloneSource.Name,
		TargetResourceID:       "same-cluster/restore/" + fenceCloneIdentity.Name,
		RestoreIncarnation:     "fence-only-restore", ArtifactSHA256: strings.Repeat("5", 64),
		OwnerRoleName: os.Getenv("PG_TEST_OWNER_ROLE"), TargetRoleName: targetRole,
		ObservedAt: fenceCloneNow, ExpiresAt: fenceCloneNow.Add(time.Hour)}
	fenceCloneRebindConfirmation, err := fenceCloneEngine.ConfirmRebindRestore(ctx, fenceCloneEvidence, fenceCloneTarget)
	require.NoError(t, err)
	_, err = fenceCloneEngine.RebindRestore(ctx, fenceCloneEvidence, fenceCloneRebindConfirmation.ConfirmationToken, fenceCloneTarget)
	require.NoError(t, err)
	fenceCloneVerifyConfirmation, err := fenceCloneEngine.ConfirmOperation(ctx, "verify-fence", report.MigrationID)
	require.NoError(t, err)
	fenceCloneVerified, err := fenceCloneEngine.VerifyFence(ctx, report.MigrationID, fenceCloneVerifyConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "fence_database_verified_retirement_pending", fenceCloneVerified.State)
	fenceCloneEngine.Close()
	_, err = fenceCloneRoot.Exec(ctx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, fenceCloneName)
	require.NoError(t, err)
	_, err = fenceCloneRoot.Exec(ctx, fmt.Sprintf(`drop database %s`, qFenceClone))
	require.NoError(t, err)
	require.NoError(t, fenceCloneRoot.Close(ctx))
	pool, err = pgxpool.New(ctx, connectionString)
	require.NoError(t, err)
	defer pool.Close()
	engine.Pool = pool

	_, err = engine.Resume(ctx, report.MigrationID, "")
	require.ErrorContains(t, err, "writer_probe_required")
	engine.Failpoint = func(name string) error {
		if name == "after_database_fence_verified" {
			return context.Canceled
		}
		return nil
	}
	verifyConfirmation, err := engine.ConfirmOperation(ctx, "verify-fence", report.MigrationID)
	require.NoError(t, err)
	fenceReport, err := engine.VerifyFence(ctx, report.MigrationID, verifyConfirmation.ConfirmationToken)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "fence_database_verified_retirement_pending", fenceReport.State)
	engine.Failpoint = func(name string) error {
		if name == "after_writer_retirement_before_receipt" {
			return context.Canceled
		}
		return nil
	}
	retirementConfirmation, err := engine.ConfirmOperation(ctx, "verify-fence", report.MigrationID)
	require.NoError(t, err)
	fenceReport, err = engine.VerifyFence(ctx, report.MigrationID, retirementConfirmation.ConfirmationToken)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "writer_retirement_in_progress", fenceReport.State)
	engine.Failpoint = nil
	fenceReport, err = engine.VerifyFence(ctx, report.MigrationID, "")
	require.NoError(t, err)
	require.Equal(t, "fence_verified", fenceReport.State)
	engine.Failpoint = func(name string) error {
		if name == "after_graph_1_chunk_commit" {
			return context.Canceled
		}
		return nil
	}
	resumeConfirmation, err := engine.ConfirmOperation(ctx, "resume", report.MigrationID)
	require.NoError(t, err)
	report, err = engine.Resume(ctx, report.MigrationID, resumeConfirmation.ConfirmationToken)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "v2_migrating", report.State)
	engine.Failpoint = nil
	resumeConfirmation, err = engine.ConfirmOperation(ctx, "resume", report.MigrationID)
	require.NoError(t, err)
	report, err = engine.Resume(ctx, report.MigrationID, resumeConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "v2_pending_acceptance", report.State)
	var migratedConstraintState string
	var migratedClaimCount int64
	require.NoError(t, pool.QueryRow(ctx, `select lifecycle_state from public.edge_property_constraint
where graph_id=1 and logical_name=$1`, constraintName).Scan(&migratedConstraintState))
	require.Equal(t, "active", migratedConstraintState)
	require.NoError(t, pool.QueryRow(ctx, `select count(*) from public.edge_property_unique_value where graph_id=1`).Scan(&migratedClaimCount))
	require.EqualValues(t, 2, migratedClaimCount)
	_, err = pool.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties)
values(3,1,14,15,1,'{"identity":"edge-1"}')`)
	require.Error(t, err)
	_, err = pool.Exec(ctx, `alter sequence public.edge_id_seq restart with 1`)
	require.NoError(t, err)
	_, err = engine.Validate(ctx, report.MigrationID)
	require.ErrorContains(t, err, "lags maximum")
	_, err = pool.Exec(ctx, `alter sequence public.edge_id_seq restart with 3`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `update public.edge_1 set properties='{"identity":"corrupted"}'::jsonb where id=1`)
	require.NoError(t, err)
	_, err = engine.Validate(ctx, report.MigrationID)
	require.ErrorContains(t, err, "target changed")
	_, err = pool.Exec(ctx, `update public.edge_1 set properties='{"identity":"edge-1"}'::jsonb where id=1`)
	require.NoError(t, err)
	validation, err := engine.Validate(ctx, report.MigrationID)
	require.NoError(t, err)
	require.Len(t, validation.ValidationDigest, 64)

	var consumedConfirmations int
	require.NoError(t, pool.QueryRow(ctx, `select count(*) from public.dawgs_operation_confirmation
where action='migrate' and scope_id=$1 and consumed_at is not null`, confirmation.ManifestSHA256).Scan(&consumedConfirmations))
	require.Equal(t, 1, consumedConfirmations)

	targetConnection, err := pgx.Connect(ctx, targetConnectionString)
	require.NoError(t, err)
	targetIdentity, err := readSessionIdentity(ctx, targetConnection)
	require.NoError(t, err)
	require.Equal(t, targetRole, targetIdentity.SessionUser)
	require.False(t, targetIdentity.Superuser)
	require.NotEqual(t, targetIdentity.RoleOID, targetIdentity.DatabaseOwner)
	require.NoError(t, targetConnection.Close(ctx))

	acceptConfirmation, err := engine.ConfirmAcceptance(ctx, report.MigrationID, validation.ValidationDigest, targetConnectionString)
	require.NoError(t, err)
	require.NotEmpty(t, acceptConfirmation.ConfirmationToken)
	acceptance, err := engine.Accept(ctx, report.MigrationID, validation.ValidationDigest, acceptConfirmation.ConfirmationToken, targetConnectionString)
	require.NoError(t, err)
	require.Equal(t, "v2_pending_smoke", acceptance.State)
	require.NotEmpty(t, acceptance.ReceiptDigest)
	_, err = engine.ConfirmOperation(ctx, "rollback", report.MigrationID)
	require.ErrorContains(t, err, "not legal")

	// A same-cluster restore at the acceptance cutoff must invalidate the
	// copied smoke pointer, rebind the retained cleanup map, and repeat smoke.
	pendingSmokeSourceIdentity, err := currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	pendingSmokeRestoreName := pendingSmokeSourceIdentity.Name + "_pending_smoke"
	pool.Close()
	adminRootURL := connectionForDatabase(t, adminConnectionString, "postgres")
	adminRoot, err := pgx.Connect(ctx, adminRootURL)
	require.NoError(t, err)
	qPendingSmokeRestore := pgx.Identifier{pendingSmokeRestoreName}.Sanitize()
	qPendingSmokeSource := pgx.Identifier{pendingSmokeSourceIdentity.Name}.Sanitize()
	qPendingOwner := pgx.Identifier{os.Getenv("PG_TEST_OWNER_ROLE")}.Sanitize()
	qPendingOperator := pgx.Identifier{os.Getenv("PG_TEST_OPERATOR_ROLE")}.Sanitize()
	qPendingTarget := pgx.Identifier{targetRole}.Sanitize()
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`,
		qPendingSmokeRestore, qPendingSmokeSource, qPendingOwner))
	require.NoError(t, err)
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
	grant connect on database %s to %s,%s`, qPendingSmokeRestore, qPendingSmokeRestore, qPendingOperator, qPendingTarget))
	require.NoError(t, err)
	pendingSmokeOperator := connectionForDatabase(t, connectionString, pendingSmokeRestoreName)
	pendingSmokeTarget := connectionForDatabase(t, targetConnectionString, pendingSmokeRestoreName)
	pendingSmokeAdmin := connectionForDatabase(t, adminConnectionString, pendingSmokeRestoreName)
	pendingSmokeEngine, err := New(ctx, pendingSmokeOperator)
	require.NoError(t, err)
	pendingSmokeEngine.AllowUnreleasedDisposable = true
	pendingSmokeEngine.RetirementConnectionString = pendingSmokeAdmin
	pendingSmokeTargetIdentity, err := currentDatabaseIdentity(ctx, pendingSmokeEngine.Pool)
	require.NoError(t, err)
	pendingSmokeNow := time.Now().UTC()
	pendingSmokeEvidence := RestoreEvidence{Version: 1, EvidenceID: "pending-smoke-restore", ScopeKind: "migration",
		ScopeID: report.MigrationID, SourceDatabaseOID: pendingSmokeSourceIdentity.OID,
		SourceDatabaseName: pendingSmokeSourceIdentity.Name, SourceSystemIdentifier: pendingSmokeSourceIdentity.SystemIdentifier,
		SourceIdentityEpoch: 1, TargetDatabaseOID: pendingSmokeTargetIdentity.OID,
		TargetDatabaseName: pendingSmokeTargetIdentity.Name, TargetSystemIdentifier: pendingSmokeTargetIdentity.SystemIdentifier,
		SourceResourceID:   "same-cluster/source/" + pendingSmokeSourceIdentity.Name,
		TargetResourceID:   "same-cluster/restore/" + pendingSmokeTargetIdentity.Name,
		RestoreIncarnation: "pending-smoke-restore", ArtifactSHA256: strings.Repeat("6", 64),
		OwnerRoleName: os.Getenv("PG_TEST_OWNER_ROLE"), TargetRoleName: targetRole,
		ObservedAt: pendingSmokeNow, ExpiresAt: pendingSmokeNow.Add(time.Hour)}
	pendingSmokeRebindConfirmation, err := pendingSmokeEngine.ConfirmRebindRestore(ctx, pendingSmokeEvidence, pendingSmokeTarget)
	require.NoError(t, err)
	_, err = pendingSmokeEngine.RebindRestore(ctx, pendingSmokeEvidence, pendingSmokeRebindConfirmation.ConfirmationToken, pendingSmokeTarget)
	require.NoError(t, err)
	pendingSmokeConfirmation, err := pendingSmokeEngine.ConfirmSmoke(ctx, report.MigrationID, pendingSmokeTarget)
	require.NoError(t, err)
	_, err = pendingSmokeEngine.Smoke(ctx, report.MigrationID, pendingSmokeConfirmation.ConfirmationToken, pendingSmokeTarget)
	require.NoError(t, err)
	pendingSmokeStatus, err := pendingSmokeEngine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", pendingSmokeStatus.State)
	pendingSmokeEngine.Close()
	_, err = adminRoot.Exec(ctx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, pendingSmokeRestoreName)
	require.NoError(t, err)
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`drop database %s`, qPendingSmokeRestore))
	require.NoError(t, err)
	require.NoError(t, adminRoot.Close(ctx))
	pool, err = pgxpool.New(ctx, connectionString)
	require.NoError(t, err)
	defer pool.Close()
	engine.Pool = pool

	adminTestConnection, err := pgx.Connect(ctx, adminConnectionString)
	require.NoError(t, err)
	defer adminTestConnection.Close(ctx)
	var recordedOwner, edgeOwner string
	require.NoError(t, pool.QueryRow(ctx, `select owner_role_name from public.dawgs_schema_layout where singleton`).Scan(&recordedOwner))
	require.NoError(t, pool.QueryRow(ctx, `select pg_catalog.pg_get_userbyid(relowner) from pg_catalog.pg_class where oid='public.edge'::pg_catalog.regclass`).Scan(&edgeOwner))
	require.Equal(t, os.Getenv("PG_TEST_OWNER_ROLE"), recordedOwner)
	require.Equal(t, recordedOwner, edgeOwner)
	var cleanupApplicable bool
	var cleanupEntries int64
	var cleanupDigest, cleanupName, cleanupFingerprint string
	require.NoError(t, pool.QueryRow(ctx, `select c.applicable,c.map_digest,count(m.object_order),
coalesce(min(m.object_name),''),coalesce(min(m.object_fingerprint),'')
from public.dawgs_legacy_cleanup_scope c left join public.dawgs_legacy_cleanup_map m
using(scope_kind,scope_id) where c.scope_kind='migration' and c.scope_id=$1
group by c.applicable,c.map_digest`, report.MigrationID).Scan(
		&cleanupApplicable, &cleanupDigest, &cleanupEntries, &cleanupName, &cleanupFingerprint))
	require.True(t, cleanupApplicable)
	require.Len(t, cleanupDigest, 64)
	require.EqualValues(t, 1, cleanupEntries)
	require.Equal(t, "edge_1_v1_legacy", cleanupName)
	require.Len(t, cleanupFingerprint, 64)

	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`revoke insert on table public.node from %s`, pgx.Identifier{targetRole}.Sanitize()))
	require.NoError(t, err)
	failedSmokeConfirmation, err := engine.ConfirmSmoke(ctx, report.MigrationID, targetConnectionString)
	require.NoError(t, err)
	failedSmoke, err := engine.Smoke(ctx, report.MigrationID, failedSmokeConfirmation.ConfirmationToken, targetConnectionString)
	require.ErrorContains(t, err, "target smoke node insert")
	require.Equal(t, "v2_pending_smoke", failedSmoke.State)
	require.NotEmpty(t, failedSmoke.ReceiptDigest)

	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`grant insert on table public.node to %s`, pgx.Identifier{targetRole}.Sanitize()))
	require.NoError(t, err)
	smokeConfirmation, err := engine.ConfirmSmoke(ctx, report.MigrationID, targetConnectionString)
	require.NoError(t, err)
	require.NotEmpty(t, smokeConfirmation.ConfirmationToken)
	smoke, err := engine.Smoke(ctx, report.MigrationID, smokeConfirmation.ConfirmationToken, targetConnectionString)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", smoke.State)
	require.NotEmpty(t, smoke.ReceiptDigest)

	status, err := engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`revoke execute on function public.dawgs_provision_edge_kind_leaf(integer,smallint) from %s`, pgx.Identifier{targetRole}.Sanitize()))
	require.NoError(t, err)
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "drifted", status.State)
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`grant execute on function public.dawgs_provision_edge_kind_leaf(integer,smallint) to %s`, pgx.Identifier{targetRole}.Sanitize()))
	require.NoError(t, err)
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)
	targetGuardConnection, err := pgx.Connect(ctx, targetConnectionString)
	require.NoError(t, err)
	_, err = targetGuardConnection.Exec(ctx, `delete from public.graph where id=1`)
	require.Error(t, err)
	require.NoError(t, targetGuardConnection.Close(ctx))
	_, err = adminTestConnection.Exec(ctx, `delete from public.graph where id=1`)
	require.ErrorContains(t, err, "managed graph rows cannot be deleted")
	var graphPresent bool
	require.NoError(t, pool.QueryRow(ctx, `select exists(select 1 from public.graph where id=1)`).Scan(&graphPresent))
	require.True(t, graphPresent)

	operatorDriver := pgdriver.NewDriver(0, pool)
	err = operatorDriver.AssertRuntime(ctx)
	require.ErrorIs(t, err, pgdriver.ErrWrongRuntimeRole)
	targetPool, err := pgxpool.New(ctx, targetConnectionString)
	require.NoError(t, err)
	defer targetPool.Close()
	targetDriver := pgdriver.NewDriver(0, targetPool)
	require.ErrorIs(t, targetDriver.AssertRuntime(ctx), pgdriver.ErrActivationNotReady)
	require.ErrorIs(t, targetDriver.ReadTransaction(ctx, func(graph.Transaction) error { return nil }), pgdriver.ErrStartupValidation)

	var unregisteredKindID int16
	require.NoError(t, adminTestConnection.QueryRow(ctx, `insert into public.kind(name) values('UNREGISTERED_KIND') returning id`).Scan(&unregisteredKindID))
	leafName := pgmodel.EdgeKindPartitionTableName(1, unregisteredKindID)
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`create table %s partition of public.edge_1 for values in (%d)`,
		pgx.Identifier{"public", leafName}.Sanitize(), unregisteredKindID))
	require.NoError(t, err)
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "drifted", status.State)
	require.ErrorIs(t, targetDriver.AssertRuntime(ctx), pgdriver.ErrLayoutDrift)
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`alter table public.edge_1 detach partition %s`, pgx.Identifier{"public", leafName}.Sanitize()))
	require.NoError(t, err)
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`drop table %s`, pgx.Identifier{"public", leafName}.Sanitize()))
	require.NoError(t, err)
	_, err = adminTestConnection.Exec(ctx, `delete from public.kind where id=$1`, unregisteredKindID)
	require.NoError(t, err)
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)

	_, err = pool.Exec(ctx, `update public.dawgs_schema_layout set current_smoke_receipt_digest=repeat('0',64) where singleton`)
	require.NoError(t, err)
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "drifted", status.State)
	_, err = pool.Exec(ctx, `update public.dawgs_schema_layout set current_smoke_receipt_digest=$1 where singleton`, smoke.ReceiptDigest)
	require.NoError(t, err)
	require.ErrorIs(t, targetDriver.AssertRuntime(ctx), pgdriver.ErrActivationNotReady)
	require.ErrorIs(t, targetDriver.ReadTransaction(ctx, func(graph.Transaction) error { return nil }), pgdriver.ErrStartupValidation)

	cleanupNow := time.Now().UTC()
	acceptedAt := cleanupNow.Add(-4 * time.Hour)
	_, err = adminTestConnection.Exec(ctx, `update public.dawgs_schema_layout set accepted_at=$1 where singleton`, acceptedAt)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `select accepted_at from public.dawgs_schema_layout where singleton`).Scan(&acceptedAt))
	databaseIdentity, err = currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	engine.RequiredCleanupSoak = time.Hour
	cleanupEvidence := validCleanupEvidence(cleanupNow)
	cleanupEvidence.DatabaseOID = databaseIdentity.OID
	cleanupEvidence.DatabaseName = databaseIdentity.Name
	cleanupEvidence.SystemIdentifier = databaseIdentity.SystemIdentifier
	cleanupEvidence.ScopeID = report.MigrationID
	cleanupEvidence.CleanupMapSHA256 = cleanupDigest
	cleanupEvidence.SmokeReceiptSHA256 = smoke.ReceiptDigest
	cleanupEvidence.AcceptedAt = acceptedAt
	cleanupEvidence.RequiredSoakSeconds = int64(time.Hour / time.Second)
	cleanupEvidence.BackupCompletedAt = cleanupNow.Add(-2 * time.Hour)
	cleanupEvidence.RestoreTestedAt = cleanupNow.Add(-90 * time.Minute)
	cleanupEvidence.ApprovedAt = cleanupNow.Add(-time.Hour)
	cleanupEvidence.ExpiresAt = cleanupNow.Add(time.Hour)
	engine.CleanupEvidence = &cleanupEvidence

	cleanupConfirmation, err := engine.ConfirmCleanup(ctx, report.MigrationID)
	require.NoError(t, err)
	legacyOriginal := pgx.Identifier{"public", "edge_1_v1_legacy_original"}.Sanitize()
	legacyRecorded := pgx.Identifier{"public", cleanupName}.Sanitize()
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`alter table %s rename to edge_1_v1_legacy_original; create table %s(id bigint)`, legacyRecorded, legacyRecorded))
	require.NoError(t, err)
	_, err = engine.Cleanup(ctx, report.MigrationID, cleanupConfirmation.ConfirmationToken)
	require.ErrorContains(t, err, "OID/name/relkind")
	_, err = adminTestConnection.Exec(ctx, fmt.Sprintf(`drop table %s; alter table %s rename to edge_1_v1_legacy`, legacyRecorded, legacyOriginal))
	require.NoError(t, err)

	cleanupConfirmation, err = engine.ConfirmCleanup(ctx, report.MigrationID)
	require.NoError(t, err)
	engine.Failpoint = func(name string) error {
		if name == "before_cleanup_commit" {
			return context.Canceled
		}
		return nil
	}
	_, err = engine.Cleanup(ctx, report.MigrationID, cleanupConfirmation.ConfirmationToken)
	require.ErrorIs(t, err, context.Canceled)
	var legacyStillPresent bool
	require.NoError(t, pool.QueryRow(ctx, `select pg_catalog.to_regclass('public.edge_1_v1_legacy') is not null`).Scan(&legacyStillPresent))
	require.True(t, legacyStillPresent)
	engine.Failpoint = nil
	cleanupReport, err := engine.Cleanup(ctx, report.MigrationID, cleanupConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", cleanupReport.State)
	require.NotEmpty(t, cleanupReport.ReceiptDigest)
	require.NoError(t, pool.QueryRow(ctx, `select pg_catalog.to_regclass('public.edge_1_v1_legacy') is not null`).Scan(&legacyStillPresent))
	require.False(t, legacyStillPresent)
	var cleanupStatus, currentCleanupDigest, currentCleanupReceipt string
	var immutableEntries, completionCount int64
	require.NoError(t, pool.QueryRow(ctx, `select legacy_cleanup_status,current_legacy_cleanup_map_digest,current_cleanup_receipt_digest,
(select count(*) from public.dawgs_legacy_cleanup_map where scope_kind='migration' and scope_id=$1),
(select count(*) from public.dawgs_legacy_cleanup_completion where scope_kind='migration' and scope_id=$1)
from public.dawgs_schema_layout where singleton`, report.MigrationID).Scan(
		&cleanupStatus, &currentCleanupDigest, &currentCleanupReceipt, &immutableEntries, &completionCount))
	require.Equal(t, "complete", cleanupStatus)
	require.Len(t, currentCleanupDigest, 64)
	require.NotEqual(t, cleanupDigest, currentCleanupDigest)
	require.Equal(t, cleanupReport.ReceiptDigest, currentCleanupReceipt)
	require.EqualValues(t, 1, immutableEntries)
	require.EqualValues(t, 1, completionCount)
	_, err = adminTestConnection.Exec(ctx, `update public.dawgs_legacy_cleanup_map set object_name='forbidden' where scope_kind='migration' and scope_id=$1`, report.MigrationID)
	require.ErrorContains(t, err, "immutable")
	status, err = engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)

	// A restored post-cleanup database rebinds the canonical empty current map;
	// it must not attempt to rediscover or drop the immutable historical map.
	sourceIdentity := databaseIdentity
	targetPool.Close()
	require.NoError(t, adminTestConnection.Close(ctx))
	pool.Close()
	restoredDatabaseName := sourceIdentity.Name + "_cleaned"
	adminRootConnection := connectionForDatabase(t, adminConnectionString, "postgres")
	adminRootHandle, err := pgx.Connect(ctx, adminRootConnection)
	require.NoError(t, err)
	quotedRestoredDatabase := pgx.Identifier{restoredDatabaseName}.Sanitize()
	quotedSourceDatabase := pgx.Identifier{sourceIdentity.Name}.Sanitize()
	quotedOwner := pgx.Identifier{os.Getenv("PG_TEST_OWNER_ROLE")}.Sanitize()
	quotedOperator := pgx.Identifier{os.Getenv("PG_TEST_OPERATOR_ROLE")}.Sanitize()
	quotedTarget := pgx.Identifier{targetRole}.Sanitize()
	_, err = adminRootHandle.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`,
		quotedRestoredDatabase, quotedSourceDatabase, quotedOwner))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupHandle, connectErr := pgx.Connect(cleanupCtx, adminRootConnection)
		if connectErr != nil {
			return
		}
		defer cleanupHandle.Close(cleanupCtx)
		_, _ = cleanupHandle.Exec(cleanupCtx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, restoredDatabaseName)
		_, _ = cleanupHandle.Exec(cleanupCtx, fmt.Sprintf(`drop database if exists %s`, quotedRestoredDatabase))
	})
	_, err = adminRootHandle.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect on database %s to %s,%s`, quotedRestoredDatabase, quotedRestoredDatabase, quotedOperator, quotedTarget))
	require.NoError(t, err)
	require.NoError(t, adminRootHandle.Close(ctx))

	operatorRestore := connectionForDatabase(t, connectionString, restoredDatabaseName)
	targetRestore := connectionForDatabase(t, targetConnectionString, restoredDatabaseName)
	adminRestore := connectionForDatabase(t, adminConnectionString, restoredDatabaseName)
	restoredEngine, err := New(ctx, operatorRestore)
	require.NoError(t, err)
	defer restoredEngine.Close()
	restoredEngine.AllowUnreleasedDisposable = true
	restoredEngine.RetirementConnectionString = adminRestore
	restoredIdentity, err := currentDatabaseIdentity(ctx, restoredEngine.Pool)
	require.NoError(t, err)
	restoreNow := time.Now().UTC()
	restoreEvidence := RestoreEvidence{Version: 1, EvidenceID: "post-cleanup-restore", ScopeKind: "migration",
		ScopeID: report.MigrationID, SourceDatabaseOID: sourceIdentity.OID, SourceDatabaseName: sourceIdentity.Name,
		SourceSystemIdentifier: sourceIdentity.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: restoredIdentity.OID, TargetDatabaseName: restoredIdentity.Name,
		TargetSystemIdentifier: restoredIdentity.SystemIdentifier,
		SourceResourceID:       "disposable/source/" + sourceIdentity.Name,
		TargetResourceID:       "disposable/restore/" + restoredIdentity.Name,
		RestoreIncarnation:     "post-cleanup-" + restoredIdentity.Name, ArtifactSHA256: strings.Repeat("f", 64),
		OwnerRoleName: os.Getenv("PG_TEST_OWNER_ROLE"), TargetRoleName: targetRole,
		ObservedAt: restoreNow, ExpiresAt: restoreNow.Add(time.Hour)}
	rebindConfirmation, err := restoredEngine.ConfirmRebindRestore(ctx, restoreEvidence, targetRestore)
	require.NoError(t, err)
	_, err = restoredEngine.RebindRestore(ctx, restoreEvidence, rebindConfirmation.ConfirmationToken, targetRestore)
	require.NoError(t, err)
	var postCleanupRebindObjects int64
	require.NoError(t, restoredEngine.Pool.QueryRow(ctx, `select count(*) from public.dawgs_restore_rebind_map where identity_epoch=2`).Scan(&postCleanupRebindObjects))
	require.Zero(t, postCleanupRebindObjects)
	restoredSmokeConfirmation, err := restoredEngine.ConfirmSmoke(ctx, report.MigrationID, targetRestore)
	require.NoError(t, err)
	_, err = restoredEngine.Smoke(ctx, report.MigrationID, restoredSmokeConfirmation.ConfirmationToken, targetRestore)
	require.NoError(t, err)
	restoredStatus, err := restoredEngine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", restoredStatus.State)
	var restoredCleanupStatus, restoredCleanupDigest string
	require.NoError(t, restoredEngine.Pool.QueryRow(ctx, `select legacy_cleanup_status,current_legacy_cleanup_map_digest
from public.dawgs_schema_layout where singleton`).Scan(&restoredCleanupStatus, &restoredCleanupDigest))
	require.Equal(t, "complete", restoredCleanupStatus)
	require.Equal(t, currentCleanupDigest, restoredCleanupDigest)
}
