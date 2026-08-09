package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
)

type cleanupBinding struct {
	Order       int    `json:"order"`
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	OID         uint32 `json:"oid"`
	Relkind     string `json:"relkind"`
	Fingerprint string `json:"fingerprint"`
}

type cleanupFacts struct {
	Version           int              `json:"version"`
	EvidenceSHA256    string           `json:"evidence_sha256"`
	ScopeKind         string           `json:"scope_kind"`
	ScopeID           string           `json:"scope_id"`
	IdentityEpoch     int64            `json:"identity_epoch"`
	DatabaseOID       uint32           `json:"database_oid"`
	DatabaseName      string           `json:"database_name"`
	SystemIdentifier  string           `json:"system_identifier"`
	AcceptedAt        time.Time        `json:"accepted_at"`
	RequiredSoakNanos int64            `json:"required_soak_nanos"`
	SourceMapSHA256   string           `json:"source_map_sha256"`
	EmptyMapSHA256    string           `json:"empty_map_sha256"`
	SmokeSHA256       string           `json:"smoke_sha256"`
	CatalogSHA256     string           `json:"catalog_sha256"`
	Objects           []cleanupBinding `json:"objects"`
}

func readCleanupEntries(ctx context.Context, queryer interface {
	rowQuerier
	catalog.Querier
}, scopeKind, scopeID string) ([]cleanupMapEntry, error) {
	rows, err := queryer.Query(ctx, `select object_order,object_schema,object_name,original_oid,relkind::text,
object_fingerprint,logical_facts::text from public.dawgs_legacy_cleanup_map
where scope_kind=$1 and scope_id=$2 order by object_order`, scopeKind, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []cleanupMapEntry
	for rows.Next() {
		var entry cleanupMapEntry
		var facts []byte
		if err := rows.Scan(&entry.Order, &entry.Schema, &entry.Name, &entry.OriginalOID, &entry.Relkind,
			&entry.Fingerprint, &facts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(facts, &entry.Facts); err != nil {
			return nil, fmt.Errorf("decoding cleanup map facts for %s.%s: %w", entry.Schema, entry.Name, err)
		}
		digest, _, err := canonicalDigest(entry.Facts)
		if err != nil || digest != entry.Fingerprint || entry.Facts.Schema != entry.Schema || entry.Facts.Name != entry.Name {
			return nil, fmt.Errorf("cleanup map logical facts do not match %s.%s", entry.Schema, entry.Name)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Engine) inspectCleanup(ctx context.Context, queryer interface {
	rowQuerier
	catalog.Querier
}, scopeID string, now time.Time) (string, cleanupFacts, databaseIdentity, error) {
	if s.CleanupEvidence == nil {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup_evidence_required: backup/restore and fleet approval evidence is required")
	}
	if s.RequiredCleanupSoak <= 0 {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup_soak_required: a positive configured soak duration is required")
	}
	if err := s.CleanupEvidence.Validate(now); err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	physical, err := layout.Classify(ctx, queryer)
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	if physical.State != layout.StateV2Accepted || !physical.Structural {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup requires a structurally accepted V2 layout, found %s", physical.State)
	}
	var state, scopeKind, acceptedScopeID, sourceMapDigest, smokeDigest, cleanupStatus string
	var databaseOID uint32
	var databaseName, systemIdentifier string
	var identityEpoch, sealedEpoch int64
	var acceptedAt time.Time
	var applicable, smokeValid bool
	if err := queryer.QueryRow(ctx, `select l.state,coalesce(l.accepted_scope_kind,''),coalesce(l.accepted_scope_id,''),
l.database_oid,l.database_name,l.system_identifier,l.identity_epoch,l.accepted_at,
l.current_legacy_cleanup_map_digest,l.current_smoke_receipt_digest,l.legacy_cleanup_status,
c.applicable,c.identity_epoch,
public.dawgs_current_smoke_receipt_valid(l.accepted_scope_kind,l.accepted_scope_id,l.current_smoke_receipt_digest)
from public.dawgs_schema_layout l join public.dawgs_legacy_cleanup_scope c
  on c.scope_kind=l.accepted_scope_kind and c.scope_id=l.accepted_scope_id where l.singleton`).Scan(
		&state, &scopeKind, &acceptedScopeID, &databaseOID, &databaseName, &systemIdentifier, &identityEpoch,
		&acceptedAt, &sourceMapDigest, &smokeDigest, &cleanupStatus, &applicable, &sealedEpoch, &smokeValid); err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	if state != "v2_accepted" || scopeKind != "migration" || acceptedScopeID != scopeID ||
		!applicable || cleanupStatus != "retained" || !smokeValid {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup is not applicable to the current accepted migration scope and smoke receipt")
	}
	if now.Before(acceptedAt.Add(s.RequiredCleanupSoak)) {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup_soak_incomplete: accepted layout has not completed the configured soak duration")
	}
	database, err := currentDatabaseIdentity(ctx, queryer)
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	evidence := s.CleanupEvidence
	if database.OID != databaseOID || database.Name != databaseName || database.SystemIdentifier != systemIdentifier ||
		evidence.DatabaseOID != database.OID || evidence.DatabaseName != database.Name ||
		evidence.SystemIdentifier != database.SystemIdentifier || evidence.IdentityEpoch != identityEpoch ||
		evidence.ScopeKind != scopeKind || evidence.ScopeID != scopeID || evidence.CleanupMapSHA256 != sourceMapDigest ||
		evidence.SmokeReceiptSHA256 != smokeDigest || !evidence.AcceptedAt.Equal(acceptedAt) ||
		evidence.RequiredSoakSeconds != int64(s.RequiredCleanupSoak/time.Second) {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup evidence is bound to different database, epoch, scope, map, or smoke facts")
	}
	entries, err := readCleanupEntries(ctx, queryer, scopeKind, scopeID)
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	recomputedMap, err := cleanupMapDigest(scopeKind, scopeID, true, entries)
	if err != nil || recomputedMap != sourceMapDigest {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("immutable cleanup map digest does not match its entries")
	}
	currentOIDs := make(map[int]uint32, len(entries))
	if identityEpoch == sealedEpoch {
		for _, entry := range entries {
			currentOIDs[entry.Order] = entry.OriginalOID
		}
	} else {
		var reboundDigest, reboundReceipt string
		if err := queryer.QueryRow(ctx, `select map_digest,receipt_digest from public.dawgs_restore_rebind_scope
where identity_epoch=$1 and scope_kind=$2 and scope_id=$3`, identityEpoch, scopeKind, scopeID).
			Scan(&reboundDigest, &reboundReceipt); err != nil || reboundDigest != sourceMapDigest || reboundReceipt == "" {
			return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup map has not been rebound to the current identity epoch")
		}
		rows, err := queryer.Query(ctx, `select object_order,rebound_oid from public.dawgs_restore_rebind_map
where identity_epoch=$1 and scope_kind=$2 and scope_id=$3 order by object_order`, identityEpoch, scopeKind, scopeID)
		if err != nil {
			return "", cleanupFacts{}, databaseIdentity{}, err
		}
		for rows.Next() {
			var order int
			var oid uint32
			if err := rows.Scan(&order, &oid); err != nil {
				rows.Close()
				return "", cleanupFacts{}, databaseIdentity{}, err
			}
			currentOIDs[order] = oid
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", cleanupFacts{}, databaseIdentity{}, err
		}
		rows.Close()
	}
	if len(currentOIDs) != len(entries) {
		return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup object map is incomplete for the current identity epoch")
	}
	snapshot, err := catalog.ReadSnapshot(ctx, queryer)
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	catalogDigest, err := catalog.Fingerprint(snapshot)
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	byOID := make(map[uint32]catalog.Relation, len(snapshot.Relations))
	for _, relation := range snapshot.Relations {
		byOID[relation.OID] = relation
	}
	bindings := make([]cleanupBinding, 0, len(entries))
	for _, entry := range entries {
		oid := currentOIDs[entry.Order]
		relation, found := byOID[oid]
		if !found || relation.Schema != entry.Schema || relation.Name != entry.Name || relation.Kind != entry.Relkind {
			return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup target %s.%s OID/name/relkind no longer matches", entry.Schema, entry.Name)
		}
		if relation.Kind != "r" {
			return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup target %s.%s has unsupported relkind %s", entry.Schema, entry.Name, relation.Kind)
		}
		fingerprint, _, err := canonicalDigest(cleanupFact(snapshot, relation))
		if err != nil || fingerprint != entry.Fingerprint {
			return "", cleanupFacts{}, databaseIdentity{}, fmt.Errorf("cleanup target %s.%s fingerprint changed", entry.Schema, entry.Name)
		}
		bindings = append(bindings, cleanupBinding{entry.Order, entry.Schema, entry.Name, oid, entry.Relkind, fingerprint})
	}
	emptyDigest, err := cleanupMapDigest(scopeKind, scopeID, false, []cleanupMapEntry{})
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	evidenceDigest, err := evidence.Digest()
	if err != nil {
		return "", cleanupFacts{}, databaseIdentity{}, err
	}
	facts := cleanupFacts{1, evidenceDigest, scopeKind, scopeID, identityEpoch, database.OID, database.Name,
		database.SystemIdentifier, acceptedAt, int64(s.RequiredCleanupSoak), sourceMapDigest, emptyDigest,
		smokeDigest, catalogDigest, bindings}
	digest, _, err := canonicalDigest(facts)
	return digest, facts, database, err
}

func (s *Engine) ConfirmCleanup(ctx context.Context, scopeID string) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	digest, facts, database, err := s.inspectCleanup(ctx, tx, scopeID, timeNowUTC())
	if err != nil {
		return Report{}, err
	}
	token, err := issueConfirmation(ctx, tx, "cleanup", scopeID, digest, database)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: "cleanup", MigrationID: scopeID,
		ConfirmationToken: token, PreflightDigest: digest, CreatedAt: timeNowUTC(),
		Blockers: []string{fmt.Sprintf("will_drop_%d_recorded_legacy_objects", len(facts.Objects))}}, nil
}

func (s *Engine) Cleanup(ctx context.Context, scopeID, confirmation string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: cleanup is restricted to the identity-guarded disposable test harness")
	}
	if confirmation == "" {
		return Report{}, fmt.Errorf("cleanup confirmation token is required")
	}
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
	digest, facts, database, err := s.inspectCleanup(ctx, tx, scopeID, timeNowUTC())
	if err != nil {
		return Report{}, err
	}
	if err := consumeConfirmation(ctx, tx, confirmation, "cleanup", scopeID, digest, database); err != nil {
		return Report{}, err
	}
	attemptID := confirmationAttemptID(confirmation)
	receipt, err := appendReceipt(ctx, tx, facts.ScopeKind, facts.ScopeID, "cleanup", attemptID, facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	for _, object := range facts.Objects {
		identifier := pgx.Identifier{object.Schema, object.Name}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`drop table %s restrict`, identifier)); err != nil {
			return Report{}, fmt.Errorf("dropping recorded cleanup target %s.%s: %w", object.Schema, object.Name, err)
		}
	}
	if _, err := tx.Exec(ctx, `insert into public.dawgs_legacy_cleanup_completion
(scope_kind,scope_id,identity_epoch,source_map_digest,empty_map_digest,cleanup_receipt_digest)
values($1,$2,$3,$4,$5,$6)`, facts.ScopeKind, facts.ScopeID, facts.IdentityEpoch,
		facts.SourceMapSHA256, facts.EmptyMapSHA256, receipt.Digest); err != nil {
		return Report{}, err
	}
	if tag, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='cleaned',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='accepted'`, scopeID); err != nil {
		return Report{}, err
	} else if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("cleanup migration state changed concurrently")
	}
	if tag, err := tx.Exec(ctx, `update public.dawgs_schema_layout set legacy_cleanup_status='complete',
current_legacy_cleanup_map_digest=$2,current_cleanup_receipt_digest=$3
where singleton and state='v2_accepted' and accepted_scope_kind='migration' and accepted_scope_id=$1
  and identity_epoch=$4 and legacy_cleanup_status='retained' and current_legacy_cleanup_map_digest=$5`,
		scopeID, facts.EmptyMapSHA256, receipt.Digest, facts.IdentityEpoch, facts.SourceMapSHA256); err != nil {
		return Report{}, err
	} else if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("cleanup layout pointer changed concurrently")
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_operation_confirmation set consumed_at=pg_catalog.clock_timestamp()
where consumed_at is null`); err != nil {
		return Report{}, err
	}
	if err := s.hitFailpoint("before_cleanup_commit"); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	report := Report{Version: 1, Action: "cleanup", State: "v2_accepted", MigrationID: scopeID,
		AttemptID: attemptID, ReceiptDigest: receipt.Digest, CreatedAt: timeNowUTC(),
		Blockers: []string{"activation_not_ready"}}
	if err := s.hitFailpoint("after_cleanup_commit"); err != nil {
		return report, err
	}
	return report, nil
}
