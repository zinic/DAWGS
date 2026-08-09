package migrate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
)

type stateRebindFacts struct {
	Version            int                `json:"version"`
	EvidenceSHA256     string             `json:"evidence_sha256"`
	LayoutPresent      bool               `json:"layout_present"`
	State              string             `json:"state"`
	ScopeKind          string             `json:"scope_kind"`
	ScopeID            string             `json:"scope_id"`
	SourceEpoch        int64              `json:"source_epoch"`
	TargetEpoch        int64              `json:"target_epoch"`
	SourceDatabaseOID  uint32             `json:"source_database_oid"`
	SourceDatabaseName string             `json:"source_database_name"`
	DatabaseOID        uint32             `json:"database_oid"`
	DatabaseName       string             `json:"database_name"`
	SystemIdentifier   string             `json:"system_identifier"`
	RestoreIncarnation string             `json:"restore_incarnation"`
	OwnerOID           uint32             `json:"owner_oid"`
	OwnerName          string             `json:"owner_name"`
	TargetOID          uint32             `json:"target_oid"`
	TargetName         string             `json:"target_name"`
	PreparationName    string             `json:"preparation_name,omitempty"`
	CatalogSHA256      string             `json:"catalog_sha256"`
	CleanupMapSHA256   string             `json:"cleanup_map_sha256,omitempty"`
	Objects            []rebindObjectFact `json:"objects,omitempty"`
}

func restoreLayoutState(ctx context.Context, queryer rowQuerier) (bool, string, error) {
	var present bool
	if err := queryer.QueryRow(ctx, `select pg_catalog.to_regclass('public.dawgs_schema_layout') is not null`).Scan(&present); err != nil {
		return false, "", err
	}
	if !present {
		return false, "", nil
	}
	var state string
	if err := queryer.QueryRow(ctx, `select state from public.dawgs_schema_layout where singleton`).Scan(&state); err != nil {
		return true, "", err
	}
	return true, state, nil
}

