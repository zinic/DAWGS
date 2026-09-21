package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
	"github.com/specterops/dawgs/drivers/pg/internal/schema"
)

type bootstrapHandoffFacts struct {
	Version          int    `json:"version"`
	OperationID      string `json:"operation_id"`
	LayoutState      string `json:"layout_state"`
	DatabaseOID      uint32 `json:"database_oid"`
	DatabaseName     string `json:"database_name"`
	SystemIdentifier string `json:"system_identifier"`
	OperatorRoleOID  uint32 `json:"operator_role_oid"`
	OperatorRoleName string `json:"operator_role_name"`
	TargetRoleOID    uint32 `json:"target_role_oid"`
	TargetRoleName   string `json:"target_role_name"`
}

func (s *Engine) Bootstrap(ctx context.Context) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: unreleased bootstrap is restricted to the identity-guarded disposable test harness")
	}
	var serverVersion int
	if err := s.Pool.QueryRow(ctx, `select pg_catalog.current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		return Report{}, fmt.Errorf("reading PostgreSQL version for bootstrap: %w", err)
	}
	if err := validatePostgresVersion(serverVersion); err != nil {
		return Report{}, err
	}
	connection, err := s.Pool.Acquire(ctx)
	if err != nil {
		return Report{}, err
	}
	defer connection.Release()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	facts, err := layout.Classify(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if facts.State != layout.StateUninitialized {
		return Report{}, fmt.Errorf("bootstrap requires an uninitialized database, found %s", facts.State)
	}
	operator, err := readSessionIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if operator.SessionUser != operator.CurrentUser || operator.Superuser || operator.RoleOID == operator.DatabaseOwner || operator.HasMembership {
		return Report{}, fmt.Errorf("bootstrap operator must be directly authenticated, restricted, non-owner, and membership-free")
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, schema.Up()).ReadAll(); err != nil {
		return Report{}, fmt.Errorf("installing canonical pending bootstrap schema: %w", err)
	}
	var operationID string
	if err := tx.QueryRow(ctx, `select bootstrap_operation_id from public.dawgs_schema_layout
where singleton and state='v2_pending_handoff'`).Scan(&operationID); err != nil {
		return Report{}, err
	}
	installFacts := struct {
		Version          int    `json:"version"`
		OperationID      string `json:"operation_id"`
		State            string `json:"state"`
		OperatorRoleOID  uint32 `json:"operator_role_oid"`
		OperatorRoleName string `json:"operator_role_name"`
	}{1, operationID, "v2_pending_handoff", operator.RoleOID, operator.RoleName}
	receipt, err := appendReceipt(ctx, tx, "bootstrap", operationID, "bootstrap_installation", operationID, installFacts, "")
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "bootstrap", State: "v2_pending_handoff", MigrationID: operationID,
		ReceiptDigest: receipt.Digest, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) bootstrapHandoffFacts(ctx context.Context, queryer rowQuerier, operationID, targetConnectionString string, expectedOperator *sessionIdentity) (string, bootstrapHandoffFacts, databaseIdentity, error) {
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
	}
	defer targetConnection.Close(ctx)
	operatorIdentity := sessionIdentity{}
	if expectedOperator == nil {
		operatorIdentity, err = readSessionIdentity(ctx, queryer)
		if err != nil {
			return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
		}
	} else {
		operatorIdentity = *expectedOperator
	}
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
	}
	if err := validateTargetIdentity(target, operatorIdentity); err != nil {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
	}
	database, err := currentDatabaseIdentity(ctx, queryer)
	if err != nil {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || targetDatabase != database {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, fmt.Errorf("bootstrap target resolves a different database incarnation")
	}
	var state, storedOperation string
	if err := queryer.QueryRow(ctx, `select state,coalesce(bootstrap_operation_id,'') from public.dawgs_schema_layout where singleton`).Scan(&state, &storedOperation); err != nil {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, err
	}
	if state != "v2_pending_handoff" || storedOperation != operationID {
		return "", bootstrapHandoffFacts{}, databaseIdentity{}, fmt.Errorf("bootstrap handoff scope changed")
	}
	facts := bootstrapHandoffFacts{1, operationID, state, database.OID, database.Name,
		database.SystemIdentifier, operatorIdentity.RoleOID, operatorIdentity.RoleName, target.RoleOID, target.RoleName}
	digest, _, err := canonicalDigest(facts)
	return digest, facts, database, err
}

func (s *Engine) ConfirmBootstrap(ctx context.Context, operationID, targetConnectionString string) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	digest, facts, database, err := s.bootstrapHandoffFacts(ctx, tx, operationID, targetConnectionString, nil)
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, "bootstrap-handoff", operationID, digest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: facts.LayoutState, MigrationID: operationID,
		ConfirmationToken: token, TargetRole: facts.TargetRoleName, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) CompleteBootstrap(ctx context.Context, operationID, confirmation, targetConnectionString string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: unreleased bootstrap handoff is restricted to the identity-guarded disposable test harness")
	}
	operator, err := readSessionIdentity(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	expectedDatabase, err := currentDatabaseIdentity(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	coordinator, err := s.connectHandoffCoordinator(ctx, expectedDatabase)
	if err != nil {
		return Report{}, err
	}
	defer coordinator.Close(ctx)
	tx, err := coordinator.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	digest, facts, database, err := s.bootstrapHandoffFacts(ctx, tx, operationID, targetConnectionString, &operator)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "bootstrap-handoff", operationID, digest, database); err != nil {
		return Report{}, err
	}
	owner, err := databaseOwnerIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := transferManagedOwnership(ctx, tx, operator, owner, facts.TargetRoleName); err != nil {
		return Report{}, err
	}
	var identityEpoch int64
	if err := tx.QueryRow(ctx, `select identity_epoch from public.dawgs_schema_layout where singleton`).Scan(&identityEpoch); err != nil {
		return Report{}, err
	}
	cleanupMapDigest, err := sealCleanupMap(ctx, tx, "bootstrap", operationID, false, identityEpoch)
	if err != nil {
		return Report{}, fmt.Errorf("sealing bootstrap empty cleanup map: %w", err)
	}
	if err := grantTarget(ctx, tx, facts.TargetRoleName); err != nil {
		return Report{}, err
	}
	var graphCount int64
	if err := tx.QueryRow(ctx, `select count(*) from public.graph`).Scan(&graphCount); err != nil {
		return Report{}, err
	}
	if graphCount != 0 {
		return Report{}, fmt.Errorf("bootstrap empty-install fallback requires an empty graph catalog")
	}
	attemptID := confirmationAttemptID(confirmation)
	_, err = appendReceipt(ctx, tx, "bootstrap", operationID, "target_handoff", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	smokeFacts := struct {
		bootstrapHandoffFacts
		Result string `json:"result"`
		Reason string `json:"reason"`
	}{facts, "fallback", "canonical empty installation has no graph leaf for sentinel routing"}
	smokeReceipt, err := appendReceipt(ctx, tx, "bootstrap", operationID, "target_smoke", attemptID, smokeFacts, confirmation)
	if err != nil {
		return Report{}, err
	}
	tag, err := tx.Exec(ctx, `update public.dawgs_schema_layout set state='v2_accepted',
database_oid=$2::pg_catalog.oid,database_name=$3::text,system_identifier=$4::text,
database_incarnation=pg_catalog.md5($4::text||':'||$2::pg_catalog.oid::text||':'||$3::text),
owner_role_oid=$8::pg_catalog.oid,owner_role_name=$9::text,
current_legacy_cleanup_map_digest=$10::text,
target_role_oid=$5::pg_catalog.oid,target_role_name=$6::text,
accepted_scope_kind='bootstrap',accepted_scope_id=$1,accepted_migration_id=null,active_migration_id=null,
post_acceptance_smoke_status='fallback',current_smoke_receipt_digest=$7,accepted_at=pg_catalog.clock_timestamp()
where singleton and state='v2_pending_handoff' and bootstrap_operation_id=$1`, operationID, database.OID,
		database.Name, database.SystemIdentifier, facts.TargetRoleOID, facts.TargetRoleName, smokeReceipt.Digest,
		owner.OID, owner.Name, cleanupMapDigest)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("bootstrap handoff pointer changed concurrently")
	}
	if err := validateAcceptedCatalog(ctx, tx, owner.Name, facts.TargetRoleName); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "bootstrap-handoff", State: "v2_accepted", MigrationID: operationID,
		AttemptID: attemptID, ReceiptDigest: smokeReceipt.Digest, TargetRole: facts.TargetRoleName,
		CreatedAt: time.Now().UTC(), Blockers: []string{"activation_not_ready"}}, nil
}
