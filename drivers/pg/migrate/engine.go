package migrate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
	"github.com/specterops/dawgs/drivers/pg/internal/schema"
	"github.com/specterops/dawgs/drivers/pg/model"
)

const layoutLockKey int64 = 0x4441574753563200

type Engine struct {
	Pool                        *pgxpool.Pool
	Failpoint                   func(string) error
	AllowUnreleasedDisposable   bool
	WriterProbes                []WriterProbe
	RetirementConnectionString  string
	TargetConnectionString      string
	PreparationRole             string
	PreparationConnectionString string
	CapacityEvidence            *CapacityEvidence
	BackupEvidence              *BackupEvidence
	CleanupEvidence             *CleanupEvidence
	RequiredCleanupSoak         time.Duration
	CopyChunkSize               int
}

type graphFact struct {
	ID          int32
	Name        string
	StoredKinds []string
}

func New(ctx context.Context, connectionString string) (*Engine, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, err
	}
	return &Engine{Pool: pool}, nil
}
func (s *Engine) Close() { s.Pool.Close() }

func (s *Engine) Status(ctx context.Context) (Report, error) {
	var fenceJournal bool
	if err := s.Pool.QueryRow(ctx, `select pg_catalog.to_regclass('public.dawgs_schema_fence_migration') is not null`).Scan(&fenceJournal); err != nil {
		return Report{}, err
	}
	if fenceJournal {
		var migrationID, state string
		var database databaseIdentity
		var epoch int64
		err := s.Pool.QueryRow(ctx, `select migration_id,state,database_oid,database_name,system_identifier,identity_epoch
from public.dawgs_schema_fence_migration order by created_at desc,migration_id desc limit 1`).Scan(
			&migrationID, &state, &database.OID, &database.Name, &database.SystemIdentifier, &epoch)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Report{}, err
		}
		if err == nil && state != "accepted" {
			blockers := []string(nil)
			switch state {
			case "fence_installed_unverified":
				blockers = []string{"writer_probe_required"}
			case "fence_database_verified_retirement_pending":
				blockers = []string{"credential_retirement_required"}
			case "writer_retirement_in_progress":
				blockers = []string{"resume_writer_retirement"}
			}
			progress, progressErr := s.graphProgress(ctx, migrationID)
			if progressErr != nil {
				return Report{}, progressErr
			}
			return Report{Version: 1, Action: "status", State: state, MigrationID: migrationID, ScopeKind: "migration",
				DatabaseOID: database.OID, DatabaseName: database.Name, SystemIdentifier: database.SystemIdentifier,
				IdentityEpoch: epoch, Recovery: recoveryGuidance(state),
				CreatedAt: time.Now().UTC(), Blockers: blockers, Progress: progress}, nil
		}
	}
	facts, err := layout.Classify(ctx, s.Pool)
	if err != nil {
		return Report{}, err
	}
	report := Report{Version: 1, Action: "status", State: string(facts.State), LayoutVersion: facts.Version,
		Recovery: recoveryGuidance(string(facts.State)), CreatedAt: time.Now().UTC()}
	if facts.State != layout.StateUninitialized && facts.State != layout.StateV1 && facts.State != layout.StateDrifted {
		if err := s.Pool.QueryRow(ctx, `select
coalesce(active_migration_id,accepted_scope_id,bootstrap_operation_id,''),
coalesce(accepted_scope_kind,case when active_migration_id is not null then 'migration'
  when bootstrap_operation_id is not null then 'bootstrap' else '' end),
coalesce(database_oid,0),coalesce(database_name,''),coalesce(system_identifier,''),identity_epoch,
coalesce(owner_role_name,''),coalesce(target_role_name,''),post_acceptance_smoke_status,
coalesce(current_smoke_receipt_digest,''),legacy_cleanup_status,coalesce(current_legacy_cleanup_map_digest,'')
from public.dawgs_schema_layout where singleton`).Scan(&report.MigrationID, &report.ScopeKind,
			&report.DatabaseOID, &report.DatabaseName, &report.SystemIdentifier, &report.IdentityEpoch,
			&report.OwnerRole, &report.TargetRole, &report.SmokeStatus, &report.SmokeReceipt,
			&report.CleanupStatus, &report.CleanupMapSHA256); err != nil {
			return Report{}, err
		}
		snapshot, err := catalog.ReadSnapshot(ctx, s.Pool)
		if err != nil {
			return Report{}, err
		}
		report.TopologySHA256, err = catalog.Fingerprint(snapshot)
		if err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

func recoveryGuidance(state string) string {
	switch state {
	case "uninitialized":
		return "use the restricted bootstrap workflow; application bootstrap is disabled"
	case "v1":
		return "run preflight with the frozen V1 manifest and evidence"
	case "fence_installed_unverified":
		return "confirm and run verify-fence with every direct writer probe"
	case "fence_database_verified_retirement_pending", "writer_retirement_in_progress":
		return "complete writer credential retirement and rerun verify-fence"
	case "fence_verified", "copying":
		return "confirm resume to continue bounded copy"
	case "pending_validation", "v2_pending_acceptance":
		return "run validation, then confirm acceptance with the direct target"
	case "v2_pending_smoke", "v2_restore_pending_smoke", "accepted_pending_smoke":
		return "confirm and run target smoke"
	case "rolled_back_handoff_pending", "v2_rolled_back":
		return "complete the directly authenticated V1 preparation-role handoff"
	case "v2_accepted", "accepted":
		return "layout is accepted; activation remains governed by the release interlock"
	case "drifted":
		return "stop mutation and perform catalog repair or restore/rebind"
	default:
		return "inspect durable receipts and the transition runbook before continuing"
	}
}

func (s *Engine) graphProgress(ctx context.Context, migrationID string) ([]GraphProgress, error) {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `select pg_catalog.to_regclass('public.dawgs_schema_migration_graph') is not null`).Scan(&exists); err != nil || !exists {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `select graph_id,phase,high_water,copied_count,coalesce(source_count,0)
from public.dawgs_schema_migration_graph where migration_id=$1 order by graph_id`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var progress []GraphProgress
	for rows.Next() {
		var value GraphProgress
		if err := rows.Scan(&value.GraphID, &value.Phase, &value.HighWater, &value.CopiedRows, &value.TotalRows); err != nil {
			return nil, err
		}
		progress = append(progress, value)
	}
	return progress, rows.Err()
}

func (s *Engine) Preflight(ctx context.Context, manifest Manifest) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	manifestDigest, factsDigest, _, err := s.inspectPreflight(ctx, tx, manifest)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("committing read-only preflight: %w", err)
	}
	return Report{Version: 1, Action: "preflight", State: "ready", ManifestSHA256: manifestDigest,
		PreflightDigest: factsDigest, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) inspectPreflight(ctx context.Context, tx pgx.Tx, manifest Manifest) (string, string, databaseIdentity, error) {
	if err := manifest.ValidateFrozenV1Lineage(); err != nil {
		return "", "", databaseIdentity{}, err
	}
	var serverVersion int
	if err := tx.QueryRow(ctx, `select pg_catalog.current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		return "", "", databaseIdentity{}, fmt.Errorf("reading PostgreSQL server version: %w", err)
	}
	if err := validatePostgresVersion(serverVersion); err != nil {
		return "", "", databaseIdentity{}, err
	}
	facts, err := layout.Classify(ctx, tx)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	if facts.State != layout.StateV1 {
		return "", "", databaseIdentity{}, fmt.Errorf("preflight requires a version 1 layout, found %s", facts.State)
	}
	digest, err := manifest.CanonicalDigest()
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	databaseGraphs, err := graphFactsQuery(ctx, tx)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	if err := validateManifestGraphs(manifest, databaseGraphs); err != nil {
		return "", "", databaseIdentity{}, err
	}
	if err := validateManifestEdgeConstraints(ctx, tx, manifest, databaseGraphs); err != nil {
		return "", "", databaseIdentity{}, err
	}
	identity, err := currentDatabaseIdentity(ctx, tx)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	catalogSnapshot, err := catalog.ReadSnapshot(ctx, tx)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	catalogDigest, err := catalog.FingerprintWithExclusions(catalogSnapshot, "public.dawgs_operation_confirmation")
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	writers, err := s.authenticateWriterProbes(ctx, identity, false)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	roles, err := s.preflightRoleFacts(ctx, tx, identity, writers)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	factsDigest, err := canonicalFactsDigest(digest, catalogDigest, databaseGraphs, identity, writers, roles)
	if err != nil {
		return "", "", databaseIdentity{}, err
	}
	return digest, factsDigest, identity, nil
}

func validatePostgresVersion(serverVersion int) error {
	major := serverVersion / 10_000
	if major != 16 && major != 18 {
		return fmt.Errorf("unsupported_postgres_major: PostgreSQL %d is not admitted for migration; supported majors are 16 and 18", major)
	}
	return nil
}

// ConfirmMigration re-runs the coherent preflight inspection and issues a
// short-lived, one-use migration confirmation bound to those current facts.
// Keeping this mutation separate makes Preflight itself safe to repeat and
// suitable for read-only operator credentials.
func (s *Engine) ConfirmMigration(ctx context.Context, manifest Manifest) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	manifestDigest, factsDigest, identity, err := s.inspectPreflight(ctx, tx, manifest)
	if err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, confirmationDDL); err != nil {
		return Report{}, fmt.Errorf("installing confirmation journal: %w", err)
	}
	token, err := issueConfirmation(ctx, tx, "migrate", manifestDigest, factsDigest, identity)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "confirm", State: "ready", ManifestSHA256: manifestDigest,
		PreflightDigest: factsDigest, ConfirmationToken: token, CreatedAt: time.Now().UTC()}, nil
}

func validateManifestGraphs(manifest Manifest, databaseGraphs []graphFact) error {
	manifestGraphs := map[string]ManifestGraph{}
	for _, graph := range manifest.Graphs {
		manifestGraphs[graph.Name] = graph
	}
	for _, databaseGraph := range databaseGraphs {
		declared, found := manifestGraphs[databaseGraph.Name]
		if !found {
			return fmt.Errorf("materialized graph %q is missing from manifest", databaseGraph.Name)
		}
		declaredKinds := map[string]struct{}{}
		for _, kind := range declared.EdgeKinds {
			declaredKinds[kind] = struct{}{}
		}
		for _, kind := range databaseGraph.StoredKinds {
			if _, found := declaredKinds[kind]; !found {
				return fmt.Errorf("stored edge kind %q in graph %q is omitted from manifest", kind, databaseGraph.Name)
			}
		}
		delete(manifestGraphs, databaseGraph.Name)
	}
	if len(manifestGraphs) > 0 {
		names := make([]string, 0, len(manifestGraphs))
		for name := range manifestGraphs {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("manifest contains unmaterialized graphs not listed as excluded: %s", strings.Join(names, ", "))
	}
	return nil
}

func (s *Engine) prepareCutover(ctx context.Context, tx pgx.Tx, manifest Manifest, graphFacts []graphFact, migrationID, manifestDigest string) (Report, error) {
	for _, graph := range graphFacts {
		partition := pgx.Identifier{"public", model.EdgePartitionTableName(graph.ID)}.Sanitize()
		legacy := pgx.Identifier{model.EdgePartitionTableName(graph.ID) + "_v1_legacy"}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`alter table public.edge detach partition %s; alter table %s rename to %s`, partition, partition, legacy)); err != nil {
			return Report{}, fmt.Errorf("detaching graph %d: %w", graph.ID, err)
		}
	}
	if _, err := tx.Exec(ctx, `alter table public.edge drop constraint edge_pkey; alter table public.edge add primary key (id, graph_id, kind_id)`); err != nil {
		return Report{}, fmt.Errorf("upgrading edge root identity key: %w", err)
	}
	if _, err := tx.Exec(ctx, schema.Up()); err != nil {
		return Report{}, fmt.Errorf("installing V2 schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into public.dawgs_schema_migration (migration_id,manifest_sha256,state) values ($1,$2,'copying')`, migrationID, manifestDigest); err != nil {
		return Report{}, err
	}
	manifestByName := map[string]ManifestGraph{}
	for _, graph := range manifest.Graphs {
		manifestByName[graph.Name] = graph
	}
	for _, graph := range graphFacts {
		parentName := model.EdgePartitionTableName(graph.ID)
		parent := pgx.Identifier{"public", parentName}.Sanitize()
		nodePartition := pgx.Identifier{"public", model.NodePartitionTableName(graph.ID)}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`drop trigger if exists delete_node_edges on %s; create trigger delete_node_edges after delete on %s referencing old table as deleted_nodes for each statement execute function public.delete_node_edges()`, nodePartition, nodePartition)); err != nil {
			return Report{}, fmt.Errorf("installing graph-safe node cascade for graph %d: %w", graph.ID, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`create table %s partition of public.edge for values in (%d) partition by list (kind_id)`, parent, graph.ID)); err != nil {
			return Report{}, err
		}
		kindIDs, err := ensureKindIDs(ctx, tx, manifestByName[graph.Name].EdgeKinds)
		if err != nil {
			return Report{}, err
		}
		legacyName := parentName + "_v1_legacy"
		legacy := pgx.Identifier{"public", legacyName}.Sanitize()
		rows, err := tx.Query(ctx, fmt.Sprintf(`select distinct kind_id from %s order by kind_id`, legacy))
		if err != nil {
			return Report{}, err
		}
		for rows.Next() {
			var kindID int16
			if err := rows.Scan(&kindID); err != nil {
				rows.Close()
				return Report{}, err
			}
			kindIDs[kindID] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Report{}, err
		}
		ordered := make([]int, 0, len(kindIDs))
		for kindID := range kindIDs {
			ordered = append(ordered, int(kindID))
		}
		sort.Ints(ordered)
		for _, rawKindID := range ordered {
			kindID := int16(rawKindID)
			leaf := pgx.Identifier{"public", model.EdgeKindPartitionTableName(graph.ID, kindID)}.Sanitize()
			if _, err := tx.Exec(ctx, `insert into public.dawgs_edge_kind_leaf (graph_id, kind_id, provenance) values ($1,$2,array['migrated']::text[]) on conflict do nothing`, graph.ID, kindID); err != nil {
				return Report{}, err
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf(`create table %s partition of %s for values in (%d)`, leaf, parent, kindID)); err != nil {
				return Report{}, err
			}
		}
		var sourceCount int64
		var sourceDigest string
		var legacyOID uint32
		if err := tx.QueryRow(ctx, fmt.Sprintf(`select count(*),
md5(coalesce(string_agg(md5(pg_catalog.jsonb_build_array(id,graph_id,start_id,end_id,kind_id,properties)::text),'' order by id,kind_id),'')),%s::pg_catalog.regclass::oid from %s t`,
			quoteLiteral("public."+legacyName), legacy)).Scan(&sourceCount, &sourceDigest, &legacyOID); err != nil {
			return Report{}, err
		}
		if _, err := tx.Exec(ctx, `insert into public.dawgs_schema_migration_graph
(migration_id,graph_id,phase,legacy_relation,legacy_oid,source_count,source_digest)
values ($1,$2,'prepared',$3,$4,$5,$6)`, migrationID, graph.ID, legacyName, legacyOID, sourceCount, sourceDigest); err != nil {
			return Report{}, err
		}
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='copying',updated_at=pg_catalog.clock_timestamp() where migration_id=$1`, migrationID); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_layout l set state='v2_migrating', active_migration_id=$1,
accepted_migration_id=null,accepted_scope_kind=null,accepted_scope_id=null,accepted_at=null,
identity_epoch=f.identity_epoch,current_rebind_receipt_digest=f.current_rebind_receipt_digest
from public.dawgs_schema_fence_migration f where l.singleton and f.migration_id=$1`, migrationID); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='copying',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state='fence_verified'`, migrationID); err != nil {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "42P01" {
			return Report{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	if err := s.hitFailpoint("after_prepare_commit"); err != nil {
		return Report{Version: 1, Action: "migrate", State: "v2_migrating", MigrationID: migrationID, ManifestSHA256: manifestDigest, CreatedAt: time.Now().UTC()}, err
	}
	report, err := s.resumeInternal(ctx, migrationID)
	if err != nil {
		return Report{Version: 1, Action: "migrate", State: "v2_migrating", MigrationID: migrationID, ManifestSHA256: manifestDigest, CreatedAt: time.Now().UTC()}, err
	}
	report.Action = "migrate"
	report.ManifestSHA256 = manifestDigest
	return report, nil
}

func reconcileSequences(ctx context.Context, tx pgx.Tx) error {
	for _, item := range []struct {
		sequence string
		relation string
	}{
		{"node_id_seq", "node"},
		{"edge_id_seq", "edge"},
	} {
		var lastValue, maximum int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`select last_value,(select coalesce(max(id),0) from public.%s) from public.%s`,
			item.relation, item.sequence)).Scan(&lastValue, &maximum); err != nil {
			return fmt.Errorf("reading %s reconciliation facts: %w", item.sequence, err)
		}
		next := lastValue + 1
		if maximum >= next {
			next = maximum + 1
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`alter sequence public.%s restart with %d`, item.sequence, next)); err != nil {
			return fmt.Errorf("reconciling %s: %w", item.sequence, err)
		}
	}
	return nil
}

type copyCheckpoint struct {
	GraphID        int32
	LegacyRelation string
	LegacyOID      uint32
	SourceCount    int64
	SourceDigest   string
}

func (s *Engine) Resume(ctx context.Context, migrationID, confirmation string) (Report, error) {
	var state string
	if err := s.Pool.QueryRow(ctx, `select state from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(&state); err != nil {
		return Report{}, err
	}
	if state == "fence_installed_unverified" || state == "fence_database_verified_retirement_pending" || state == "writer_retirement_in_progress" {
		return s.resumeInternal(ctx, migrationID)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if err := consumeOperationConfirmation(ctx, tx, "resume", migrationID, confirmation); err != nil {
		return Report{}, err
	}
	facts := struct {
		Version int    `json:"version"`
		State   string `json:"state"`
	}{1, state}
	if _, err := appendReceipt(ctx, tx, "migration", migrationID, "resume_intent",
		confirmationAttemptID(confirmation), facts, confirmation); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return s.resumeInternal(ctx, migrationID)
}

