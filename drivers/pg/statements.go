package pg

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

func qualifiedManagedRelation(name string) string {
	return pgx.Identifier{"public", strings.TrimPrefix(name, "public.")}.Sanitize()
}

const (
	createNodeStatement               = `insert into %s (graph_id, kind_ids, properties) values (@graph_id, @kind_ids, @properties) returning (id, kind_ids, properties)::public.nodeComposite;`
	createNodeWithoutIDBatchStatement = `insert into %s (graph_id, kind_ids, properties) select $1, pg_catalog.unnest($2::text[])::int2[], pg_catalog.unnest($3::jsonb[])`
	createNodesReturningIDsStatement  = `with input as (
select ordinality, kind_ids, properties
from rows from (pg_catalog.unnest($2::text[]), pg_catalog.unnest($3::jsonb[]))
with ordinality as rows(kind_ids, properties, ordinality)
), prepared as materialized (
select ordinality, pg_catalog.nextval(pg_catalog.pg_get_serial_sequence('public.node', 'id')) as id, kind_ids::int2[] as kind_ids, properties
from input
), inserted as (
insert into %s (graph_id, id, kind_ids, properties)
select $1, id, kind_ids, properties from prepared order by ordinality
returning id
)
select prepared.ordinality, prepared.id from prepared join inserted using (id) order by prepared.ordinality`
	createNodeWithIDBatchStatement = `insert into %s (graph_id, id, kind_ids, properties) select $1, pg_catalog.unnest($2::int8[]), pg_catalog.unnest($3::text[])::int2[], pg_catalog.unnest($4::jsonb[])`
	deleteNodeWithIDStatement      = `delete from %s where id = any($1)`

	createEdgeStatement = `insert into %s (graph_id, start_id, end_id, kind_id, properties) values (@graph_id, @start_id, @end_id, @kind_id, @properties) returning (id, start_id, end_id, kind_id, properties)::public.edgeComposite;`

	// TODO: The query below is not a pure creation statement as it contains an `on conflict` clause to dance around
	//	     Azure post-processing. This was done because Azure post will submit the same creation request hundreds of
	// 		 times for the same edge. In PostgreSQL this results in a constraint violation. For now this is best-effort
	//		 until Azure post-processing can be refactored.
	createEdgeBatchStatement  = `insert into %s as e (graph_id, start_id, end_id, kind_id, properties) select $1, pg_catalog.unnest($2::int8[]), pg_catalog.unnest($3::int8[]), pg_catalog.unnest($4::int2[]), pg_catalog.unnest($5::jsonb[]) on conflict (start_id, end_id, kind_id, graph_id) do update set properties = e.properties || excluded.properties;`
	deleteEdgeWithIDStatement = `delete from %s as e where e.id = any($1)`

	edgePropertySetOnlyStatement      = `update %s set properties = properties || $1::jsonb where id = $2`
	edgePropertyDeleteOnlyStatement   = `update %s set properties = properties - $1::text[] where id = $2`
	edgePropertySetAndDeleteStatement = `update %s set properties = properties || $1::jsonb - $2::text[] where id = $3`
)
