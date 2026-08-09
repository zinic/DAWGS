package migrate

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// frozenV1ContractSQL is the exact checksum-pinned V1 schema named by the
// migration manifest. Rollback replays it only after the retained V1
// partitions have been reattached, so CREATE OR REPLACE restores helper and
// trigger definitions without replacing application data.
//
//go:embed testdata/v1_schema.sql
var frozenV1ContractSQL string

func restoreFrozenV1Contract(ctx context.Context, tx pgx.Tx) error {
	// Remove V2-only runtime objects. Control-plane relations, confirmations,
	// receipts, and their append-only trigger deliberately survive rollback.
	if _, err := tx.Exec(ctx, `
drop trigger if exists dawgs_refuse_graph_delete on public.graph;
drop function if exists public.dawgs_refuse_graph_delete();
drop function if exists public.dawgs_current_smoke_receipt_valid(text,text,text);
drop function if exists public.dawgs_current_cleanup_receipt_valid(text,text,text);
drop function if exists public.dawgs_provision_graph(text);
drop function if exists public.dawgs_provision_edge_kind_leaf(integer,smallint);
drop function if exists public.dawgs_wipe_graph();
drop function if exists public.start_node(integer,public.edgeComposite);
drop function if exists public.end_node(integer,public.edgeComposite);
do $rollback$
declare relation_oid pg_catalog.oid;
begin
  for relation_oid in
    select i.inhrelid from pg_catalog.pg_inherits i
    where i.inhparent='public.node'::pg_catalog.regclass
  loop
    execute pg_catalog.format('drop trigger if exists delete_node_edges on %s',relation_oid::pg_catalog.regclass);
  end loop;
end
$rollback$;`); err != nil {
		return fmt.Errorf("removing V2-only runtime contract: %w", err)
	}
	if _, err := tx.Exec(ctx, `set local search_path=public,pg_catalog`); err != nil {
		return err
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, frozenV1ContractSQL).ReadAll(); err != nil {
		return fmt.Errorf("replaying frozen V1 contract: %w", err)
	}
	return validateFrozenV1Contract(ctx, tx)
}

func validateFrozenV1Contract(ctx context.Context, tx pgx.Tx) error {
	var primaryKey string
	if err := tx.QueryRow(ctx, `select pg_catalog.pg_get_constraintdef(c.oid,true)
from pg_catalog.pg_constraint c
where c.conrelid='public.edge'::pg_catalog.regclass and c.contype='p'`).Scan(&primaryKey); err != nil {
		return err
	}
	if primaryKey != "PRIMARY KEY (id, graph_id)" {
		return fmt.Errorf("frozen V1 edge key was not restored: %s", primaryKey)
	}
	var missingRequired, forbiddenV2, childCascadeTriggers int64
	if err := tx.QueryRow(ctx, `select
  (select count(*) from (values
    (pg_catalog.to_regprocedure('public.start_node(public.edgeComposite)')),
    (pg_catalog.to_regprocedure('public.end_node(public.edgeComposite)')),
    (pg_catalog.to_regprocedure('public.delete_node_edges()'))
  ) required(routine) where routine is null),
  (select count(*) from (values
    (pg_catalog.to_regprocedure('public.start_node(integer,public.edgeComposite)')),
    (pg_catalog.to_regprocedure('public.end_node(integer,public.edgeComposite)')),
    (pg_catalog.to_regprocedure('public.dawgs_provision_graph(text)')),
    (pg_catalog.to_regprocedure('public.dawgs_provision_edge_kind_leaf(integer,smallint)')),
    (pg_catalog.to_regprocedure('public.dawgs_wipe_graph()')),
    (pg_catalog.to_regprocedure('public.dawgs_refuse_graph_delete()')),
    (pg_catalog.to_regprocedure('public.dawgs_claim_edge()')),
    (pg_catalog.to_regprocedure('public.dawgs_update_edge_claims()'))
  ) forbidden(routine) where routine is not null),
  (select count(*) from pg_catalog.pg_trigger t
    join pg_catalog.pg_inherits i on i.inhrelid=t.tgrelid
    where i.inhparent='public.node'::pg_catalog.regclass
      and t.tgname='delete_node_edges' and not t.tgisinternal)`).Scan(
		&missingRequired, &forbiddenV2, &childCascadeTriggers); err != nil {
		return err
	}
	if missingRequired != 0 || forbiddenV2 != 0 || childCascadeTriggers != 0 {
		return fmt.Errorf("frozen V1 routine/trigger contract drift: missing=%d forbidden=%d child_triggers=%d",
			missingRequired, forbiddenV2, childCascadeTriggers)
	}
	var rootCascadeTriggers int64
	if err := tx.QueryRow(ctx, `select count(*) from pg_catalog.pg_trigger
where tgrelid='public.node'::pg_catalog.regclass and tgname='delete_node_edges' and not tgisinternal`).Scan(&rootCascadeTriggers); err != nil {
		return err
	}
	if rootCascadeTriggers != 1 {
		return fmt.Errorf("frozen V1 root cascade trigger count is %d, expected 1", rootCascadeTriggers)
	}
	return nil
}