func (s *Engine) inspectStateRebind(ctx context.Context, queryer interface {
	rowQuerier
	catalog.Querier
}, evidence RestoreEvidence, targetConnectionString string) (string, stateRebindFacts, databaseIdentity, error) {
	if err := evidence.Validate(timeNowUTC()); err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	if evidence.SourceSystemIdentifier != evidence.TargetSystemIdentifier {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("rebind_restore_cross_cluster_unsupported_state: only accepted V2 may be rebound across clusters")
	}
	layoutPresent, state, err := restoreLayoutState(ctx, queryer)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	var scopeKind, scopeID, sourceName, sourceSystem, preparationName string
	var sourceOID uint32
	var sourceEpoch int64
	if layoutPresent {
		var bootstrapID, activeID string
		if err := queryer.QueryRow(ctx, `select state,coalesce(bootstrap_operation_id,''),coalesce(active_migration_id,''),
coalesce(database_oid,0),coalesce(database_name,''),coalesce(system_identifier,''),identity_epoch
from public.dawgs_schema_layout where singleton`).Scan(&state, &bootstrapID, &activeID, &sourceOID,
			&sourceName, &sourceSystem, &sourceEpoch); err != nil {
			return "", stateRebindFacts{}, databaseIdentity{}, err
		}
		switch state {
		case "v2_pending_handoff":
			scopeKind, scopeID = "bootstrap", bootstrapID
		case "v2_migrating", "v2_pending_acceptance", "v2_pending_smoke":
			scopeKind, scopeID = "migration", activeID
		case "v2_rolled_back":
			scopeKind = "migration"
			if err := queryer.QueryRow(ctx, `select migration_id from public.dawgs_schema_fence_migration
where state in ('rolled_back_handoff_pending','rolled_back') order by updated_at desc,migration_id desc limit 1`).Scan(&scopeID); err != nil {
				return "", stateRebindFacts{}, databaseIdentity{}, err
			}
		default:
			return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("rebind_restore_unsupported_state: accepted V2 or a same-cluster transition state is required, found %s", state)
		}
	} else {
		var fenceCount int64
		if err := queryer.QueryRow(ctx, `select count(*) from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relname='dawgs_schema_fence_migration' and c.relkind='r'`).Scan(&fenceCount); err != nil || fenceCount != 1 {
			return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("rebind_restore_unsupported_state: no supported transition journal is present")
		}
		scopeKind = "migration"
		if err := queryer.QueryRow(ctx, `select migration_id,state,database_oid,database_name,system_identifier,identity_epoch
from public.dawgs_schema_fence_migration where state not in ('accepted') order by updated_at desc,migration_id desc limit 1`).
			Scan(&scopeID, &state, &sourceOID, &sourceName, &sourceSystem, &sourceEpoch); err != nil {
			return "", stateRebindFacts{}, databaseIdentity{}, err
		}
	}
	if scopeID == "" || evidence.ScopeKind != scopeKind || evidence.ScopeID != scopeID ||
		evidence.SourceDatabaseOID != sourceOID || evidence.SourceDatabaseName != sourceName ||
		evidence.SourceSystemIdentifier != sourceSystem || evidence.SourceIdentityEpoch != sourceEpoch {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restore evidence does not match the recorded transition source identity and scope")
	}
	current, err := currentDatabaseIdentity(ctx, queryer)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	if current.OID != evidence.TargetDatabaseOID || current.Name != evidence.TargetDatabaseName ||
		current.SystemIdentifier != evidence.TargetSystemIdentifier {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restore evidence target does not match the current database incarnation")
	}
	targetConnection, err := connectTarget(ctx, targetConnectionString)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	defer targetConnection.Close(ctx)
	target, err := readSessionIdentity(ctx, targetConnection)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	targetDatabase, err := currentDatabaseIdentity(ctx, targetConnection)
	if err != nil || targetDatabase != current || target.RoleName != evidence.TargetRoleName ||
		target.SessionUser != target.CurrentUser || target.Superuser || target.HasMembership || target.RoleOID == target.DatabaseOwner {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restored target identity is not the direct restricted role named by evidence")
	}
	owner, err := databaseOwnerIdentity(ctx, queryer)
	if err != nil || owner.Name != evidence.OwnerRoleName || owner.OID == target.RoleOID {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restored database owner identity does not match evidence")
	}
	physical, err := layout.Classify(ctx, queryer)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	if layoutPresent && state != "v2_rolled_back" && !physical.Structural {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restored transition topology is not structurally valid")
	}
	if !layoutPresent && physical.State != layout.StateV1 {
		return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restored fenced source no longer matches frozen V1")
	}
	if scopeKind == "migration" {
		var encodedRoles []byte
		if err := queryer.QueryRow(ctx, `select role_facts from public.dawgs_schema_fence_migration where migration_id=$1`, scopeID).Scan(&encodedRoles); err != nil {
			return "", stateRebindFacts{}, databaseIdentity{}, err
		}
		var roles preflightRoleFact
		if err := json.Unmarshal(encodedRoles, &roles); err != nil || roles.TargetName != target.RoleName {
			return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("restored transition role facts do not match the target")
		}
		preparationName = roles.PreparationName
	}
	snapshot, err := catalog.ReadSnapshot(ctx, queryer)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	catalogDigest, err := catalog.Fingerprint(snapshot)
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	var cleanupDigest string
	var objects []rebindObjectFact
	if state == "v2_pending_smoke" {
		if err := queryer.QueryRow(ctx, `select current_legacy_cleanup_map_digest from public.dawgs_schema_layout where singleton`).Scan(&cleanupDigest); err != nil {
			return "", stateRebindFacts{}, databaseIdentity{}, err
		}
		entries, err := readCleanupEntries(ctx, queryer, scopeKind, scopeID)
		if err != nil {
			return "", stateRebindFacts{}, databaseIdentity{}, err
		}
		if digest, err := cleanupMapDigest(scopeKind, scopeID, true, entries); err != nil || digest != cleanupDigest {
			return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("pending-smoke cleanup map is inconsistent")
		}
		byOID := make(map[uint32]catalog.Relation, len(snapshot.Relations))
		for _, relation := range snapshot.Relations {
			byOID[relation.OID] = relation
		}
		for _, entry := range entries {
			relation, found := byOID[entry.OriginalOID]
			fingerprint := ""
			if found {
				fingerprint, _, err = canonicalDigest(cleanupFact(snapshot, relation))
			}
			if err != nil || !found || relation.Schema != entry.Schema || relation.Name != entry.Name || fingerprint != entry.Fingerprint {
				return "", stateRebindFacts{}, databaseIdentity{}, fmt.Errorf("pending-smoke cleanup target %s.%s changed during restore", entry.Schema, entry.Name)
			}
			objects = append(objects, rebindObjectFact{entry.Order, entry.Schema, entry.Name, relation.OID, fingerprint})
		}
	}
	evidenceDigest, err := evidence.Digest()
	if err != nil {
		return "", stateRebindFacts{}, databaseIdentity{}, err
	}
	facts := stateRebindFacts{1, evidenceDigest, layoutPresent, state, scopeKind, scopeID, sourceEpoch,
		sourceEpoch + 1, sourceOID, sourceName, current.OID, current.Name, current.SystemIdentifier,
		evidence.RestoreIncarnation, owner.OID, owner.Name, target.RoleOID, target.RoleName, preparationName,
		catalogDigest, cleanupDigest, objects}
	digest, _, err := canonicalDigest(facts)
	return digest, facts, current, err
}