func (s *Engine) resumeInternal(ctx context.Context, migrationID string) (Report, error) {
	var fenceState string
	var encodedManifest []byte
	var fencedManifestDigest string
	fenceErr := s.Pool.QueryRow(ctx, `select state,manifest,manifest_sha256 from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).
		Scan(&fenceState, &encodedManifest, &fencedManifestDigest)
	if fenceErr == nil {
		switch fenceState {
		case "fence_installed_unverified":
			return Report{}, fmt.Errorf("writer_probe_required: migration %q must pass direct database fence probes", migrationID)
		case "fence_database_verified_retirement_pending":
			return Report{}, fmt.Errorf("credential_retirement_required: migration %q has not retired its writer credentials", migrationID)
		case "fence_verified":
			return s.startFencedCutover(ctx, migrationID, encodedManifest, fencedManifestDigest)
		}
	} else if !errors.Is(fenceErr, pgx.ErrNoRows) {
		var pgError *pgconn.PgError
		if !errors.As(fenceErr, &pgError) || pgError.Code != "42P01" {
			return Report{}, fenceErr
		}
	}
	rows, err := s.Pool.Query(ctx, `select graph_id,legacy_relation,legacy_oid,source_count,source_digest
from public.dawgs_schema_migration_graph where migration_id=$1 and phase='prepared' order by graph_id`, migrationID)
	if err != nil {
		return Report{}, err
	}
	var checkpoints []copyCheckpoint
	for rows.Next() {
		var checkpoint copyCheckpoint
		if err := rows.Scan(&checkpoint.GraphID, &checkpoint.LegacyRelation, &checkpoint.LegacyOID,
			&checkpoint.SourceCount, &checkpoint.SourceDigest); err != nil {
			rows.Close()
			return Report{}, err
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Report{}, err
	}

	for _, checkpoint := range checkpoints {
		if err := s.copyGraphCheckpoint(ctx, migrationID, checkpoint); err != nil {
			return Report{}, err
		}
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	var remaining int64
	if err := tx.QueryRow(ctx, `select count(*) from public.dawgs_schema_migration_graph
where migration_id=$1 and phase<>'copied'`, migrationID).Scan(&remaining); err != nil {
		return Report{}, err
	}
	if remaining != 0 {
		return Report{}, fmt.Errorf("migration %q has %d incomplete graph checkpoints", migrationID, remaining)
	}
	if err := reconcileSequences(ctx, tx); err != nil {
		return Report{}, err
	}
	var encodedManifestForConstraints []byte
	if err := tx.QueryRow(ctx, `select manifest from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(&encodedManifestForConstraints); err != nil {
		return Report{}, err
	}
	var manifestForConstraints Manifest
	if err := json.Unmarshal(encodedManifestForConstraints, &manifestForConstraints); err != nil {
		return Report{}, err
	}
	if err := installManifestEdgeConstraints(ctx, tx, manifestForConstraints); err != nil {
		return Report{}, err
	}
	tag, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='pending_validation',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state in ('copying','pending_validation')`, migrationID)
	if err != nil {
		return Report{}, err
	}
	if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("migration %q is not resumable", migrationID)
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_layout set state='v2_pending_acceptance'
where singleton and state in ('v2_migrating','v2_pending_acceptance') and active_migration_id=$1`, migrationID); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='pending_validation',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state in ('copying','pending_validation')`, migrationID); err != nil {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "42P01" {
			return Report{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "resume", State: "v2_pending_acceptance", MigrationID: migrationID, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) startFencedCutover(ctx context.Context, migrationID string, encodedManifest []byte, expectedManifestDigest string) (Report, error) {
	var manifest Manifest
	if err := json.Unmarshal(encodedManifest, &manifest); err != nil {
		return Report{}, fmt.Errorf("decoding journaled manifest: %w", err)
	}
	manifestDigest, err := manifest.CanonicalDigest()
	if err != nil {
		return Report{}, err
	}
	if manifestDigest != expectedManifestDigest {
		return Report{}, fmt.Errorf("journaled manifest digest mismatch")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	var state string
	if err := tx.QueryRow(ctx, `select state from public.dawgs_schema_fence_migration where migration_id=$1 for update`, migrationID).Scan(&state); err != nil {
		return Report{}, err
	}
	if state != "fence_verified" {
		return Report{}, fmt.Errorf("migration %q is no longer fence_verified", migrationID)
	}
	graphs, err := graphFactsQuery(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := validateManifestGraphs(manifest, graphs); err != nil {
		return Report{}, err
	}
	var sourceKey string
	if err := tx.QueryRow(ctx, `select pg_catalog.pg_get_constraintdef(c.oid,true)
from pg_catalog.pg_constraint c where c.conrelid='public.edge'::pg_catalog.regclass and c.contype='p'`).Scan(&sourceKey); err != nil {
		return Report{}, err
	}
	if sourceKey != "PRIMARY KEY (id, graph_id)" {
		return Report{}, fmt.Errorf("fenced V1 source identity key changed: %s", sourceKey)
	}
	return s.prepareCutover(ctx, tx, manifest, graphs, migrationID, manifestDigest)
}

func (s *Engine) copyGraphCheckpoint(ctx context.Context, migrationID string, checkpoint copyCheckpoint) error {
	legacy := pgx.Identifier{"public", checkpoint.LegacyRelation}.Sanitize()
	parent := pgx.Identifier{"public", model.EdgePartitionTableName(checkpoint.GraphID)}.Sanitize()
	chunkSize := s.CopyChunkSize
	if chunkSize <= 0 {
		chunkSize = 10_000
	}
	for {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_catalog.pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
			tx.Rollback(ctx)
			return err
		}
		var currentOID uint32
		if err := tx.QueryRow(ctx, `select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relname=$1`, checkpoint.LegacyRelation).Scan(&currentOID); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("resolving legacy relation for graph %d: %w", checkpoint.GraphID, err)
		}
		if currentOID != checkpoint.LegacyOID {
			tx.Rollback(ctx)
			return fmt.Errorf("legacy relation identity changed for graph %d: expected OID %d, found %d", checkpoint.GraphID, checkpoint.LegacyOID, currentOID)
		}
		var phase string
		var highWater *int64
		var copiedCount int64
		if err := tx.QueryRow(ctx, `select phase,high_water,copied_count from public.dawgs_schema_migration_graph
where migration_id=$1 and graph_id=$2 for update`, migrationID, checkpoint.GraphID).Scan(&phase, &highWater, &copiedCount); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if phase == "copied" {
			tx.Rollback(ctx)
			return nil
		}
		if phase != "prepared" {
			tx.Rollback(ctx)
			return fmt.Errorf("graph %d has non-copyable phase %q", checkpoint.GraphID, phase)
		}
		var targetRows int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`select count(*) from %s`, parent)).Scan(&targetRows); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if targetRows != copiedCount {
			tx.Rollback(ctx)
			return fmt.Errorf("graph %d target row count %d disagrees with durable copied count %d", checkpoint.GraphID, targetRows, copiedCount)
		}
		var inserted int64
		var newHighWater *int64
		copySQL := fmt.Sprintf(`with chunk as materialized (
  select * from %s where ($1::bigint is null or id>$1) order by id limit $2
), inserted as (
  insert into %s select * from chunk returning id
)
select count(*),max(id) from inserted`, legacy, parent)
		if err := tx.QueryRow(ctx, copySQL, highWater, chunkSize).Scan(&inserted, &newHighWater); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("copying graph %d chunk: %w", checkpoint.GraphID, err)
		}
		if inserted == 0 {
			sourceCount, sourceDigest, err := relationDigest(ctx, tx, legacy)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			targetCount, targetDigest, err := relationDigest(ctx, tx, parent)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			if sourceCount != checkpoint.SourceCount || sourceDigest != checkpoint.SourceDigest || targetCount != sourceCount || targetDigest != sourceDigest {
				tx.Rollback(ctx)
				return fmt.Errorf("graph %d checkpoint mismatch: source %d/%s target %d/%s",
					checkpoint.GraphID, sourceCount, sourceDigest, targetCount, targetDigest)
			}
			tag, err := tx.Exec(ctx, `update public.dawgs_schema_migration_graph
set phase='copied',target_count=$3,target_digest=$4,copied_at=pg_catalog.clock_timestamp()
where migration_id=$1 and graph_id=$2 and phase='prepared'`, migrationID, checkpoint.GraphID, targetCount, targetDigest)
			if err != nil || tag.RowsAffected() != 1 {
				tx.Rollback(ctx)
				if err != nil {
					return err
				}
				return fmt.Errorf("graph %d checkpoint was concurrently changed", checkpoint.GraphID)
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit_outcome_unknown for graph %d completion: %w", checkpoint.GraphID, err)
			}
			if err := s.hitFailpoint(fmt.Sprintf("after_graph_%d_commit", checkpoint.GraphID)); err != nil {
				return err
			}
			return nil
		}
		if newHighWater == nil {
			tx.Rollback(ctx)
			return fmt.Errorf("graph %d copied rows without a high-water value", checkpoint.GraphID)
		}
		tag, err := tx.Exec(ctx, `update public.dawgs_schema_migration_graph
set high_water=$3,copied_count=copied_count+$4 where migration_id=$1 and graph_id=$2 and phase='prepared'`,
			migrationID, checkpoint.GraphID, *newHighWater, inserted)
		if err != nil || tag.RowsAffected() != 1 {
			tx.Rollback(ctx)
			if err != nil {
				return err
			}
			return fmt.Errorf("graph %d chunk checkpoint was concurrently changed", checkpoint.GraphID)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit_outcome_unknown for graph %d chunk: %w", checkpoint.GraphID, err)
		}
		if err := s.hitFailpoint(fmt.Sprintf("after_graph_%d_chunk_commit", checkpoint.GraphID)); err != nil {
			return err
		}
	}
}

func (s *Engine) hitFailpoint(name string) error {
	if s.Failpoint != nil {
		return s.Failpoint(name)
	}
	return nil
}

func relationDigest(ctx context.Context, tx pgx.Tx, relation string) (int64, string, error) {
	var count int64
	var digest string
	if err := tx.QueryRow(ctx, fmt.Sprintf(`select count(*),md5(coalesce(string_agg(
md5(pg_catalog.jsonb_build_array(id,graph_id,start_id,end_id,kind_id,properties)::text),'' order by id,kind_id),'')) from %s t`, relation)).Scan(&count, &digest); err != nil {
		return 0, "", err
	}
	return count, digest, nil
}

func validateExactEdgeEquality(ctx context.Context, tx pgx.Tx, source, target string, graphID int32) error {
	var differs bool
	query := fmt.Sprintf(`select exists(
  (select id,graph_id,start_id,end_id,kind_id,properties from %s
   except all
   select id,graph_id,start_id,end_id,kind_id,properties from %s)
  union all
  (select id,graph_id,start_id,end_id,kind_id,properties from %s
   except all
   select id,graph_id,start_id,end_id,kind_id,properties from %s))`, source, target, target, source)
	if err := tx.QueryRow(ctx, query).Scan(&differs); err != nil {
		return err
	}
	if differs {
		return fmt.Errorf("graph %d fails exact bidirectional V1/V2 row equality", graphID)
	}
	return nil
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (s *Engine) Validate(ctx context.Context, migrationID string) (Report, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	var migrationState, layoutState, activeMigration string
	if err := tx.QueryRow(ctx, `select m.state,l.state,coalesce(l.active_migration_id,'')
from public.dawgs_schema_migration m cross join public.dawgs_schema_layout l
where m.migration_id=$1 and l.singleton`, migrationID).Scan(&migrationState, &layoutState, &activeMigration); err != nil {
		return Report{}, fmt.Errorf("loading validation scope: %w", err)
	}
	if (migrationState != "pending_validation" && migrationState != "validated") || layoutState != "v2_pending_acceptance" || activeMigration != migrationID {
		return Report{}, fmt.Errorf("migration %q is not the active pending-validation scope", migrationID)
	}
	rows, err := tx.Query(ctx, `select graph_id,legacy_relation,legacy_oid,source_count,target_count,source_digest,target_digest
from public.dawgs_schema_migration_graph where migration_id=$1 and phase='copied' order by graph_id`, migrationID)
	if err != nil {
		return Report{}, err
	}
	type graphCheckpoint struct {
		graphID      int32
		legacy       string
		legacyOID    uint32
		source       int64
		target       int64
		sourceDigest string
		targetDigest string
	}
	checkpoints := make([]graphCheckpoint, 0)
	for rows.Next() {
		var checkpoint graphCheckpoint
		if err := rows.Scan(&checkpoint.graphID, &checkpoint.legacy, &checkpoint.legacyOID,
			&checkpoint.source, &checkpoint.target, &checkpoint.sourceDigest, &checkpoint.targetDigest); err != nil {
			rows.Close()
			return Report{}, err
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Report{}, err
	}

	hash := sha256.New()
	for _, checkpoint := range checkpoints {
		legacy := pgx.Identifier{"public", checkpoint.legacy}.Sanitize()
		parent := pgx.Identifier{"public", model.EdgePartitionTableName(checkpoint.graphID)}.Sanitize()
		var currentOID uint32
		if err := tx.QueryRow(ctx, `select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relname=$1`, checkpoint.legacy).Scan(&currentOID); err != nil {
			return Report{}, err
		}
		if currentOID != checkpoint.legacyOID {
			return Report{}, fmt.Errorf("graph %d retained source OID changed", checkpoint.graphID)
		}
		sourceCount, sourceDigest, err := relationDigest(ctx, tx, legacy)
		if err != nil {
			return Report{}, err
		}
		targetCount, targetDigest, err := relationDigest(ctx, tx, parent)
		if err != nil {
			return Report{}, err
		}
		if sourceCount != checkpoint.source || sourceDigest != checkpoint.sourceDigest {
			return Report{}, fmt.Errorf("graph %d source changed after checkpoint", checkpoint.graphID)
		}
		if targetCount != checkpoint.target || targetDigest != checkpoint.targetDigest || targetDigest != sourceDigest {
			return Report{}, fmt.Errorf("graph %d target changed or differs from source", checkpoint.graphID)
		}
		if err := validateExactEdgeEquality(ctx, tx, legacy, parent, checkpoint.graphID); err != nil {
			return Report{}, err
		}
		if err := validateGraphTopology(ctx, tx, checkpoint.graphID); err != nil {
			return Report{}, err
		}
		var identityDrift int64
		if err := tx.QueryRow(ctx, `select count(*) from (
select coalesce(e.id,i.edge_id) from public.edge e full join public.edge_identity i
on i.graph_id=e.graph_id and i.edge_id=e.id
where coalesce(e.graph_id,i.graph_id)=$1 and
(e.id is null or i.edge_id is null or e.kind_id<>i.kind_id)) drift`, checkpoint.graphID).Scan(&identityDrift); err != nil {
			return Report{}, err
		}
		if identityDrift != 0 {
			return Report{}, fmt.Errorf("graph %d has %d edge identity registry mismatches", checkpoint.graphID, identityDrift)
		}
		fmt.Fprintf(hash, "%d:%d:%s:%d;", checkpoint.graphID, targetCount, targetDigest, currentOID)
	}
	if err := validateGlobalRegistriesAndSequences(ctx, tx); err != nil {
		return Report{}, err
	}
	if len(checkpoints) == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from public.dawgs_schema_migration where migration_id=$1)`, migrationID).Scan(&exists); err != nil {
			return Report{}, err
		}
		if !exists {
			return Report{}, fmt.Errorf("migration %q does not exist", migrationID)
		}
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err := tx.Exec(ctx, `update public.dawgs_schema_migration set state='validated',validation_digest=$2,updated_at=pg_catalog.clock_timestamp() where migration_id=$1`, migrationID, digest); err != nil {
		return Report{}, err
	}
	if tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration set state='validated',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state in ('pending_validation','validated')`, migrationID); err != nil {
		return Report{}, err
	} else if tag.RowsAffected() != 1 {
		return Report{}, fmt.Errorf("migration %q fence journal is not pending validation", migrationID)
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	return Report{Version: 1, Action: "validate", State: "validated", MigrationID: migrationID, ValidationDigest: digest, CreatedAt: time.Now().UTC()}, nil
}

func validateGlobalRegistriesAndSequences(ctx context.Context, tx pgx.Tx) error {
	var propertyClaimDrift int64
	if err := tx.QueryRow(ctx, `select count(*) from (
with expected as (
  select c.graph_id,c.constraint_id,e.properties->>c.property_name property_value,e.id edge_id
  from public.edge_property_constraint c join public.edge e on e.graph_id=c.graph_id
  where c.lifecycle_state='active' and e.properties->>c.property_name is not null
), actual as (
  select u.graph_id,u.constraint_id,u.property_value,u.edge_id
  from public.edge_property_unique_value u join public.edge_property_constraint c
    on c.graph_id=u.graph_id and c.constraint_id=u.constraint_id
  where c.lifecycle_state='active'
)
select coalesce(e.graph_id,a.graph_id) from expected e full join actual a
using(graph_id,constraint_id,property_value)
where e.edge_id is null or a.edge_id is null or e.edge_id<>a.edge_id
union all
select u.graph_id from public.edge_property_unique_value u join public.edge_property_constraint c
on c.graph_id=u.graph_id and c.constraint_id=u.constraint_id where c.lifecycle_state<>'active'
	) drift`).Scan(&propertyClaimDrift); err != nil {
		return err
	}
	if propertyClaimDrift != 0 {
		return fmt.Errorf("property constraint registry has %d claim mismatches", propertyClaimDrift)
	}
	for _, item := range []struct{ sequence, relation string }{{"node_id_seq", "node"}, {"edge_id_seq", "edge"}} {
		var lastValue, maximum int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`select last_value,(select coalesce(max(id),0) from public.%s) from public.%s`,
			item.relation, item.sequence)).Scan(&lastValue, &maximum); err != nil {
			return err
		}
		if lastValue <= maximum {
			return fmt.Errorf("sequence %s lags maximum %s ID: %d <= %d", item.sequence, item.relation, lastValue, maximum)
		}
	}
	return nil
}

func validateGraphTopology(ctx context.Context, tx pgx.Tx, graphID int32) error {
	parentName := model.EdgePartitionTableName(graphID)
	var physicalCount, registeredCount int64
	if err := tx.QueryRow(ctx, `select
