package query_test

import (
	"strings"
	"testing"

	"github.com/specterops/dawgs/drivers/pg/model"
	query "github.com/specterops/dawgs/drivers/pg/query"
	"github.com/stretchr/testify/assert"
)

func generateTestGraphTarget(nodePartitionName string) model.Graph {
	return model.Graph{
		Partitions: model.GraphPartitions{
			Node: model.NewGraphPartition(nodePartitionName),
		},
	}
}

func TestFormatRelationshipPropertyIdentityUpsertUsesRegistryCompatibleStatements(t *testing.T) {
	graphTarget := model.Graph{Partitions: model.GraphPartitions{Edge: model.NewGraphPartition("public.edge_7")}}
	statement := query.FormatRelationshipPartitionUpsert(graphTarget, []string{"object_id"})
	assert.Contains(t, statement, `update "public"."edge_7" as e`)
	assert.Contains(t, statement, "e.properties ->> $6=input.properties ->> $6")
	assert.NotContains(t, statement, "on conflict ((properties")
}

func TestFormatNodesUpdate(t *testing.T) {
	t.Parallel()

	var (
		partitionName = "node_1"
		expected      = strings.Join([]string{
			`update "public"."node_1" as n `,
			"set ",
			" kind_ids = uniq(sort(kind_ids - u.deleted_kinds || u.added_kinds)), ",
			" properties = n.properties - u.deleted_properties || u.properties ",
			"from ",
			" (select ",
			"  pg_catalog.unnest($1::text[])::int8 as id, ",
			"  pg_catalog.unnest($2::text[])::int2[] as added_kinds, ",
			"  pg_catalog.unnest($3::text[])::int2[] as deleted_kinds, ",
			"  pg_catalog.unnest($4::jsonb[]) as properties, ",
			"  pg_catalog.unnest($5::text[])::text[] as deleted_properties) as u ",
			"where n.id = u.id; ",
		}, "")
		result = query.FormatNodesUpdate(generateTestGraphTarget(partitionName))
	)

	assert.Equal(t, expected, result)
}

func TestFormatCreateNodeUpdateStagingTable(t *testing.T) {
	t.Parallel()

	var (
		tableName = "my_staging_table"
		expected  = strings.Join([]string{
			`create temp table if not exists "my_staging_table" (`,
			"id bigint, ",
			"added_kinds text, ",
			"deleted_kinds text, ",
			"properties text, ",
			"deleted_props text",
			") on commit drop;",
		}, "")
		result = query.FormatCreateNodeUpdateStagingTable(tableName)
	)

	assert.Equal(t, expected, result)
}

func TestFormatMergeNodeLargeUpdate(t *testing.T) {
	t.Parallel()

	var (
		partitionName = "node_part_1"
		stagingTable  = "my_staging"
		expected      = strings.Join([]string{
			`merge into "public"."node_part_1" as n `,
			`using "my_staging" as u on n.id = u.id `,
			"when matched then update set ",
			"kind_ids = uniq(sort(n.kind_ids - u.deleted_kinds::int2[] || u.added_kinds::int2[])), ",
			"properties = n.properties - u.deleted_props::text[] || u.properties::jsonb;",
		}, "")
		result = query.FormatMergeNodeLargeUpdate(generateTestGraphTarget(partitionName), stagingTable)
	)

	assert.Equal(t, expected, result)
}

func TestNodeUpdateStagingColumns(t *testing.T) {
	t.Parallel()

	var (
		// Note: order is important
		expected = []string{"id", "added_kinds", "deleted_kinds", "properties", "deleted_props"}
	)

	assert.Equal(t, expected, query.NodeUpdateStagingColumns)
}
