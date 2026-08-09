//go:build restore_integration

package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgdriver "github.com/specterops/dawgs/drivers/pg"
	"github.com/stretchr/testify/require"
)

func TestAcceptedBootstrapRestoreRequiresIdentityRebindAndFreshSmoke(t *testing.T) {
	if os.Getenv("DAWGS_TEST_POSTGRES_DISPOSABLE") != "1" {
		t.Skip("restore integration requires the disposable role-aware harness")
	}
	operatorSource := os.Getenv("CONNECTION_STRING")
	targetSource := os.Getenv("PG_TEST_TARGET_CONNECTION_STRING")
	adminSource := os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING")
	sourceDatabaseName := os.Getenv("PG_TEST_DATABASE_NAME")
	ownerRole := os.Getenv("PG_TEST_OWNER_ROLE")
	targetRole := os.Getenv("PG_TEST_TARGET_ROLE")
	require.NotEmpty(t, operatorSource)
	require.NotEmpty(t, targetSource)
	require.NotEmpty(t, adminSource)
	require.NotEmpty(t, sourceDatabaseName)
	require.NotEmpty(t, ownerRole)
	require.NotEmpty(t, targetRole)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	source, err := New(ctx, operatorSource)
	require.NoError(t, err)
	source.AllowUnreleasedDisposable = true
	source.RetirementConnectionString = adminSource
	installation, err := source.Bootstrap(ctx)
	require.NoError(t, err)
	handoff, err := source.ConfirmBootstrap(ctx, installation.MigrationID, targetSource)
	require.NoError(t, err)
	_, err = source.CompleteBootstrap(ctx, installation.MigrationID, handoff.ConfirmationToken, targetSource)
	require.NoError(t, err)
	sourceIdentity, err := currentDatabaseIdentity(ctx, source.Pool)
	require.NoError(t, err)
	source.Close()

	restoredDatabaseName := sourceDatabaseName + "_restore"
	adminRoot := connectionForDatabase(t, adminSource, "postgres")
	root, err := pgx.Connect(ctx, adminRoot)
	require.NoError(t, err)
	quotedRestore := pgx.Identifier{restoredDatabaseName}.Sanitize()
	quotedSource := pgx.Identifier{sourceDatabaseName}.Sanitize()
	quotedOwner := pgx.Identifier{ownerRole}.Sanitize()
	quotedTarget := pgx.Identifier{targetRole}.Sanitize()
	quotedOperator := pgx.Identifier{os.Getenv("PG_TEST_OPERATOR_ROLE")}.Sanitize()
	_, err = root.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`, quotedRestore, quotedSource, quotedOwner))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, connectErr := pgx.Connect(cleanupCtx, adminRoot)
		if connectErr != nil {
			return
		}
		defer cleanup.Close(cleanupCtx)
		_, _ = cleanup.Exec(cleanupCtx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, restoredDatabaseName)
		_, _ = cleanup.Exec(cleanupCtx, fmt.Sprintf(`drop database if exists %s`, quotedRestore))
	})
	_, err = root.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public; grant connect on database %s to %s,%s`,
		quotedRestore, quotedRestore, quotedOperator, quotedTarget))
	require.NoError(t, err)
	require.NoError(t, root.Close(ctx))

	operatorRestore := connectionForDatabase(t, operatorSource, restoredDatabaseName)
	targetRestore := connectionForDatabase(t, targetSource, restoredDatabaseName)
	adminRestore := connectionForDatabase(t, adminSource, restoredDatabaseName)
	restored, err := New(ctx, operatorRestore)
	require.NoError(t, err)
	defer restored.Close()
	restored.RetirementConnectionString = adminRestore
	restoredIdentity, err := currentDatabaseIdentity(ctx, restored.Pool)
	require.NoError(t, err)
	require.NotEqual(t, sourceIdentity.OID, restoredIdentity.OID)

	evidenceTime := time.Now().UTC()
	evidence := RestoreEvidence{
		Version: 1, EvidenceID: "bootstrap-clone-restore", ScopeKind: "bootstrap", ScopeID: installation.MigrationID,
		SourceDatabaseOID: sourceIdentity.OID, SourceDatabaseName: sourceIdentity.Name,
		SourceSystemIdentifier: sourceIdentity.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: restoredIdentity.OID, TargetDatabaseName: restoredIdentity.Name,
		TargetSystemIdentifier: restoredIdentity.SystemIdentifier,
		SourceResourceID:       "disposable/source/" + sourceIdentity.Name,
		TargetResourceID:       "disposable/restore/" + restoredIdentity.Name,
		RestoreIncarnation:     "clone-" + restoredIdentity.Name, ArtifactSHA256: strings.Repeat("b", 64),
		OwnerRoleName: ownerRole, TargetRoleName: targetRole,
		ObservedAt: evidenceTime, ExpiresAt: evidenceTime.Add(time.Hour),
	}
	confirmation, err := restored.ConfirmRebindRestore(ctx, evidence, targetRestore)
	require.NoError(t, err)
	_, err = restored.RebindRestore(ctx, evidence, confirmation.ConfirmationToken, targetRestore)
	require.ErrorContains(t, err, "activation_not_ready")
	restored.AllowUnreleasedDisposable = true
	rebind, err := restored.RebindRestore(ctx, evidence, confirmation.ConfirmationToken, targetRestore)
	require.NoError(t, err)
	require.Equal(t, "v2_restore_pending_smoke", rebind.State)

	var state, smokeStatus string
	var epoch int64
	var reboundScopes, reboundObjects, liveConfirmations int64
	require.NoError(t, restored.Pool.QueryRow(ctx, `select state,identity_epoch,post_acceptance_smoke_status,
(select count(*) from public.dawgs_restore_rebind_scope where identity_epoch=2),
(select count(*) from public.dawgs_restore_rebind_map where identity_epoch=2),
(select count(*) from public.dawgs_operation_confirmation where consumed_at is null)
from public.dawgs_schema_layout where singleton`).Scan(&state, &epoch, &smokeStatus, &reboundScopes, &reboundObjects, &liveConfirmations))
	require.Equal(t, "v2_restore_pending_smoke", state)
	require.EqualValues(t, 2, epoch)
	require.Equal(t, "pending", smokeStatus)
	require.EqualValues(t, 1, reboundScopes)
	require.Zero(t, reboundObjects)
	require.Zero(t, liveConfirmations)

	smokeConfirmation, err := restored.ConfirmSmoke(ctx, installation.MigrationID, targetRestore)
	require.NoError(t, err)
	smoke, err := restored.Smoke(ctx, installation.MigrationID, smokeConfirmation.ConfirmationToken, targetRestore)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", smoke.State)
	status, err := restored.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)

	targetConnection, err := pgxpool.New(ctx, targetRestore)
	require.NoError(t, err)
	defer targetConnection.Close()
	driver := pgdriver.NewDriver(0, targetConnection)
	health, readinessErr := driver.Readiness(ctx)
	require.ErrorIs(t, readinessErr, pgdriver.ErrActivationNotReady)
	require.EqualValues(t, 2, health.IdentityEpoch)
}