(select count(*) from pg_catalog.pg_inherits i join pg_catalog.pg_class p on p.oid=i.inhparent
 join pg_catalog.pg_namespace n on n.oid=p.relnamespace where n.nspname='public' and p.relname=$1),
(select count(*) from public.dawgs_edge_kind_leaf where graph_id=$2 and lifecycle_state='active')`,
		parentName, graphID).Scan(&physicalCount, &registeredCount); err != nil {
		return err
	}
	if physicalCount != registeredCount {
		return fmt.Errorf("graph %d leaf topology count drift: physical %d registry %d", graphID, physicalCount, registeredCount)
	}
	rows, err := tx.Query(ctx, `select kind_id from public.dawgs_edge_kind_leaf
where graph_id=$1 and lifecycle_state='active' order by kind_id`, graphID)
	if err != nil {
		return err
	}
	var kindIDs []int16
	for rows.Next() {
		var kindID int16
		if err := rows.Scan(&kindID); err != nil {
			rows.Close()
			return err
		}
		kindIDs = append(kindIDs, kindID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, kindID := range kindIDs {
		leafName := model.EdgeKindPartitionTableName(graphID, kindID)
		var bound string
		if err := tx.QueryRow(ctx, `select pg_catalog.pg_get_expr(c.relpartbound,c.oid)
