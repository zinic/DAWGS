package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/specterops/dawgs/drivers/pg/model"
)

type sessionIdentity struct {
	RoleOID       uint32
	RoleName      string
	SessionUser   string
	CurrentUser   string
	Superuser     bool
	DatabaseOwner uint32
	HasMembership bool
}

func readSessionIdentity(ctx context.Context, queryer rowQuerier) (sessionIdentity, error) {
	var identity sessionIdentity
	err := queryer.QueryRow(ctx, `select r.oid,r.rolname,session_user::text,current_user::text,r.rolsuper,d.datdba,
exists(select 1 from pg_catalog.pg_auth_members m where m.member=r.oid)
from pg_catalog.pg_roles r cross join pg_catalog.pg_database d
where r.rolname=session_user and d.datname=pg_catalog.current_database()`).Scan(
		&identity.RoleOID, &identity.RoleName, &identity.SessionUser, &identity.CurrentUser,
		&identity.Superuser, &identity.DatabaseOwner, &identity.HasMembership)
	if err != nil {
		return sessionIdentity{}, fmt.Errorf("reading authenticated session identity: %w", err)
	}
	return identity, nil
}

func validateTargetIdentity(target, operator sessionIdentity) error {
	if target.SessionUser != target.CurrentUser || target.RoleName != target.SessionUser {
		return fmt.Errorf("target must be directly authenticated with session_user=current_user")
	}
	if target.Superuser || target.RoleOID == target.DatabaseOwner {
		return fmt.Errorf("target role must be a non-owner non-superuser")
	}
	if target.RoleOID == operator.RoleOID {
		return fmt.Errorf("target and operator roles must be distinct")
	}
	if target.HasMembership {
		return fmt.Errorf("target role with inherited role membership is refused")
	}
	return nil
}

type acceptanceFacts struct {
	Version          int    `json:"version"`
	Action           string `json:"action"`
	MigrationID      string `json:"migration_id"`
	ValidationDigest string `json:"validation_digest"`
	LayoutState      string `json:"layout_state"`
	DatabaseOID      uint32 `json:"database_oid"`
	DatabaseName     string `json:"database_name"`
	SystemIdentifier string `json:"system_identifier"`
	TargetRoleOID    uint32 `json:"target_role_oid"`
	TargetRoleName   string `json:"target_role_name"`
}

func acceptanceFactsDigest(action, migrationID, validationDigest, state string, database databaseIdentity, target sessionIdentity) (string, acceptanceFacts, error) {
	facts := acceptanceFacts{1, action, migrationID, validationDigest, state, database.OID,
		database.Name, database.SystemIdentifier, target.RoleOID, target.RoleName}
	digest, _, err := canonicalDigest(facts)
	return digest, facts, err
}

func connectTarget(ctx context.Context, connectionString string) (*pgx.Conn, error) {
	if connectionString == "" {
		return nil, fmt.Errorf("target connection is required")
	}
	connection, err := pgx.Connect(ctx, connectionString)
	if err != nil {
		return nil, fmt.Errorf("directly authenticating target: %w", err)
	}
	return connection, nil
}

// ConfirmAcceptance authenticates the target now and issues a short-lived,
// one-use token bound to the current validation and database facts.
func (s *Engine) ConfirmAcceptance(ctx context.Context, migrationID, validationDigest, targetConnectionString string) (Report, error) {
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return Report{}, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	operator, err := readSessionIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := validateTargetIdentity(target, operator); err != nil {
		return Report{}, err
	}
	database, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil {
		return Report{}, err
	}
	if database != targetDatabase {
		return Report{}, fmt.Errorf("target connection resolves a different database identity")
	}
	var state, active, storedValidation string
	if err := tx.QueryRow(ctx, `select l.state,coalesce(l.active_migration_id,''),coalesce(m.validation_digest,'')
from public.dawgs_schema_layout l join public.dawgs_schema_migration m on m.migration_id=$1 where l.singleton`, migrationID).
		Scan(&state, &active, &storedValidation); err != nil {
		return Report{}, err
	}
	if state != "v2_pending_acceptance" || active != migrationID || storedValidation != validationDigest || validationDigest == "" {
		return Report{}, fmt.Errorf("accept confirmation refused: active validation facts changed")
	}
	factsDigest, _, err := acceptanceFactsDigest("accept", migrationID, validationDigest, state, database, target)
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, "accept", migrationID, factsDigest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: state, MigrationID: migrationID,
		ValidationDigest: validationDigest, ConfirmationToken: token, TargetRole: target.RoleName, CreatedAt: time.Now().UTC()}, nil
}

