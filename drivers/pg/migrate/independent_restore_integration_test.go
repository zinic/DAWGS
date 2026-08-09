//go:build independent_restore_integration

package migrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type independentRestoreCluster struct {
	adminURL, database, owner, operator, target string
}

func provisionIndependentRestoreCluster(t *testing.T, ctx context.Context, adminURL, database,
	owner, operator, target, password string) independentRestoreCluster {
	t.Helper()
	admin, err := pgx.Connect(ctx, connectionForDatabase(t, adminURL, "postgres"))
	require.NoError(t, err)
	defer admin.Close(ctx)
	qDatabase := pgx.Identifier{database}.Sanitize()
	qOwner := pgx.Identifier{owner}.Sanitize()
	qOperator := pgx.Identifier{operator}.Sanitize()
	qTarget := pgx.Identifier{target}.Sanitize()
	_, err = admin.Exec(ctx, fmt.Sprintf(`create role %s nologin nosuperuser nocreatedb nocreaterole noinherit;
create role %s login password %s nosuperuser nocreatedb nocreaterole noinherit;
	create role %s login password %s nosuperuser nocreatedb nocreaterole noinherit`,
		qOwner, qOperator, quoteLiteral(password), qTarget, quoteLiteral(password)))
	require.NoError(t, err)
	_, err = admin.Exec(ctx, fmt.Sprintf(`create database %s owner %s`, qDatabase, qOwner))
	require.NoError(t, err)
	_, err = admin.Exec(ctx, fmt.Sprintf(`revoke all on database %s from public;
grant connect,create,temp on database %s to %s;
grant connect on database %s to %s`, qDatabase, qDatabase, qOperator, qDatabase, qTarget))
	require.NoError(t, err)
	databaseAdmin, err := pgx.Connect(ctx, connectionForDatabase(t, adminURL, database))
	require.NoError(t, err)
	_, err = databaseAdmin.Exec(ctx, fmt.Sprintf(`revoke create on schema public from public; grant usage,create on schema public to %s`, qOperator))
	require.NoError(t, err)
	require.NoError(t, databaseAdmin.Close(ctx))
	return independentRestoreCluster{adminURL, database, owner, operator, target}
}

func cleanupIndependentRestoreCluster(t *testing.T, cluster independentRestoreCluster) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, connectionForDatabase(t, cluster.adminURL, "postgres"))
	if err != nil {
		return
	}
	defer admin.Close(ctx)
	_, _ = admin.Exec(ctx, `select pg_catalog.pg_terminate_backend(pid) from pg_catalog.pg_stat_activity
where datname=$1 and pid<>pg_catalog.pg_backend_pid()`, cluster.database)
	_, _ = admin.Exec(ctx, fmt.Sprintf(`drop database if exists %s`, pgx.Identifier{cluster.database}.Sanitize()))
	for _, role := range []string{cluster.target, cluster.operator, cluster.owner} {
		_, _ = admin.Exec(ctx, fmt.Sprintf(`drop role if exists %s`, pgx.Identifier{role}.Sanitize()))
	}
}

func dumpRestoreDatabase(t *testing.T, ctx context.Context, sourceCoordinator, targetCoordinator, _ string) {
	t.Helper()
	mount := t.TempDir()
	archivePath := mount + "/accepted-v2.dump"
	dumpProgram := "pg_dump"
	dumpArguments := []string{"--format=custom", "--file", archivePath, sourceCoordinator}
	restoreProgram := "pg_restore"
	restoreArguments := []string{"--exit-on-error", "--dbname", targetCoordinator, archivePath}
	if image := os.Getenv("PG_TEST_POSTGRES_CLIENT_IMAGE"); image != "" {
		containerUser := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		dumpProgram = "docker"
		dumpArguments = []string{"run", "--rm", "--network", "host", "--user", containerUser, "--volume", mount + ":/work", image,
			"pg_dump", "--format=custom", "--file", "/work/accepted-v2.dump", sourceCoordinator}
		restoreProgram = "docker"
		restoreArguments = []string{"run", "--rm", "--network", "host", "--user", containerUser, "--volume", mount + ":/work", image,
			"pg_restore", "--exit-on-error", "--dbname", targetCoordinator, "/work/accepted-v2.dump"}
	}
	dumpOutput, err := exec.CommandContext(ctx, dumpProgram, dumpArguments...).CombinedOutput()
	require.NoErrorf(t, err, "pg_dump failed: %s", dumpOutput)
	restoreOutput, err := exec.CommandContext(ctx, restoreProgram, restoreArguments...).CombinedOutput()
	require.NoErrorf(t, err, "pg_restore failed: %s", restoreOutput)
}