from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
join pg_catalog.pg_inherits i on i.inhrelid=c.oid join pg_catalog.pg_class p on p.oid=i.inhparent
where n.nspname='public' and c.relname=$1 and p.relname=$2`, leafName, parentName).Scan(&bound); err != nil {
			return fmt.Errorf("graph %d kind %d leaf missing or detached: %w", graphID, kindID, err)
		}
		expected := fmt.Sprintf("FOR VALUES IN ('%d')", kindID)
		if bound != expected {
			return fmt.Errorf("graph %d kind %d has unexpected partition bound %q", graphID, kindID, bound)
		}
	}
	return nil
}

func (s *Engine) Rollback(ctx context.Context, migrationID, confirmation string) (Report, error) {
	if !s.AllowUnreleasedDisposable {
		return Report{}, fmt.Errorf("activation_not_ready: unreleased rollback is restricted to the identity-guarded disposable test harness")
	}
	var fenceState string
	fenceErr := s.Pool.QueryRow(ctx, `select state from public.dawgs_schema_fence_migration where migration_id=$1`, migrationID).Scan(&fenceState)
	if fenceErr == nil && (fenceState == "fence_installed_unverified" || fenceState == "fence_database_verified_retirement_pending" || fenceState == "fence_verified") {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return Report{}, err
		}
		defer tx.Rollback(ctx)
		if err := consumeOperationConfirmation(ctx, tx, "rollback", migrationID, confirmation); err != nil {
			return Report{}, err
		}
		facts := struct {
			Version int    `json:"version"`
			State   string `json:"state"`
			From    string `json:"from"`
		}{1, "rolled_back_handoff_pending", fenceState}
		receipt, err := appendReceipt(ctx, tx, "migration", migrationID, "rollback",
			confirmationAttemptID(confirmation), facts, confirmation)
		if err != nil {
			return Report{}, err
		}
		tag, err := tx.Exec(ctx, `update public.dawgs_schema_fence_migration
