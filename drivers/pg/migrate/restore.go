package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
)

type rebindObjectFact struct {
	Order       int    `json:"order"`
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	CurrentOID  uint32 `json:"current_oid"`
	Fingerprint string `json:"fingerprint"`
}

type rebindFacts struct {
	Version            int                `json:"version"`
	EvidenceID         string             `json:"evidence_id"`
	EvidenceSHA256     string             `json:"evidence_sha256"`
	RestoreIncarnation string             `json:"restore_incarnation"`
	ArtifactSHA256     string             `json:"artifact_sha256"`
	ScopeKind          string             `json:"scope_kind"`
	ScopeID            string             `json:"scope_id"`
	SourceEpoch        int64              `json:"source_epoch"`
	TargetEpoch        int64              `json:"target_epoch"`
	DatabaseOID        uint32             `json:"database_oid"`
	DatabaseName       string             `json:"database_name"`
	SystemIdentifier   string             `json:"system_identifier"`
	OwnerOID           uint32             `json:"owner_oid"`
	OwnerName          string             `json:"owner_name"`
	TargetOID          uint32             `json:"target_oid"`
	TargetName         string             `json:"target_name"`
	CatalogSHA256      string             `json:"catalog_sha256"`
	CleanupMapSHA256   string             `json:"cleanup_map_sha256"`
	CleanupStatus      string             `json:"cleanup_status"`
	CleanupReceipt     string             `json:"cleanup_receipt,omitempty"`
	Objects            []rebindObjectFact `json:"objects"`
}