func (s *Engine) confirmStateRebind(ctx context.Context, evidence RestoreEvidence, targetConnectionString string) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	digest, facts, database, err := s.inspectStateRebind(ctx, tx, evidence, targetConnectionString)
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
	return Report{Version: 1, Action: "confirm", State: facts.State, MigrationID: facts.ScopeID,
		ConfirmationToken: token, PreflightDigest: digest, TargetRole: facts.TargetName, CreatedAt: timeNowUTC()}, nil
}

func (s *Engine) rebindState(ctx context.Context, evidence RestoreEvidence, confirmation, targetConnectionString string) (Report, error) {
	expected, err := currentDatabaseIdentity(ctx, s.Pool)
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
	digest, facts, database, err := s.inspectStateRebind(ctx, tx, evidence, targetConnectionString)
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "rebind-restore", facts.ScopeID, digest, database); err != nil {
		return Report{}, err
	}
	attemptID := confirmationAttemptID(confirmation)
	receipt, err := appendReceipt(ctx, tx, facts.ScopeKind, facts.ScopeID, "rebind_restore", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	if facts.LayoutPresent {
		if tag, err := tx.Exec(ctx, `update public.dawgs_schema_layout set database_oid=$1::pg_catalog.oid,
database_name=$2::text,system_identifier=$3::text,
database_incarnation=pg_catalog.md5($3::text||':'||$1::pg_catalog.oid::text||':'||$2::text||':'||$4::text),
identity_epoch=$5,current_rebind_receipt_digest=$6,
post_acceptance_smoke_status=case when state='v2_pending_smoke' then 'pending' else post_acceptance_smoke_status end,
current_smoke_receipt_digest=case when state='v2_pending_smoke' then null else current_smoke_receipt_digest end,
target_role_oid=case when state='v2_pending_smoke' then $7::pg_catalog.oid else target_role_oid end,
target_role_name=case when state='v2_pending_smoke' then $8::text else target_role_name end
where singleton and state=$9 and identity_epoch=$10`, facts.DatabaseOID, facts.DatabaseName,
			facts.SystemIdentifier, facts.RestoreIncarnation, facts.TargetEpoch, receipt.Digest,
			facts.TargetOID, facts.TargetName, facts.State, facts.SourceEpoch); err != nil {
			return Report{}, err
		} else if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("restore state pointer changed concurrently")
		}
	}
	if facts.ScopeKind == "migration" {
		if facts.State == "v2_rolled_back" {
			preparation := pgx.Identifier{facts.PreparationName}.Sanitize()
			for _, statement := range []string{
				fmt.Sprintf(`revoke all privileges on all tables in schema public from %s`, preparation),
				fmt.Sprintf(`revoke all privileges on all sequences in schema public from %s`, preparation),
				fmt.Sprintf(`revoke all privileges on all functions in schema public from %s`, preparation),
				fmt.Sprintf(`revoke all privileges on schema public from %s`, preparation),
			} {
				if _, err := tx.Exec(ctx, statement); err != nil {
					return Report{}, err
				}
			}
		}
		if tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set database_oid=$2::pg_catalog.oid,
database_name=$3::text,system_identifier=$4::text,identity_epoch=$5,current_rebind_receipt_digest=$6,
state=case when $7='v2_rolled_back' then 'rolled_back_handoff_pending' else state end,
updated_at=pg_catalog.clock_timestamp() where migration_id=$1`, facts.ScopeID, facts.DatabaseOID,
			facts.DatabaseName, facts.SystemIdentifier, facts.TargetEpoch, receipt.Digest, facts.State); err != nil {
			return Report{}, err
		} else if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("restore fence pointer changed concurrently")
		}
	}
	if facts.State == "v2_pending_smoke" {
		if _, err := tx.Exec(ctx, `insert into public.dawgs_restore_rebind_scope
(identity_epoch,scope_kind,scope_id,map_digest,receipt_digest) values($1,$2,$3,$4,$5)`,
			facts.TargetEpoch, facts.ScopeKind, facts.ScopeID, facts.CleanupMapSHA256, receipt.Digest); err != nil {
			return Report{}, err
		}
		for _, object := range facts.Objects {
			if _, err := tx.Exec(ctx, `insert into public.dawgs_restore_rebind_map
(identity_epoch,scope_kind,scope_id,object_order,object_schema,object_name,rebound_oid,object_fingerprint,rebind_receipt_digest)
values($1,$2,$3,$4,$5,$6,$7,$8,$9)`, facts.TargetEpoch, facts.ScopeKind, facts.ScopeID,
				object.Order, object.Schema, object.Name, object.CurrentOID, object.Fingerprint, receipt.Digest); err != nil {
				return Report{}, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_operation_confirmation set consumed_at=pg_catalog.clock_timestamp()
where consumed_at is null`); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "rebind-restore", State: facts.State, MigrationID: facts.ScopeID,
		AttemptID: attemptID, ReceiptDigest: receipt.Digest, TargetRole: facts.TargetName, CreatedAt: timeNowUTC()}, nil
}