set state='rolled_back_handoff_pending',updated_at=pg_catalog.clock_timestamp()
where migration_id=$1 and state=$2`, migrationID, fenceState)
		if err != nil {
			return Report{}, err
		}
		if tag.RowsAffected() != 1 {
			return Report{}, fmt.Errorf("rollback fence state changed concurrently")
		}
		if err := s.hitFailpoint("before_rollback_commit"); err != nil {
			return Report{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
		}
		if err := s.hitFailpoint("after_rollback_commit"); err != nil {
			return Report{}, err
		}
		return Report{Version: 1, Action: "rollback", State: "rolled_back_handoff_pending",
			MigrationID: migrationID, ReceiptDigest: receipt.Digest, CreatedAt: time.Now().UTC()}, nil
	}
	if fenceErr != nil && !errors.Is(fenceErr, pgx.ErrNoRows) {
		return Report{}, fenceErr
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return Report{}, err
	}
	if err := consumeOperationConfirmation(ctx, tx, "rollback", migrationID, confirmation); err != nil {
		return Report{}, err
	}
	var state, active string
	if err := tx.QueryRow(ctx, `select state,coalesce(active_migration_id,'') from public.dawgs_schema_layout where singleton`).Scan(&state, &active); err != nil {
		return Report{}, err
	}
	if state == "v2_accepted" {
		return Report{}, fmt.Errorf("post-acceptance in-place rollback is refused")
	}
	if state != "v2_migrating" && state != "v2_pending_acceptance" {
		return Report{}, fmt.Errorf("rollback is refused from layout state %q", state)
	}
	if active != migrationID {
		return Report{}, fmt.Errorf("migration %q is not the active rollback scope", migrationID)
	}
	if err := validateRollbackEligibility(ctx, tx, migrationID, fenceState); err != nil {
		return Report{}, err
	}
	rows, err := tx.Query(ctx, `select graph_id,legacy_relation from public.dawgs_schema_migration_graph where migration_id=$1 order by graph_id`, migrationID)
	if err != nil {
		return Report{}, err
	}
	type legacyGraph struct {
		ID       int32
		Relation string
	}
	var graphs []legacyGraph
	for rows.Next() {
		var graph legacyGraph
		if err := rows.Scan(&graph.ID, &graph.Relation); err != nil {
			rows.Close()
			return Report{}, err
		}
		graphs = append(graphs, graph)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `drop trigger if exists dawgs_10_claim_edge on public.edge; drop trigger if exists dawgs_20_update_edge_claims on public.edge; drop table if exists public.edge_property_unique_value; drop table if exists public.edge_property_constraint; drop table if exists public.edge_identity; drop table if exists public.dawgs_edge_kind_leaf`); err != nil {
		return Report{}, err
	}
	if err := s.hitFailpoint("after_rollback_drop_v2"); err != nil {
		return Report{}, err
	}
	for _, graph := range graphs {
		parent := pgx.Identifier{"public", model.EdgePartitionTableName(graph.ID)}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`drop table %s cascade`, parent)); err != nil {
			return Report{}, err
		}
	}
	if err := s.hitFailpoint("after_rollback_reattach"); err != nil {
		return Report{}, err
	}
	if _, err := tx.Exec(ctx, `alter table public.edge drop constraint edge_pkey;