func TestSameClusterPendingBootstrapRestoreRepeatsTargetHandoff(t *testing.T) {
	if os.Getenv("DAWGS_TEST_POSTGRES_DISPOSABLE") != "1" {
		t.Skip("restore integration requires the disposable role-aware harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adminSource := os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING")
	operatorSource := os.Getenv("CONNECTION_STRING")
	targetSource := os.Getenv("PG_TEST_TARGET_CONNECTION_STRING")
	ownerRole := os.Getenv("PG_TEST_OWNER_ROLE")
	operatorRole := os.Getenv("PG_TEST_OPERATOR_ROLE")
	targetRole := os.Getenv("PG_TEST_TARGET_ROLE")
	nonce, err := randomID()
	require.NoError(t, err)
	pendingDatabase := "dawgs_pending_" + nonce[:10]
	restoredDatabase := pendingDatabase + "_restore"
	adminRoot := connectionForDatabase(t, adminSource, "postgres")
	root, err := pgx.Connect(ctx, adminRoot)
	require.NoError(t, err)
	defer root.Close(ctx)
	qPending := pgx.Identifier{pendingDatabase}.Sanitize()
	qRestored := pgx.Identifier{restoredDatabase}.Sanitize()
	qOwner := pgx.Identifier{ownerRole}.Sanitize()
	qOperator := pgx.Identifier{operatorRole}.Sanitize()
	qTarget := pgx.Identifier{targetRole}.Sanitize()
	_, err = root.Exec(ctx, fmt.Sprintf(`create database %s owner %s`, qPending, qOwner))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, connectErr := pgx.Connect(cleanupCtx, adminRoot)
		if connectErr != nil {
			return
		}
		defer cleanup.Close(cleanupCtx)
		for _, database := range []string{restoredDatabase, pendingDatabase} {
			_, _ = cleanup.Exec(cleanupCtx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, database)
			_, _ = cleanup.Exec(cleanupCtx, fmt.Sprintf(`drop database if exists %s`, pgx.Identifier{database}.Sanitize()))
		}
	})
	_, err = root.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect,create,temp on database %s to %s; grant connect on database %s to %s`,
		qPending, qPending, qOperator, qPending, qTarget))
	require.NoError(t, err)
	pendingAdmin := connectionForDatabase(t, adminSource, pendingDatabase)
	pendingAdminHandle, err := pgx.Connect(ctx, pendingAdmin)
	require.NoError(t, err)
	_, err = pendingAdminHandle.Exec(ctx, fmt.Sprintf(`revoke create on schema public from public; grant usage,create on schema public to %s`, qOperator))
	require.NoError(t, err)
	require.NoError(t, pendingAdminHandle.Close(ctx))
	pendingOperator := connectionForDatabase(t, operatorSource, pendingDatabase)
	pendingEngine, err := New(ctx, pendingOperator)
	require.NoError(t, err)
	pendingEngine.AllowUnreleasedDisposable = true
	installation, err := pendingEngine.Bootstrap(ctx)
	require.NoError(t, err)
	sourceIdentity, err := currentDatabaseIdentity(ctx, pendingEngine.Pool)
	require.NoError(t, err)
	pendingEngine.Close()
	_, err = root.Exec(ctx, fmt.Sprintf(`create database %s with template %s owner %s`, qRestored, qPending, qOwner))
	require.NoError(t, err)
	_, err = root.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect,create,temp on database %s to %s; grant connect on database %s to %s`,
		qRestored, qRestored, qOperator, qRestored, qTarget))
	require.NoError(t, err)

	restoredOperator := connectionForDatabase(t, operatorSource, restoredDatabase)
	restoredTarget := connectionForDatabase(t, targetSource, restoredDatabase)
	restoredAdmin := connectionForDatabase(t, adminSource, restoredDatabase)
	restoredEngine, err := New(ctx, restoredOperator)
	require.NoError(t, err)
	defer restoredEngine.Close()
	restoredEngine.AllowUnreleasedDisposable = true
	restoredEngine.RetirementConnectionString = restoredAdmin
	targetIdentity, err := currentDatabaseIdentity(ctx, restoredEngine.Pool)
	require.NoError(t, err)
	now := time.Now().UTC()
	evidence := RestoreEvidence{Version: 1, EvidenceID: "pending-bootstrap-restore", ScopeKind: "bootstrap",
		ScopeID: installation.MigrationID, SourceDatabaseOID: sourceIdentity.OID, SourceDatabaseName: sourceIdentity.Name,
		SourceSystemIdentifier: sourceIdentity.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: targetIdentity.OID, TargetDatabaseName: targetIdentity.Name,
		TargetSystemIdentifier: targetIdentity.SystemIdentifier,
		SourceResourceID:       "same-cluster/source/" + pendingDatabase,
		TargetResourceID:       "same-cluster/restore/" + restoredDatabase,
		RestoreIncarnation:     "pending-bootstrap-" + nonce[:10], ArtifactSHA256: strings.Repeat("7", 64),
		OwnerRoleName: ownerRole, TargetRoleName: targetRole, ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	rebindConfirmation, err := restoredEngine.ConfirmRebindRestore(ctx, evidence, restoredTarget)
	require.NoError(t, err)
	rebind, err := restoredEngine.RebindRestore(ctx, evidence, rebindConfirmation.ConfirmationToken, restoredTarget)
	require.NoError(t, err)
	require.Equal(t, "v2_pending_handoff", rebind.State)
	handoff, err := restoredEngine.ConfirmBootstrap(ctx, installation.MigrationID, restoredTarget)
	require.NoError(t, err)
	accepted, err := restoredEngine.CompleteBootstrap(ctx, installation.MigrationID, handoff.ConfirmationToken, restoredTarget)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", accepted.State)
	var epoch int64
	require.NoError(t, restoredEngine.Pool.QueryRow(ctx, `select identity_epoch from public.dawgs_schema_layout where singleton`).Scan(&epoch))
	require.EqualValues(t, 2, epoch)
}
