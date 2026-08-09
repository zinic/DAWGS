package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func validateManifestEdgeConstraints(ctx context.Context, tx pgx.Tx, manifest Manifest, graphs []graphFact) error {
	manifestByName := make(map[string]ManifestGraph, len(manifest.Graphs))
	for _, graph := range manifest.Graphs {
		manifestByName[graph.Name] = graph
	}
	for _, graph := range graphs {
		declared := manifestByName[graph.Name].EdgeConstraints
		relation := fmt.Sprintf("public.edge_%d", graph.ID)
		var actualCount int64
		if err := tx.QueryRow(ctx, `select count(*) from pg_catalog.pg_index i
join pg_catalog.pg_class c on c.oid=i.indexrelid
where i.indrelid=$1::pg_catalog.regclass and i.indisunique and not i.indisprimary and i.indexprs is not null`, relation).Scan(&actualCount); err != nil {
			return fmt.Errorf("inventorying V1 edge property constraints for graph %d: %w", graph.ID, err)
		}
		if actualCount != int64(len(declared)) {
			return fmt.Errorf("manifest graph %q declares %d edge property constraints but V1 catalog has %d", graph.Name, len(declared), actualCount)
		}
		for _, constraint := range declared {
			var valid bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from pg_catalog.pg_index i
join pg_catalog.pg_class c on c.oid=i.indexrelid
join pg_catalog.pg_am am on am.oid=c.relam
left join pg_catalog.pg_collation coll on coll.oid=i.indcollation[0]
where i.indrelid=$1::pg_catalog.regclass and c.relname=$2 and i.indisunique and i.indisvalid and i.indisready
  and not i.indisprimary and am.amname=$4::text
  and pg_catalog.pg_get_expr(i.indexprs,i.indrelid)=pg_catalog.format('(properties ->> %L::text)',$3::text)
  and coalesce(coll.collname,'default')=$5::text)`, relation, constraint.LogicalName, constraint.PropertyName,
				constraint.ConstraintType, constraint.CollationName).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return fmt.Errorf("manifest edge constraint %q for graph %q does not match a valid V1 catalog expression", constraint.LogicalName, graph.Name)
			}
		}
	}
	return nil
}

func installManifestEdgeConstraints(ctx context.Context, tx pgx.Tx, manifest Manifest) error {
	for _, graph := range manifest.Graphs {
		if len(graph.EdgeConstraints) == 0 {
			continue
		}
		var graphID int32
		if err := tx.QueryRow(ctx, `select id::integer from public.graph where name=$1`, graph.Name).Scan(&graphID); err != nil {
			return err
		}
		for _, constraint := range graph.EdgeConstraints {
			var constraintID int64
			var lifecycle, logicalName, constraintType, collationName string
			if err := tx.QueryRow(ctx, `insert into public.edge_property_constraint
(graph_id,logical_name,property_name,constraint_type,collation_name,lifecycle_state)
values($1,$2,$3,$4,$5,'pending')
on conflict(graph_id,property_name) do update set property_name=excluded.property_name
returning constraint_id,lifecycle_state,logical_name,constraint_type,collation_name`, graphID, constraint.LogicalName,
				constraint.PropertyName, constraint.ConstraintType, constraint.CollationName).Scan(
				&constraintID, &lifecycle, &logicalName, &constraintType, &collationName); err != nil {
				return fmt.Errorf("creating V2 edge property constraint %q: %w", constraint.LogicalName, err)
			}
			if logicalName != constraint.LogicalName || constraintType != constraint.ConstraintType || collationName != constraint.CollationName {
				return fmt.Errorf("existing V2 edge property constraint for %q has different semantics", constraint.PropertyName)
			}
			if lifecycle == "active" {
				continue
			}
			if lifecycle != "pending" {
				return fmt.Errorf("edge property constraint %q has non-installable lifecycle %q", constraint.LogicalName, lifecycle)
			}
			if _, err := tx.Exec(ctx, `insert into public.edge_property_unique_value
(graph_id,constraint_id,property_value,edge_id)
select $1,$2,e.properties->>$3,e.id from public.edge e
where e.graph_id=$1 and e.properties->>$3 is not null order by e.properties->>$3,e.id`,
				graphID, constraintID, constraint.PropertyName); err != nil {
				return fmt.Errorf("backfilling V2 edge property constraint %q: %w", constraint.LogicalName, err)
			}
			if tag, err := tx.Exec(ctx, `update public.edge_property_constraint set lifecycle_state='active'
where graph_id=$1 and constraint_id=$2 and lifecycle_state='pending'`, graphID, constraintID); err != nil {
				return err
			} else if tag.RowsAffected() != 1 {
				return fmt.Errorf("edge property constraint %q lifecycle changed during install", constraint.LogicalName)
			}
		}
	}
	return nil
}