alter table public.edge add primary key(id,graph_id);
alter sequence public.graph_id_seq maxvalue 9223372036854775807`); err != nil {
		return Report{}, err
	}
	for _, graph := range graphs {
		legacy := pgx.Identifier{"public", graph.Relation}.Sanitize()
		restoredName := model.EdgePartitionTableName(graph.ID)
		restored := pgx.Identifier{"public", restoredName}.Sanitize()
		if _, err := tx.Exec(ctx, fmt.Sprintf(`alter table %s rename to %s; alter table public.edge attach partition %s for values in (%d)`, legacy, pgx.Identifier{restoredName}.Sanitize(), restored, graph.ID)); err != nil {
			return Report{}, err
		}
	}
	if _, err := tx.Exec(ctx, `drop function if exists public.dawgs_claim_edge(); drop function if exists public.dawgs_update_edge_claims()`); err != nil {
		return Report{}, err
	}
	if err := restoreFrozenV1Contract(ctx, tx); err != nil {
		return Report{}, err
	}
	facts := struct {
		Version int    `json:"version"`
		State   string `json:"state"`
		From    string `json:"from"`
	}{1, "rolled_back_handoff_pending", state}
	receipt, err := appendReceipt(ctx, tx, "migration", migrationID, "rollback", confirmationAttemptID(confirmation), facts, confirmation)
	if err != nil {
		return Report{}, err
	}
	for _, statement := range []string{
		`update public.dawgs_schema_migration set state='rolled_back',updated_at=pg_catalog.clock_timestamp() where migration_id=$1`,
		`update public.dawgs_schema_layout set state='v2_rolled_back',active_migration_id=null,accepted_migration_id=null where singleton and $1<>''`,
		`update public.dawgs_schema_fence_migration set state='rolled_back_handoff_pending',updated_at=pg_catalog.clock_timestamp() where migration_id=$1`,
	} {
		if _, err := tx.Exec(ctx, statement, migrationID); err != nil {
			return Report{}, err
		}
	}
	if err := s.hitFailpoint("before_rollback_commit"); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit_outcome_unknown: %w", err)
	}
	if err := s.hitFailpoint("after_rollback_commit"); err != nil {
		return Report{}, err
	}
	return Report{Version: 1, Action: "rollback", State: "rolled_back_handoff_pending", MigrationID: migrationID,
		ReceiptDigest: receipt.Digest, CreatedAt: time.Now().UTC()}, nil
}

func (s *Engine) graphFacts(ctx context.Context) ([]graphFact, error) {
	return graphFactsQuery(ctx, s.Pool)
}

type rowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func graphFactsQuery(ctx context.Context, queryer rowsQuerier) ([]graphFact, error) {
	invalidRows, err := queryer.Query(ctx, `select id::text from public.graph where id < 1 or id > 2147483647 order by id limit 1`)
	if err != nil {
		return nil, err
	}
	if invalidRows.Next() {
		var invalidGraphID string
		if err := invalidRows.Scan(&invalidGraphID); err != nil {
			invalidRows.Close()
			return nil, err
		}
		invalidRows.Close()
		return nil, fmt.Errorf("unsupported graph ID %s: expected positive int32", invalidGraphID)
	}
	if err := invalidRows.Err(); err != nil {
		invalidRows.Close()
		return nil, err
	}
	invalidRows.Close()

	rows, err := queryer.Query(ctx, `select g.id,g.name,coalesce(array_agg(distinct k.name order by k.name) filter(where k.name is not null),array[]::varchar[]) from public.graph g left join public.edge e on e.graph_id=g.id left join public.kind k on k.id=e.kind_id group by g.id,g.name order by g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []graphFact
	for rows.Next() {
		var fact graphFact
		if err := rows.Scan(&fact.ID, &fact.Name, &fact.StoredKinds); err != nil {
			return nil, err
		}
		if fact.ID <= 0 {
			return nil, fmt.Errorf("unsupported graph ID %d", fact.ID)
		}
		result = append(result, fact)
	}
	return result, rows.Err()
}
func ensureKindIDs(ctx context.Context, tx pgx.Tx, names []string) (map[int16]struct{}, error) {
	result := map[int16]struct{}{}
	for _, name := range names {
		var id int16
		if err := tx.QueryRow(ctx, `insert into public.kind(name) values($1) on conflict(name) do update set name=excluded.name returning id`, name).Scan(&id); err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	return result, nil
}
func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
