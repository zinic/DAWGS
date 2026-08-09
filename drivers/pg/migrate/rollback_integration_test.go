//go:build rollback_integration

package migrate

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/v1_schema.sql
var rollbackV1Schema string

func TestCutoverRollbackRestoresFrozenV1AndRequiresPreparationRoleHandoff(t *testing.T) {
	if os.Getenv("DAWGS_TEST_POSTGRES_DISPOSABLE") != "1" {
		t.Skip("rollback integration requires the disposable role-aware harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("CONNECTION_STRING"))
	require.NoError(t, err)
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = connection.Conn().PgConn().Exec(ctx, rollbackV1Schema+`
insert into public.graph(id,name) values(1,'rollback_test');
insert into public.kind(id,name) values(1,'ROLLS_BACK');
create table public.node_1 partition of public.node for values in (1);
create table public.edge_1 partition of public.edge for values in (1);
insert into public.edge_1(id,graph_id,start_id,end_id,kind_id,properties) values(1,1,10,11,1,'{}');`).ReadAll()
	connection.Release()
	require.NoError(t, err)
	writerRole := os.Getenv("PG_TEST_OLD_WRITER_ROLE")
	writerConnection := os.Getenv("PG_TEST_OLD_WRITER_CONNECTION_STRING")
	targetConnection := os.Getenv("PG_TEST_TARGET_CONNECTION_STRING")
	preparationRole := os.Getenv("PG_TEST_PREPARATION_ROLE")
	preparationConnection := os.Getenv("PG_TEST_PREPARATION_CONNECTION_STRING")
	preparationPassword := os.Getenv("PG_TEST_PREPARATION_PASSWORD")
	adminConnection := os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING")
	require.NotEmpty(t, writerRole)
	_, err = pool.Exec(ctx, fmt.Sprintf(`grant usage on schema public to %[1]s;
grant select,insert,update,delete,truncate on all tables in schema public to %[1]s;
grant usage,update on all sequences in schema public to %[1]s;
grant execute on all functions in schema public to %[1]s`, pgx.Identifier{writerRole}.Sanitize()))
	require.NoError(t, err)
	assertFrozenV1Export(t, ctx, writerConnection, true)
	v1CatalogBaseline, err := frozenV1StructuralCatalog(ctx, pool)
	require.NoError(t, err)
	manifest, err := DecodeManifest(strings.NewReader(`{"version":1,"installation_id":"rollback-v1","source_configuration":{"artifact":"drivers/pg/migrate/testdata/v1_schema.sql","sha256":"8bea76272e005639d34643f8311599440e1eccc8c3152ca4da1e6dda42c87e0f"},"owner_attestation":{"owner_id":"rollback-test","attested_at":"2026-08-01T00:00:00Z","statement_version":1},"graphs":[{"name":"rollback_test","edge_kinds":["ROLLS_BACK"]}],"excluded_unmaterialized_graphs":[]}`))
	require.NoError(t, err)
	database, err := currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	now := time.Now().UTC()
	engine := &Engine{Pool: pool, AllowUnreleasedDisposable: true, TargetConnectionString: targetConnection,
		PreparationRole: preparationRole, PreparationConnectionString: preparationConnection,
		RetirementConnectionString: adminConnection, CopyChunkSize: 1,
		WriterProbes: []WriterProbe{{Version: 1, ProbeID: "rollback-writer", RoleName: writerRole,
			ConnectionString: writerConnection, Retirement: "postgres_role_nologin"}},
		CapacityEvidence: &CapacityEvidence{Version: 1, EvidenceID: "rollback-capacity", DatabaseName: database.Name,
			SystemIdentifier: database.SystemIdentifier, ObservedAt: now, ExpiresAt: now.Add(time.Hour), AvailableBytes: 1 << 30, RequiredBytes: 1, EstimatedWALBytes: 1},
		BackupEvidence: &BackupEvidence{Version: 1, EvidenceID: "rollback-backup", DatabaseName: database.Name,
			SystemIdentifier: database.SystemIdentifier, CompletedAt: now.Add(-time.Minute), RestoreTestedAt: now,
			ExpiresAt: now.Add(time.Hour), ArtifactSHA256: strings.Repeat("b", 64)}}
	confirmation, err := engine.ConfirmMigration(ctx, manifest)
	require.NoError(t, err)
	fence, err := engine.Migrate(ctx, manifest, confirmation.ConfirmationToken)
	require.NoError(t, err)
	databaseFenceConfirmation, err := engine.ConfirmOperation(ctx, "verify-fence", fence.MigrationID)
	require.NoError(t, err)
	databaseFence, err := engine.VerifyFence(ctx, fence.MigrationID, databaseFenceConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "fence_database_verified_retirement_pending", databaseFence.State)
	retirementConfirmation, err := engine.ConfirmOperation(ctx, "verify-fence", fence.MigrationID)
	require.NoError(t, err)
	verifiedFence, err := engine.VerifyFence(ctx, fence.MigrationID, retirementConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "fence_verified", verifiedFence.State)
	assertFrozenV1Export(t, ctx, writerConnection, false)
	resumeConfirmation, err := engine.ConfirmOperation(ctx, "resume", fence.MigrationID)
	require.NoError(t, err)
	pending, err := engine.Resume(ctx, fence.MigrationID, resumeConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "v2_pending_acceptance", pending.State)
	rollbackConfirmation, err := engine.ConfirmOperation(ctx, "rollback", fence.MigrationID)
	require.NoError(t, err)
	engine.Failpoint = func(name string) error {
		if name == "after_rollback_drop_v2" {
			return fmt.Errorf("injected rollback before commit")
		}
		return nil
	}
	_, err = engine.Rollback(ctx, fence.MigrationID, rollbackConfirmation.ConfirmationToken)
	require.ErrorContains(t, err, "injected rollback before commit")
	afterAbortedRollback, err := engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "pending_validation", afterAbortedRollback.State)
	engine.Failpoint = func(name string) error {
		if name == "after_rollback_commit" {
			return fmt.Errorf("injected rollback client outcome loss")
		}
		return nil
	}
	_, err = engine.Rollback(ctx, fence.MigrationID, rollbackConfirmation.ConfirmationToken)
	require.ErrorContains(t, err, "injected rollback client outcome loss")
	engine.Failpoint = nil
	rolledBack, err := engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "rolled_back_handoff_pending", rolledBack.State)
	rollbackReceipt, err := engine.Receipt(ctx, fence.MigrationID, "rollback", "")
	require.NoError(t, err)
	require.NotEmpty(t, rollbackReceipt.ReceiptDigest)
	require.NoError(t, verifyWriterAuthenticationRetired(ctx, engine.WriterProbes[0]))
	var restoredRows int64
	require.NoError(t, pool.QueryRow(ctx, `select count(*) from public.edge_1`).Scan(&restoredRows))
	require.EqualValues(t, 1, restoredRows)
	var v1Helpers bool
	require.NoError(t, pool.QueryRow(ctx, `select
pg_catalog.to_regprocedure('public.start_node(public.edgeComposite)') is not null
and pg_catalog.to_regprocedure('public.end_node(public.edgeComposite)') is not null
and pg_catalog.to_regprocedure('public.start_node(integer,public.edgeComposite)') is null
		and pg_catalog.to_regprocedure('public.dawgs_provision_edge_kind_leaf(integer,smallint)') is null`).Scan(&v1Helpers))
	require.True(t, v1Helpers)
	assertFrozenV1Export(t, ctx, preparationConnection, false)

	// A same-cluster restore of rolled-back V1 must reset any copied handoff and
	// require a fresh preparation-role confirmation on the restored database.
	rolledSourceIdentity, err := currentDatabaseIdentity(ctx, pool)
	require.NoError(t, err)
	rolledRestoreName := rolledSourceIdentity.Name + "_rolled_restore"
	pool.Close()
	adminRootURL := connectionForDatabase(t, adminConnection, "postgres")
	adminRoot, err := pgx.Connect(ctx, adminRootURL)
	require.NoError(t, err)
	qRestore := pgx.Identifier{rolledRestoreName}.Sanitize()
	qSource := pgx.Identifier{rolledSourceIdentity.Name}.Sanitize()
	qOwner := pgx.Identifier{os.Getenv("PG_TEST_OWNER_ROLE")}.Sanitize()
	qOperator := pgx.Identifier{os.Getenv("PG_TEST_OPERATOR_ROLE")}.Sanitize()
	qTarget := pgx.Identifier{os.Getenv("PG_TEST_TARGET_ROLE")}.Sanitize()
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`, qRestore, qSource, qOwner))
	require.NoError(t, err)
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect,create,temp on database %s to %s; grant connect on database %s to %s,%s`,
		qRestore, qRestore, qOperator, qRestore, qTarget, pgx.Identifier{preparationRole}.Sanitize()))
	require.NoError(t, err)
	restoredOperator := connectionForDatabase(t, os.Getenv("CONNECTION_STRING"), rolledRestoreName)
	restoredTarget := connectionForDatabase(t, targetConnection, rolledRestoreName)
	restoredAdmin := connectionForDatabase(t, adminConnection, rolledRestoreName)
	restoredPreparation := connectionForDatabase(t, preparationConnection, rolledRestoreName)
	restoredEngine, err := New(ctx, restoredOperator)
	require.NoError(t, err)
	restoredEngine.AllowUnreleasedDisposable = true
	restoredEngine.RetirementConnectionString = restoredAdmin
	restoredEngine.PreparationRole = preparationRole
	restoredEngine.PreparationConnectionString = restoredPreparation
	restoredIdentity, err := currentDatabaseIdentity(ctx, restoredEngine.Pool)
	require.NoError(t, err)
	restoreNow := time.Now().UTC()
	restoreEvidence := RestoreEvidence{Version: 1, EvidenceID: "rolled-back-restore", ScopeKind: "migration",
		ScopeID: fence.MigrationID, SourceDatabaseOID: rolledSourceIdentity.OID,
		SourceDatabaseName: rolledSourceIdentity.Name, SourceSystemIdentifier: rolledSourceIdentity.SystemIdentifier,
		SourceIdentityEpoch: 1, TargetDatabaseOID: restoredIdentity.OID, TargetDatabaseName: restoredIdentity.Name,
		TargetSystemIdentifier: restoredIdentity.SystemIdentifier,
		SourceResourceID:       "same-cluster/source/" + rolledSourceIdentity.Name,
		TargetResourceID:       "same-cluster/restore/" + restoredIdentity.Name,
		RestoreIncarnation:     "rolled-back-restore", ArtifactSHA256: strings.Repeat("8", 64),
		OwnerRoleName: os.Getenv("PG_TEST_OWNER_ROLE"), TargetRoleName: os.Getenv("PG_TEST_TARGET_ROLE"),
		ObservedAt: restoreNow, ExpiresAt: restoreNow.Add(time.Hour)}
	rebindConfirmation, err := restoredEngine.ConfirmRebindRestore(ctx, restoreEvidence, restoredTarget)
	require.NoError(t, err)
	_, err = restoredEngine.RebindRestore(ctx, restoreEvidence, rebindConfirmation.ConfirmationToken, restoredTarget)
	require.NoError(t, err)
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`alter role %s login password %s`, pgx.Identifier{preparationRole}.Sanitize(), quoteLiteral(preparationPassword)))
	require.NoError(t, err)
	restoredHandoffConfirmation, err := restoredEngine.ConfirmOperation(ctx, "complete-v1-handoff", fence.MigrationID)
	require.NoError(t, err)
	restoredHandoff, err := restoredEngine.CompleteV1Handoff(ctx, fence.MigrationID, restoredHandoffConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "rolled_back", restoredHandoff.State)
	restoredPreparationConnection, err := pgx.Connect(ctx, restoredPreparation)
	require.NoError(t, err)
	_, err = restoredPreparationConnection.Exec(ctx, `insert into public.edge_1(id,graph_id,start_id,end_id,kind_id,properties) values(3,1,14,15,1,'{}')`)
	require.NoError(t, err)
	require.NoError(t, restoredPreparationConnection.Close(ctx))
	restoredEngine.Close()
	_, err = adminRoot.Exec(ctx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, rolledRestoreName)
	require.NoError(t, err)
	_, err = adminRoot.Exec(ctx, fmt.Sprintf(`drop database %s`, qRestore))
	require.NoError(t, err)
	require.NoError(t, adminRoot.Close(ctx))
	pool, err = pgxpool.New(ctx, os.Getenv("CONNECTION_STRING"))
	require.NoError(t, err)
	defer pool.Close()
	engine.Pool = pool

	admin, err := pgx.Connect(ctx, adminConnection)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, fmt.Sprintf(`alter role %s login password %s`, pgx.Identifier{preparationRole}.Sanitize(), quoteLiteral(preparationPassword)))
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))
	handoffConfirmation, err := engine.ConfirmOperation(ctx, "complete-v1-handoff", fence.MigrationID)
	require.NoError(t, err)
	handoff, err := engine.CompleteV1Handoff(ctx, fence.MigrationID, handoffConfirmation.ConfirmationToken)
	require.NoError(t, err)
	require.Equal(t, "rolled_back", handoff.State)
	assertFrozenV1Export(t, ctx, preparationConnection, true)
	assertFrozenV1HandoffCatalog(t, ctx, pool, v1CatalogBaseline, preparationRole)
	preparation, err := pgx.Connect(ctx, preparationConnection)
	require.NoError(t, err)
	_, err = preparation.Exec(ctx, `insert into public.edge_1(id,graph_id,start_id,end_id,kind_id,properties) values(2,1,12,13,1,'{}')`)
	require.NoError(t, err)
	require.NoError(t, preparation.Close(ctx))
	require.NoError(t, verifyWriterAuthenticationRetired(ctx, engine.WriterProbes[0]))
}

func assertFrozenV1Export(t *testing.T, ctx context.Context, connectionString string, succeeds bool) {
	t.Helper()
	binary := os.Getenv("DAWGS_FROZEN_V1_EXPORT")
	if binary == "" {
		t.Log("DAWGS_FROZEN_V1_EXPORT is unset; frozen built-client assertion is exercised in CI")
		return
	}
	output := t.TempDir() + "/graph.json"
	command := exec.CommandContext(ctx, binary, "rollback_test", output)
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PGCONN=") {
			environment = append(environment, value)
		}
	}
	command.Env = append(environment, "PGCONN="+connectionString)
	combined, err := command.CombinedOutput()
	if succeeds {
		require.NoError(t, err, string(combined))
		info, statErr := os.Stat(output)
		require.NoError(t, statErr)
		require.Positive(t, info.Size())
	} else {
		require.Error(t, err, string(combined))
	}
}
