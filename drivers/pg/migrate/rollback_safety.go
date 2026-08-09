package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/model"
)

func validateRollbackEligibility(ctx context.Context, tx pgx.Tx, migrationID, fenceState string) error {
	if fenceState != "copying" && fenceState != "pending_validation" && fenceState != "validated" {
		return fmt.Errorf("rollback fence state %q is not a pre-acceptance cutover state", fenceState)
	}
	var acceptanceReceipts, unsafeWriters, targetWriteRoutes int64
	if err := tx.QueryRow(ctx, `select
  (select count(*) from public.dawgs_schema_receipt
    where scope_kind='migration' and scope_id=$1
      and receipt_type in ('acceptance','target_smoke')),
  (select count(*) from jsonb_to_recordset((select writer_facts from public.dawgs_schema_fence_migration where migration_id=$1))
      as w(role_name text)
    join pg_catalog.pg_roles r on r.rolname=w.role_name
    where r.rolcanlogin
      or pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
      or exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
        where n.nspname='public' and c.relkind in ('r','p')
          and pg_catalog.has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE'))
      or exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
        where n.nspname='public'
          and case when c.relkind='S' then pg_catalog.has_sequence_privilege(r.oid,c.oid,'USAGE,UPDATE') else false end)),
  (select count(*) from pg_catalog.pg_roles r
    where r.rolname=(select role_facts->>'target_name' from public.dawgs_schema_fence_migration where migration_id=$1)
      and (pg_catalog.has_schema_privilege(r.oid,'public','CREATE')
        or exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
          where n.nspname='public' and c.relkind in ('r','p')
            and pg_catalog.has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE'))
        or exists(select 1 from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
          where n.nspname='public'
            and case when c.relkind='S' then pg_catalog.has_sequence_privilege(r.oid,c.oid,'USAGE,UPDATE') else false end)))`, migrationID).Scan(
		&acceptanceReceipts, &unsafeWriters, &targetWriteRoutes); err != nil {
		return fmt.Errorf("checking rollback identity fence: %w", err)
	}
	if acceptanceReceipts != 0 {
		return fmt.Errorf("rollback_cutoff_passed: acceptance or target-smoke receipt already exists")
	}
	if unsafeWriters != 0 {
		return fmt.Errorf("writer_fence_drift: %d retired writer identities regained a write route or login", unsafeWriters)
	}
	if targetWriteRoutes != 0 {
		return fmt.Errorf("rollback_cutoff_passed: target runtime privileges were already granted")
	}

	rows, err := tx.Query(ctx, `select graph_id,legacy_relation,legacy_oid,source_count,source_digest,
phase,copied_count,coalesce(target_count,0),coalesce(target_digest,'')
from public.dawgs_schema_migration_graph where migration_id=$1 order by graph_id`, migrationID)
	if err != nil {
		return err
	}
	type checkpoint struct {
		graphID                               int32
		legacyRelation, sourceDigest, phase   string
		targetDigest                          string
		legacyOID                             uint32
		sourceCount, copiedCount, targetCount int64
	}
	var checkpoints []checkpoint
	for rows.Next() {
		var value checkpoint
		if err := rows.Scan(&value.graphID, &value.legacyRelation, &value.legacyOID, &value.sourceCount, &value.sourceDigest,
			&value.phase, &value.copiedCount, &value.targetCount, &value.targetDigest); err != nil {
			rows.Close()
			return err
		}
		checkpoints = append(checkpoints, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, value := range checkpoints {
		var currentOID uint32
		if err := tx.QueryRow(ctx, `select c.oid from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relname=$1`, value.legacyRelation).Scan(&currentOID); err != nil {
			return err
		}
		if currentOID != value.legacyOID {
			return fmt.Errorf("rollback source identity changed for graph %d", value.graphID)
		}
		legacy := pgx.Identifier{"public", value.legacyRelation}.Sanitize()
		currentSourceCount, currentSourceDigest, err := relationDigest(ctx, tx, legacy)
		if err != nil {
			return err
		}
		if currentSourceCount != value.sourceCount || currentSourceDigest != value.sourceDigest {
			return fmt.Errorf("rollback source data changed for graph %d", value.graphID)
		}
		parent := pgx.Identifier{"public", model.EdgePartitionTableName(value.graphID)}.Sanitize()
		currentTargetCount, currentTargetDigest, err := relationDigest(ctx, tx, parent)
		if err != nil {
			return err
		}
		if currentTargetCount != value.copiedCount {
			return fmt.Errorf("rollback staging row count changed for graph %d", value.graphID)
		}
		if value.phase == "copied" && (value.targetCount != currentTargetCount || value.targetDigest != currentTargetDigest || currentTargetDigest != value.sourceDigest) {
			return fmt.Errorf("rollback copied checkpoint changed for graph %d", value.graphID)
		}
	}
	return nil
}