func confirmationAttemptID(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Engine) revalidateAcceptance(ctx context.Context, tx pgx.Tx, migrationID, expectedDigest string) error {
	rows, err := tx.Query(ctx, `select graph_id,legacy_relation,legacy_oid,source_count,source_digest
from public.dawgs_schema_migration_graph where migration_id=$1 and phase='copied' order by graph_id`, migrationID)
	if err != nil {
		return err
	}
	type checkpoint struct {
		graphID int32
		legacy  string
		oid     uint32
		count   int64
		digest  string
	}
	var checkpoints []checkpoint
	for rows.Next() {
		var value checkpoint
		if err := rows.Scan(&value.graphID, &value.legacy, &value.oid, &value.count, &value.digest); err != nil {
			rows.Close()
			return err
		}
		checkpoints = append(checkpoints, value)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	hash := sha256.New()
	for _, checkpoint := range checkpoints {
		legacy := pgx.Identifier{"public", checkpoint.legacy}.Sanitize()
		parent := pgx.Identifier{"public", model.EdgePartitionTableName(checkpoint.graphID)}.Sanitize()
		var oid uint32
		if err := tx.QueryRow(ctx, `select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relname=$1`, checkpoint.legacy).Scan(&oid); err != nil {
			return err
		}
		sourceCount, sourceDigest, err := relationDigest(ctx, tx, legacy)
		if err != nil {
			return err
		}
		targetCount, targetDigest, err := relationDigest(ctx, tx, parent)
		if err != nil {
			return err
		}
		if oid != checkpoint.oid || sourceCount != checkpoint.count || sourceDigest != checkpoint.digest || targetCount != sourceCount || targetDigest != sourceDigest {
			return fmt.Errorf("acceptance revalidation failed for graph %d", checkpoint.graphID)
		}
		if err := validateExactEdgeEquality(ctx, tx, legacy, parent, checkpoint.graphID); err != nil {
			return err
		}
		if err := validateGraphTopology(ctx, tx, checkpoint.graphID); err != nil {
			return err
		}
		var drift int64
		if err := tx.QueryRow(ctx, `select count(*) from (
select coalesce(e.id,i.edge_id) from public.edge e full join public.edge_identity i
on i.graph_id=e.graph_id and i.edge_id=e.id
where coalesce(e.graph_id,i.graph_id)=$1 and (e.id is null or i.edge_id is null or e.kind_id<>i.kind_id)) d`, checkpoint.graphID).Scan(&drift); err != nil {
			return err
		}
		if drift != 0 {
			return fmt.Errorf("acceptance revalidation found identity drift in graph %d", checkpoint.graphID)
		}
		fmt.Fprintf(hash, "%d:%d:%s:%d;", checkpoint.graphID, targetCount, targetDigest, oid)
	}
	if err := validateGlobalRegistriesAndSequences(ctx, tx); err != nil {
		return fmt.Errorf("acceptance global revalidation: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return fmt.Errorf("acceptance validation digest is stale")
	}
	return nil
}

func grantTarget(ctx context.Context, tx pgx.Tx, role string) error {
	target := pgx.Identifier{role}.Sanitize()
	statements := []string{
		fmt.Sprintf(`grant usage on schema public to %s`, target),
		fmt.Sprintf(`grant select on table public.graph to %s`, target),
		fmt.Sprintf(`grant select,insert,update on table public.kind to %s`, target),
		fmt.Sprintf(`grant select,insert,update,delete on table public.node,public.edge to %s`, target),
		fmt.Sprintf(`grant select on table public.dawgs_schema_layout,public.dawgs_schema_migration,public.dawgs_edge_kind_leaf,public.edge_identity,public.edge_property_constraint,public.dawgs_legacy_cleanup_scope,public.dawgs_legacy_cleanup_completion,public.dawgs_restore_rebind_scope to %s`, target),
		fmt.Sprintf(`grant usage,select on all sequences in schema public to %s`, target),
		fmt.Sprintf(`grant execute on function public.dawgs_current_smoke_receipt_valid(text,text,text) to %s`, target),
		fmt.Sprintf(`grant execute on function public.dawgs_current_cleanup_receipt_valid(text,text,text) to %s`, target),
		fmt.Sprintf(`grant execute on function public.dawgs_provision_graph(text) to %s`, target),
		fmt.Sprintf(`grant execute on function public.dawgs_provision_edge_kind_leaf(integer,smallint) to %s`, target),
		fmt.Sprintf(`grant execute on function public.dawgs_wipe_graph() to %s`, target),
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Engine) Accept(ctx context.Context, migrationID, validationDigest, confirmation, targetConnectionString string) (Report, error) {
	if confirmation == "" {
		return Report{}, fmt.Errorf("accept confirmation token is required")
	}
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return Report{}, err
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
	tx, err := coordinator.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	if err := validateTargetIdentity(target, operator); err != nil {
		return Report{}, err
	}
	database, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || database != targetDatabase {
		return Report{}, fmt.Errorf("target connection database identity changed")
	}
	var state, active, storedValidation, manifest string
	var identityEpoch int64
	if err := tx.QueryRow(ctx, `select l.state,coalesce(l.active_migration_id,''),coalesce(m.validation_digest,''),m.manifest_sha256,l.identity_epoch
from public.dawgs_schema_layout l join public.dawgs_schema_migration m on m.migration_id=$1 where l.singleton`, migrationID).
		Scan(&state, &active, &storedValidation, &manifest, &identityEpoch); err != nil {
		return Report{}, err
	}
	if state != "v2_pending_acceptance" || active != migrationID || storedValidation != validationDigest {
		return Report{}, fmt.Errorf("acceptance refused: active validation facts changed")
	}
	var recordedOperatorOID uint32
	var recordedOperatorName string
	if err := tx.QueryRow(ctx, `select (role_facts->>'operator_oid')::pg_catalog.oid,role_facts->>'operator_name'
from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(&recordedOperatorOID, &recordedOperatorName); err != nil {
		return Report{}, err
	}
	if recordedOperatorOID != operator.RoleOID || recordedOperatorName != operator.RoleName {
		return Report{}, fmt.Errorf("migration operator identity changed before owner handoff")
	}
	factsDigest, facts, err := acceptanceFactsDigest("accept", migrationID, validationDigest, state, database, target)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "accept", migrationID, factsDigest, database); err != nil {
		return Report{}, err
	}
	if err := s.revalidateAcceptance(ctx, tx, migrationID, validationDigest); err != nil {
		return Report{}, err
	}
	owner, err := databaseOwnerIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := transferManagedOwnership(ctx, tx, operator, owner, target.RoleName); err != nil {
		return Report{}, err
	}
	cleanupMapDigest, err := sealCleanupMap(ctx, tx, "migration", migrationID, true, identityEpoch)
	if err != nil {
		return Report{}, fmt.Errorf("sealing retained legacy cleanup map: %w", err)
	}
	if err := grantTarget(ctx, tx, target.RoleName); err != nil {
		return Report{}, fmt.Errorf("granting target runtime privileges: %w", err)
	}
	attemptID := confirmationAttemptID(confirmation)
	receipt, err := appendReceipt(ctx, tx, "migration", migrationID, "acceptance", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='accepted_pending_smoke',updated_at=pg_catalog.clock_timestamp() where migration_id=$1`, migrationID); err != nil {
		return Report{}, err
	}
	if tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='accepted_pending_smoke',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='validated'`, migrationID); err != nil {
		return Report{}, err
	} else if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("acceptance fence journal state changed")
	}
	tag, err := tx.Exec(ctx, `update public.dawgs_schema_layout set state='v2_pending_smoke',
accepted_migration_id=null,active_migration_id=$1,accepted_at=pg_catalog.clock_timestamp(),rollback_cutoff_at=pg_catalog.clock_timestamp(),
database_oid=$2::pg_catalog.oid,database_name=$3::text,system_identifier=$4::text,
database_incarnation=pg_catalog.md5($4::text||':'||$2::pg_catalog.oid::text||':'||$3::text),
owner_role_oid=$9::pg_catalog.oid,owner_role_name=$10::text,
current_legacy_cleanup_map_digest=$11::text,
target_role_oid=$5::pg_catalog.oid,target_role_name=$6::text,manifest_sha256=$7::text,validation_digest=$8::text,
post_acceptance_smoke_status='pending',current_smoke_receipt_digest=null
where singleton and state='v2_pending_acceptance' and active_migration_id=$1`, migrationID, database.OID, database.Name,
		database.SystemIdentifier, target.RoleOID, target.RoleName, manifest, validationDigest, owner.OID, owner.Name, cleanupMapDigest)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("acceptance pointer changed concurrently")
	}
	if err := validateAcceptedCatalog(ctx, tx, owner.Name, target.RoleName); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "accept", State: "v2_pending_smoke", MigrationID: migrationID,
		ValidationDigest: validationDigest, AttemptID: attemptID, ReceiptDigest: receipt.Digest,
		TargetRole: target.RoleName, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) ConfirmSmoke(ctx context.Context, migrationID, targetConnectionString string) (Report, error) {
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return Report{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	operator, err := readSessionIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := validateTargetIdentity(target, operator); err != nil {
		return Report{}, err
	}
	database, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || database != targetDatabase {
		return Report{}, fmt.Errorf("target connection resolves a different database identity")
	}
	var state, validation, role, scopeKind, scopeID string
	var roleOID uint32
	if err := tx.QueryRow(ctx, `select state,coalesce(validation_digest,''),coalesce(target_role_name,''),coalesce(target_role_oid,0),
case when state='v2_pending_smoke' then 'migration' else coalesce(accepted_scope_kind,'') end,
case when state='v2_pending_smoke' then coalesce(active_migration_id,'') else coalesce(accepted_scope_id,'') end
from public.dawgs_schema_layout where singleton and
((state='v2_pending_smoke' and active_migration_id=$1) or
 (state='v2_restore_pending_smoke' and accepted_scope_id=$1))`, migrationID).
		Scan(&state, &validation, &role, &roleOID, &scopeKind, &scopeID); err != nil {
		return Report{}, err
	}
	if (state != "v2_pending_smoke" && state != "v2_restore_pending_smoke") ||
		(scopeKind != "migration" && scopeKind != "bootstrap") || scopeID != migrationID ||
		target.RoleName != role || target.RoleOID != roleOID {
		return Report{}, fmt.Errorf("smoke confirmation target or state mismatch")
	}
	digest, _, err := acceptanceFactsDigest("smoke", scopeID, validation, state, database, target)
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, "smoke", migrationID, digest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: state, MigrationID: migrationID,
		ConfirmationToken: token, TargetRole: target.RoleName, CreatedAt: time.Now().UTC()}, nil
}

func runTargetSmoke(ctx context.Context, connection *pgx.Conn, allowEmptyFallback bool) (string, error) {
	tx, err := connection.Begin(ctx)
	if err != nil {
		return "failed", err
	}
	defer tx.Rollback(ctx)
	var graphID int32
	var kindID int16
	if err := tx.QueryRow(ctx, `select graph_id,kind_id from public.dawgs_edge_kind_leaf
where lifecycle_state='active' order by graph_id,kind_id limit 1`).Scan(&graphID, &kindID); err != nil {
		if allowEmptyFallback && errors.Is(err, pgx.ErrNoRows) {
			var graphCount int64
			if countErr := tx.QueryRow(ctx, `select count(*) from public.graph`).Scan(&graphCount); countErr == nil && graphCount == 0 {
				return "fallback", nil
			}
		}
		return "failed", fmt.Errorf("target smoke requires at least one active graph/kind leaf: %w", err)
	}
	random, err := randomID()
	if err != nil {
		return "failed", err
	}
	var nodeA, nodeB, edgeID int64
	if _, err := fmt.Sscanf(random[:15], "%x", &nodeA); err != nil {
		return "failed", err
	}
	nodeA = -nodeA
	nodeB = nodeA - 1
	edgeID = nodeA - 2
	if _, err := tx.Exec(ctx, `insert into public.node(id,graph_id,kind_ids,properties) values($1,$3,array[]::smallint[],'{}'),($2,$3,array[]::smallint[],'{}')`, nodeA, nodeB, graphID); err != nil {
		return "failed", fmt.Errorf("target smoke node insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values($1,$2,$3,$4,$5,'{}')`, edgeID, graphID, nodeA, nodeB, kindID); err != nil {
		return "failed", fmt.Errorf("target smoke edge insert: %w", err)
	}
	var tableName string
	var properties string
	if err := tx.QueryRow(ctx, `select tableoid::pg_catalog.regclass::text,properties::text from public.edge where graph_id=$1 and id=$2`, graphID, edgeID).Scan(&tableName, &properties); err != nil {
		return "failed", fmt.Errorf("target smoke read: %w", err)
	}
	if !strings.Contains(tableName, "edge_") || properties != "{}" {
		return "failed", fmt.Errorf("target smoke routed to unexpected relation %q", tableName)
	}
	if tag, err := tx.Exec(ctx, `delete from public.edge where graph_id=$1 and id=$2`, graphID, edgeID); err != nil {
		return "failed", fmt.Errorf("target smoke edge delete: %w", err)
	} else if tag.RowsAffected() != 1 {
		return "failed", fmt.Errorf("target smoke edge delete affected %d rows", tag.RowsAffected())
	}
	if tag, err := tx.Exec(ctx, `delete from public.node where graph_id=$1 and id in ($2,$3)`, graphID, nodeA, nodeB); err != nil {
		return "failed", fmt.Errorf("target smoke node delete: %w", err)
	} else if tag.RowsAffected() != 2 {
		return "failed", fmt.Errorf("target smoke node delete affected %d rows", tag.RowsAffected())
	}
	if err := tx.Rollback(ctx); err != nil {
		return "failed", err
	}
	return "passed", nil
}

func (s *Engine) Smoke(ctx context.Context, migrationID, confirmation, targetConnectionString string) (Report, error) {
	if confirmation == "" {
		return Report{}, fmt.Errorf("smoke confirmation token is required")
	}
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return Report{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	operator, err := readSessionIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := validateTargetIdentity(target, operator); err != nil {
		return Report{}, err
	}
	database, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || database != targetDatabase {
		return Report{}, fmt.Errorf("target connection resolves a different database identity")
	}
	var state, validation, role, scopeKind, scopeID string
	var roleOID uint32
	if err := tx.QueryRow(ctx, `select state,coalesce(validation_digest,''),coalesce(target_role_name,''),coalesce(target_role_oid,0),
case when state='v2_pending_smoke' then 'migration' else coalesce(accepted_scope_kind,'') end,
case when state='v2_pending_smoke' then coalesce(active_migration_id,'') else coalesce(accepted_scope_id,'') end
from public.dawgs_schema_layout where singleton and
((state='v2_pending_smoke' and active_migration_id=$1) or
 (state='v2_restore_pending_smoke' and accepted_scope_id=$1))`, migrationID).
		Scan(&state, &validation, &role, &roleOID, &scopeKind, &scopeID); err != nil {
		return Report{}, err
	}
	if (state != "v2_pending_smoke" && state != "v2_restore_pending_smoke") ||
		(scopeKind != "migration" && scopeKind != "bootstrap") || scopeID != migrationID ||
		target.RoleName != role || target.RoleOID != roleOID {
		return Report{}, fmt.Errorf("smoke target or state mismatch")
	}
	factsDigest, baseFacts, err := acceptanceFactsDigest("smoke", scopeID, validation, state, database, target)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "smoke", migrationID, factsDigest, database); err != nil {
		return Report{}, err
	}
	result, smokeErr := runTargetSmoke(ctx, targetConnection, scopeKind == "bootstrap")
	errorText := ""
	if smokeErr != nil {
		errorText = smokeErr.Error()
	}
	facts := struct {
		acceptanceFacts
		Result string `json:"result"`
		Error  string `json:"error,omitempty"`
	}{baseFacts, result, errorText}
	attemptID := confirmationAttemptID(confirmation)
	receipt, err := appendReceipt(ctx, tx, scopeKind, scopeID, "target_smoke", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	nextState := state
	if smokeErr == nil {
		nextState = "v2_accepted"
	}
	var tag pgconn.CommandTag
	if state == "v2_pending_smoke" {
		tag, err = tx.Exec(ctx, `update public.dawgs_schema_layout set state=$2,post_acceptance_smoke_status=$3,
current_smoke_receipt_digest=$4,accepted_migration_id=case when $2='v2_accepted' then $1 else null end,
accepted_scope_kind=case when $2='v2_accepted' then 'migration' else null end,
accepted_scope_id=case when $2='v2_accepted' then $1 else null end,
active_migration_id=case when $2='v2_accepted' then null else $1 end
where singleton and state='v2_pending_smoke' and active_migration_id=$1`, migrationID, nextState, result, receipt.Digest)
	} else {
		tag, err = tx.Exec(ctx, `update public.dawgs_schema_layout set state=$2,post_acceptance_smoke_status=$3,
current_smoke_receipt_digest=$4
where singleton and state='v2_restore_pending_smoke' and accepted_scope_kind=$5 and accepted_scope_id=$1`,
			scopeID, nextState, result, receipt.Digest, scopeKind)
	}
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("smoke pointer changed concurrently")
	}
	if smokeErr == nil && state == "v2_pending_smoke" {
		if _, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='accepted',updated_at=pg_catalog.clock_timestamp() where migration_id=$1`, migrationID); err != nil {
			return Report{}, err
		}
		if tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='accepted',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='accepted_pending_smoke'`, migrationID); err != nil {
			return Report{}, err
		} else if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("smoke fence journal state changed")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	report := Report{Version: 1, Action: "smoke", State: nextState, MigrationID: migrationID,
		AttemptID: attemptID, ReceiptDigest: receipt.Digest, TargetRole: target.RoleName, CreatedAt: time.Now().UTC()}
	if smokeErr != nil {
		return report, smokeErr
	}
	return report, nil
}