func TestIndependentClusterAcceptedV2RestoreRebind(t *testing.T) {
	sourceAdmin := os.Getenv("PG_TEST_RESTORE_SOURCE_ADMIN")
	targetAdmin := os.Getenv("PG_TEST_RESTORE_TARGET_ADMIN")
	if sourceAdmin == "" || targetAdmin == "" {
		t.Skip("independent restore integration requires source and target cluster admin URLs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nonce, err := randomID()
	require.NoError(t, err)
	nonce = nonce[:12]
	owner := "dawgs_ir_" + nonce + "_owner"
	operator := "dawgs_ir_" + nonce + "_operator"
	target := "dawgs_ir_" + nonce + "_target"
	password := "p_" + nonce
	sourceDatabase := "dawgs_ir_" + nonce + "_source"
	targetDatabase := "dawgs_ir_" + nonce + "_targetdb"
	sourceCluster := provisionIndependentRestoreCluster(t, ctx, sourceAdmin, sourceDatabase, owner, operator, target, password)
	t.Cleanup(func() { cleanupIndependentRestoreCluster(t, sourceCluster) })
	targetCluster := provisionIndependentRestoreCluster(t, ctx, targetAdmin, targetDatabase, owner, operator, target, password)
	t.Cleanup(func() { cleanupIndependentRestoreCluster(t, targetCluster) })

	sourceOperator := connectionForRoleDatabase(t, sourceAdmin, operator, password, sourceDatabase)
	sourceTarget := connectionForRoleDatabase(t, sourceAdmin, target, password, sourceDatabase)
	sourceCoordinator := connectionForDatabase(t, sourceAdmin, sourceDatabase)
	sourceEngine, err := New(ctx, sourceOperator)
	require.NoError(t, err)
	sourceEngine.AllowUnreleasedDisposable = true
	sourceEngine.RetirementConnectionString = sourceCoordinator
	bootstrap, err := sourceEngine.Bootstrap(ctx)
	require.NoError(t, err)
	handoff, err := sourceEngine.ConfirmBootstrap(ctx, bootstrap.MigrationID, sourceTarget)
	require.NoError(t, err)
	_, err = sourceEngine.CompleteBootstrap(ctx, bootstrap.MigrationID, handoff.ConfirmationToken, sourceTarget)
	require.NoError(t, err)
	sourceIdentity, err := currentDatabaseIdentity(ctx, sourceEngine.Pool)
	require.NoError(t, err)
	sourceEngine.Close()

	targetCoordinator := connectionForDatabase(t, targetAdmin, targetDatabase)
	dumpRestoreDatabase(t, ctx, sourceCoordinator, targetCoordinator, owner)

	targetOperator := connectionForRoleDatabase(t, targetAdmin, operator, password, targetDatabase)
	targetRuntime := connectionForRoleDatabase(t, targetAdmin, target, password, targetDatabase)
	restoredEngine, err := New(ctx, targetOperator)
	require.NoError(t, err)
	defer restoredEngine.Close()
	restoredEngine.AllowUnreleasedDisposable = true
	restoredEngine.RetirementConnectionString = targetCoordinator
	restoredIdentity, err := currentDatabaseIdentity(ctx, restoredEngine.Pool)
	require.NoError(t, err)
	require.NotEqual(t, sourceIdentity.SystemIdentifier, restoredIdentity.SystemIdentifier)
	now := time.Now().UTC()
	evidence := RestoreEvidence{Version: 1, EvidenceID: "independent-cluster-restore", ScopeKind: "bootstrap",
		ScopeID: bootstrap.MigrationID, SourceDatabaseOID: sourceIdentity.OID, SourceDatabaseName: sourceIdentity.Name,
		SourceSystemIdentifier: sourceIdentity.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: restoredIdentity.OID, TargetDatabaseName: restoredIdentity.Name,
		TargetSystemIdentifier: restoredIdentity.SystemIdentifier,
		SourceResourceID:       "independent/source/" + sourceDatabase,
		TargetResourceID:       "independent/target/" + targetDatabase,
		RestoreIncarnation:     "independent-" + nonce, ArtifactSHA256: strings.Repeat("9", 64),
		OwnerRoleName: owner, TargetRoleName: target, ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	confirmation, err := restoredEngine.ConfirmRebindRestore(ctx, evidence, targetRuntime)
	require.NoError(t, err)
	_, err = restoredEngine.RebindRestore(ctx, evidence, confirmation.ConfirmationToken, targetRuntime)
	require.NoError(t, err)
	smokeConfirmation, err := restoredEngine.ConfirmSmoke(ctx, bootstrap.MigrationID, targetRuntime)
	require.NoError(t, err)
	_, err = restoredEngine.Smoke(ctx, bootstrap.MigrationID, smokeConfirmation.ConfirmationToken, targetRuntime)
	require.NoError(t, err)
	status, err := restoredEngine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)
}

func TestIndependentClusterPendingStateIsStableUnsupported(t *testing.T) {
	sourceAdmin := os.Getenv("PG_TEST_RESTORE_SOURCE_ADMIN")
	targetAdmin := os.Getenv("PG_TEST_RESTORE_TARGET_ADMIN")
	if sourceAdmin == "" || targetAdmin == "" {
		t.Skip("independent restore integration requires source and target cluster admin URLs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nonce, err := randomID()
	require.NoError(t, err)
	nonce = nonce[:12]
	owner := "dawgs_ip_" + nonce + "_owner"
	operator := "dawgs_ip_" + nonce + "_operator"
	target := "dawgs_ip_" + nonce + "_target"
	password := "p_" + nonce
	sourceDatabase := "dawgs_ip_" + nonce + "_source"
	targetDatabase := "dawgs_ip_" + nonce + "_targetdb"
	sourceCluster := provisionIndependentRestoreCluster(t, ctx, sourceAdmin, sourceDatabase, owner, operator, target, password)
	t.Cleanup(func() { cleanupIndependentRestoreCluster(t, sourceCluster) })
	targetCluster := provisionIndependentRestoreCluster(t, ctx, targetAdmin, targetDatabase, owner, operator, target, password)
	t.Cleanup(func() { cleanupIndependentRestoreCluster(t, targetCluster) })
	sourceOperator := connectionForRoleDatabase(t, sourceAdmin, operator, password, sourceDatabase)
	sourceCoordinator := connectionForDatabase(t, sourceAdmin, sourceDatabase)
	sourceEngine, err := New(ctx, sourceOperator)
	require.NoError(t, err)
	sourceEngine.AllowUnreleasedDisposable = true
	bootstrap, err := sourceEngine.Bootstrap(ctx)
	require.NoError(t, err)
	sourceIdentity, err := currentDatabaseIdentity(ctx, sourceEngine.Pool)
	require.NoError(t, err)
	sourceEngine.Close()
	targetCoordinator := connectionForDatabase(t, targetAdmin, targetDatabase)
	dumpRestoreDatabase(t, ctx, sourceCoordinator, targetCoordinator, owner)
	targetOperator := connectionForRoleDatabase(t, targetAdmin, operator, password, targetDatabase)
	targetRuntime := connectionForRoleDatabase(t, targetAdmin, target, password, targetDatabase)
	restoredEngine, err := New(ctx, targetOperator)
	require.NoError(t, err)
	defer restoredEngine.Close()
	restoredIdentity, err := currentDatabaseIdentity(ctx, restoredEngine.Pool)
	require.NoError(t, err)
	now := time.Now().UTC()
	evidence := RestoreEvidence{Version: 1, EvidenceID: "independent-pending-restore", ScopeKind: "bootstrap",
		ScopeID: bootstrap.MigrationID, SourceDatabaseOID: sourceIdentity.OID, SourceDatabaseName: sourceIdentity.Name,
		SourceSystemIdentifier: sourceIdentity.SystemIdentifier, SourceIdentityEpoch: 1,
		TargetDatabaseOID: restoredIdentity.OID, TargetDatabaseName: restoredIdentity.Name,
		TargetSystemIdentifier: restoredIdentity.SystemIdentifier,
		SourceResourceID:       "independent/source/" + sourceDatabase,
		TargetResourceID:       "independent/target/" + targetDatabase,
		RestoreIncarnation:     "independent-pending-" + nonce, ArtifactSHA256: strings.Repeat("4", 64),
		OwnerRoleName: owner, TargetRoleName: target, ObservedAt: now, ExpiresAt: now.Add(time.Hour)}
	_, err = restoredEngine.ConfirmRebindRestore(ctx, evidence, targetRuntime)
	require.ErrorContains(t, err, "rebind_restore_cross_cluster_unsupported_state")
}