func inspectRebind(ctx context.Context, queryer interface {
	rowQuerier
	catalog.Querier
}, evidence RestoreEvidence, targetConnectionString string) (string, rebindFacts, databaseIdentity, sessionIdentity, ownerIdentity, error) {
	if err := evidence.Validate(timeNowUTC()); err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	var state, scopeKind, scopeID, recordedDatabaseName, recordedSystem, cleanupDigest, cleanupStatus, cleanupReceipt, recordedOwner, recordedTarget string
	var recordedDatabaseOID uint32
	var epoch int64
	if err := queryer.QueryRow(ctx, `select state,coalesce(accepted_scope_kind,''),coalesce(accepted_scope_id,''),
coalesce(database_oid,0),coalesce(database_name,''),coalesce(system_identifier,''),identity_epoch,
coalesce(current_legacy_cleanup_map_digest,''),legacy_cleanup_status,coalesce(current_cleanup_receipt_digest,''),
coalesce(owner_role_name,''),coalesce(target_role_name,'')
from public.dawgs_schema_layout where singleton`).Scan(&state, &scopeKind, &scopeID, &recordedDatabaseOID,
		&recordedDatabaseName, &recordedSystem, &epoch, &cleanupDigest, &cleanupStatus, &cleanupReceipt,
		&recordedOwner, &recordedTarget); err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	if state != "v2_accepted" {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("rebind_restore_unsupported_state: accepted V2 is required, found %s", state)
	}
	if evidence.ScopeKind != scopeKind || evidence.ScopeID != scopeID || evidence.SourceIdentityEpoch != epoch ||
		evidence.SourceDatabaseOID != recordedDatabaseOID || evidence.SourceDatabaseName != recordedDatabaseName || evidence.SourceSystemIdentifier != recordedSystem ||
		evidence.OwnerRoleName != recordedOwner || evidence.TargetRoleName != recordedTarget {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restore evidence does not match the recorded accepted source identity and scope")
	}
	currentDatabase, err := currentDatabaseIdentity(ctx, queryer)
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	if currentDatabase.OID != evidence.TargetDatabaseOID || currentDatabase.Name != evidence.TargetDatabaseName || currentDatabase.SystemIdentifier != evidence.TargetSystemIdentifier {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restore evidence target does not match the current database incarnation")
	}
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || targetDatabase != currentDatabase || target.RoleName != evidence.TargetRoleName || target.Superuser || target.HasMembership || target.RoleOID == target.DatabaseOwner {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restored target identity is not the direct restricted role named by evidence")
	}
	owner, err := databaseOwnerIdentity(ctx, queryer)
	if err != nil || owner.Name != evidence.OwnerRoleName || owner.OID == target.RoleOID {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restored database owner identity does not match evidence")
	}
	physical, err := layout.Classify(ctx, queryer)
	if err != nil || !physical.Structural {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restored V2 topology is not structurally valid")
	}
	snapshot, err := catalog.ReadSnapshot(ctx, queryer)
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	catalogDigest, err := catalog.Fingerprint(snapshot)
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	if cleanupStatus != "retained" && cleanupStatus != "complete" {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restore cleanup state is invalid")
	}
	type expectedObject struct {
		order                     int
		schema, name, fingerprint string
	}
	var expectedObjects []expectedObject
	if cleanupStatus == "retained" {
		rows, err := queryer.Query(ctx, `select object_order,object_schema,object_name,object_fingerprint
from public.dawgs_legacy_cleanup_map where scope_kind=$1 and scope_id=$2 order by object_order`, scopeKind, scopeID)
		if err != nil {
			return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
		}
		for rows.Next() {
			var value expectedObject
			if err := rows.Scan(&value.order, &value.schema, &value.name, &value.fingerprint); err != nil {
				rows.Close()
				return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
			}
			expectedObjects = append(expectedObjects, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
		}
		rows.Close()
	} else {
		var completionCount int64
		if err := queryer.QueryRow(ctx, `select count(*) from public.dawgs_legacy_cleanup_completion
where scope_kind=$1 and scope_id=$2 and empty_map_digest=$3 and cleanup_receipt_digest=$4`,
			scopeKind, scopeID, cleanupDigest, cleanupReceipt).Scan(&completionCount); err != nil || completionCount != 1 {
			return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restored cleanup completion evidence is inconsistent")
		}
	}
	byName := make(map[string]catalog.Relation, len(snapshot.Relations))
	for _, relation := range snapshot.Relations {
		byName[relation.Schema+"."+relation.Name] = relation
	}
	bindings := make([]rebindObjectFact, 0, len(expectedObjects))
	for _, expected := range expectedObjects {
		relation, found := byName[expected.schema+"."+expected.name]
		if !found {
			return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restore cleanup target %s.%s is missing", expected.schema, expected.name)
		}
		fingerprint, _, err := canonicalDigest(cleanupFact(snapshot, relation))
		if err != nil || fingerprint != expected.fingerprint {
			return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, fmt.Errorf("restore cleanup target %s.%s does not match its logical fingerprint", expected.schema, expected.name)
		}
		bindings = append(bindings, rebindObjectFact{expected.order, expected.schema, expected.name, relation.OID, fingerprint})
	}
	evidenceDigest, err := evidence.Digest()
	if err != nil {
		return "", rebindFacts{}, databaseIdentity{}, sessionIdentity{}, ownerIdentity{}, err
	}
	facts := rebindFacts{1, evidence.EvidenceID, evidenceDigest, evidence.RestoreIncarnation, evidence.ArtifactSHA256,
		scopeKind, scopeID, epoch, epoch + 1, currentDatabase.OID, currentDatabase.Name, currentDatabase.SystemIdentifier,
		owner.OID, owner.Name, target.RoleOID, target.RoleName, catalogDigest, cleanupDigest, cleanupStatus, cleanupReceipt, bindings}
	digest, _, err := canonicalDigest(facts)
	return digest, facts, currentDatabase, target, owner, err
}

var timeNowUTC = func() time.Time { return time.Now().UTC() }

func (s *Engine) ConfirmRebindRestore(ctx context.Context, evidence RestoreEvidence, targetConnectionString string) (Report, error) {
	layoutPresent, state, err := restoreLayoutState(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	if !layoutPresent || state != "v2_accepted" {
		return s.confirmStateRebind(ctx, evidence, targetConnectionString)
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	digest, facts, database, _, _, err := inspectRebind(ctx, tx, evidence, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, "rebind-restore", facts.ScopeID, digest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: "rebind-restore", MigrationID: facts.ScopeID,
		ConfirmationToken: token, PreflightDigest: digest, TargetRole: facts.TargetName, CreatedAt: timeNowUTC()}, nil
}

func (s *Engine) RebindRestore(ctx context.Context, evidence RestoreEvidence, confirmation, targetConnectionString string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: restore rebind is restricted to the identity-guarded disposable test harness")
	}
	if confirmation == "" {
		return Report{}, fmt.Errorf("rebind-restore confirmation token is required")
	}
	layoutPresent, state, err := restoreLayoutState(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	if !layoutPresent || state != "v2_accepted" {
		return s.rebindState(ctx, evidence, confirmation, targetConnectionString)
	}
	expected, err := currentDatabaseIdentity(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	operator, err := readSessionIdentity(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	coordinator, err := s.connectHandoffCoordinator(ctx, expected)
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
	digest, facts, database, _, owner, err := inspectRebind(ctx, tx, evidence, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "rebind-restore", facts.ScopeID, digest, database); err != nil {
		return Report{}, err
	}
	if err := transferManagedOwnership(ctx, tx, operator, owner, facts.TargetName); err != nil {
		return Report{}, err
	}
	if err := grantTarget(ctx, tx, facts.TargetName); err != nil {
		return Report{}, err
	}
	attemptID := confirmationAttemptID(confirmation)
	receipt, err := appendReceipt(ctx, tx, facts.ScopeKind, facts.ScopeID, "rebind_restore", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `insert into public.dawgs_restore_rebind_scope
(identity_epoch,scope_kind,scope_id,map_digest,receipt_digest) values($1,$2,$3,$4,$5)`,
		facts.TargetEpoch, facts.ScopeKind, facts.ScopeID, facts.CleanupMapSHA256, receipt.Digest); err != nil {
		return Report{}, err
	}
	for _, object := range facts.Objects {
		if _, err := tx.Exec(ctx, `insert into public.dawgs_restore_rebind_map
(identity_epoch,scope_kind,scope_id,object_order,object_schema,object_name,rebound_oid,object_fingerprint,rebind_receipt_digest)
values($1,$2,$3,$4,$5,$6,$7,$8,$9)`, facts.TargetEpoch, facts.ScopeKind, facts.ScopeID, object.Order,
			object.Schema, object.Name, object.CurrentOID, object.Fingerprint, receipt.Digest); err != nil {
			return Report{}, err
		}
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_operation_confirmation set consumed_at=pg_catalog.clock_timestamp()
where consumed_at is null`); err != nil {
		return Report{}, err
	}
	tag, err := tx.Exec(ctx, `update public.dawgs_schema_layout set state='v2_restore_pending_smoke',
database_oid=$1::pg_catalog.oid,database_name=$2::text,system_identifier=$3::text,
database_incarnation=pg_catalog.md5($3::text||':'||$1::pg_catalog.oid::text||':'||$2::text||':'||$4::text),identity_epoch=$5::bigint,
owner_role_oid=$6::pg_catalog.oid,owner_role_name=$7::text,target_role_oid=$8::pg_catalog.oid,target_role_name=$9::text,
post_acceptance_smoke_status='pending',current_smoke_receipt_digest=null,current_rebind_receipt_digest=$10::text
where singleton and state='v2_accepted' and accepted_scope_kind=$11::text and accepted_scope_id=$12::text and identity_epoch=$13::bigint`,
		facts.DatabaseOID, facts.DatabaseName, facts.SystemIdentifier, facts.RestoreIncarnation, facts.TargetEpoch,
		facts.OwnerOID, facts.OwnerName, facts.TargetOID, facts.TargetName, receipt.Digest, facts.ScopeKind, facts.ScopeID, facts.SourceEpoch)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("restore rebind pointer changed concurrently")
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "rebind-restore", State: "v2_restore_pending_smoke", MigrationID: facts.ScopeID,
		AttemptID: attemptID, ReceiptDigest: receipt.Digest, TargetRole: facts.TargetName, CreatedAt: timeNowUTC()}, nil
}
