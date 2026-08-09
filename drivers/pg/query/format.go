package query

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/graph"
)

func postgresIndexType(indexType graph.IndexType) string {
	switch indexType {
	case graph.BTreeIndex:
		return pgIndexTypeBTree
	case graph.TextSearchIndex:
		return pgIndexTypeGIN
	default:
		return "NOT SUPPORTED"
	}
}

func parsePostgresIndexType(pgType string) graph.IndexType {
	switch strings.ToLower(pgType) {
	case pgIndexTypeBTree:
		return graph.BTreeIndex
	case pgIndexTypeGIN:
		return graph.TextSearchIndex
	default:
		return graph.UnsupportedIndex
	}
}

func join(values ...string) string {
	return strings.Join(values, "")
}

func managedRelation(name string) string {
	name = strings.TrimPrefix(name, "public.")
	return pgx.Identifier{"public", name}.Sanitize()
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func formatDropPropertyIndex(indexName string) string {
	return join("drop index if exists ", managedRelation(indexName), ";")
}

func formatDropPropertyConstraint(constraintName string) string {
	return join("drop index if exists ", managedRelation(constraintName), ";")
}

func formatCreatePropertyConstraint(constraintName, tableName, fieldName string, indexType graph.IndexType) string {
	pgIndexType := postgresIndexType(indexType)

	return join("create unique index ", managedRelation(constraintName), " on ", managedRelation(tableName), " using ",
		pgIndexType, " ((", pgPropertiesColumn, " ->> ", sqlLiteral(fieldName), "));")
}

func formatCreatePropertyIndex(indexName, tableName, fieldName string, indexType graph.IndexType) string {
	var (
		pgIndexType  = postgresIndexType(indexType)
		queryPartial = join("create index ", managedRelation(indexName), " on ", managedRelation(tableName), " using ",
			pgIndexType, " ((", pgPropertiesColumn, " ->> ", sqlLiteral(fieldName))
	)

	if indexType == graph.TextSearchIndex {
		// GIN text search requires the column to be typed and to contain the tri-gram operation extension
		return join(queryPartial, "::text) gin_trgm_ops);")
	} else {
		return join(queryPartial, "));")
	}
}

func formatCreatePartitionTable(name, parent string, graphID int32) string {
	builder := strings.Builder{}

	builder.WriteString("create table ")
	builder.WriteString(managedRelation(name))
	builder.WriteString(" partition of ")
	builder.WriteString(managedRelation(parent))
	builder.WriteString(" for values in (")
	builder.WriteString(strconv.FormatInt(int64(graphID), 10))
	builder.WriteString(")")

	return builder.String()
}

func formatCreateEdgeParentTable(name, parent string, graphID int32) string {
	return formatCreatePartitionTable(name, parent, graphID) + " partition by list (kind_id)"
}

func formatCreateEdgeKindPartitionTable(name, parent string, kindID int16) string {
	return join(
		"create table if not exists ", managedRelation(name),
		" partition of ", managedRelation(parent),
		" for values in (", strconv.FormatInt(int64(kindID), 10), ")",
	)
}

func formatConflictMatcher(propertyNames []string, defaultOnConflict string) string {
	builder := strings.Builder{}
	builder.WriteString("on conflict (")

	if len(propertyNames) > 0 {
		for idx, propertyName := range propertyNames {
			if idx > 0 {
				builder.WriteString(", ")
			}

			builder.WriteString("(properties->>")
			builder.WriteString(sqlLiteral(propertyName))
			builder.WriteString(")")
		}
	} else {
		builder.WriteString(defaultOnConflict)
	}

	builder.WriteString(") ")
	return builder.String()
}

func FormatNodesUpdate(graphTarget model.Graph) string {
	return join(
		"update ", managedRelation(graphTarget.Partitions.Node.Name), " as n ",
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
	)
}

// NodeUpdateStagingTable is the name of the temporary staging table used by largeUpdate.
const NodeUpdateStagingTable = "node_update_staging"

// NodeUpdateStagingColumns lists the columns (in order) written by a COPY FROM during largeUpdate.
var NodeUpdateStagingColumns = []string{"id", "added_kinds", "deleted_kinds", "properties", "deleted_props"}

func FormatCreateNodeUpdateStagingTable(stagingTable string) string {
	return join(
		"create temp table if not exists ", pgx.Identifier{stagingTable}.Sanitize(), " (",
		"id bigint, ",
		"added_kinds text, ",
		"deleted_kinds text, ",
		"properties text, ",
		"deleted_props text",
		") on commit drop;",
	)
}

func FormatMergeNodeLargeUpdate(graphTarget model.Graph, stagingTable string) string {
	return join(
		"merge into ", managedRelation(graphTarget.Partitions.Node.Name), " as n ",
		"using ", pgx.Identifier{stagingTable}.Sanitize(), " as u on n.id = u.id ",
		"when matched then update set ",
		"kind_ids = uniq(sort(n.kind_ids - u.deleted_kinds::int2[] || u.added_kinds::int2[])), ",
		"properties = n.properties - u.deleted_props::text[] || u.properties::jsonb;",
	)
}

func FormatNodeUpsert(graphTarget model.Graph, identityProperties []string) string {
	return join(
		"insert into ", managedRelation(graphTarget.Partitions.Node.Name), " as n ",
		"(graph_id, kind_ids, properties) ",
		"select $1, pg_catalog.unnest($2::text[])::int2[], pg_catalog.unnest($3::jsonb[]) ",
		formatConflictMatcher(identityProperties, "id, graph_id"),
		"do update set properties = n.properties || excluded.properties, kind_ids = uniq(sort(n.kind_ids || excluded.kind_ids)) ",
		"returning id;",
	)
}

func FormatRelationshipPartitionUpsert(graphTarget model.Graph, identityProperties []string) string {
	if len(identityProperties) == 1 {
		return join("with input(start_id,end_id,kind_id,properties) as (",
			"select pg_catalog.unnest($2::int8[]),pg_catalog.unnest($3::int8[]),pg_catalog.unnest($4::int2[]),pg_catalog.unnest($5::jsonb[])), ",
			"updated as (update ", managedRelation(graphTarget.Partitions.Edge.Name), " as e ",
			"set properties=e.properties||input.properties from input ",
			"where e.graph_id=$1 and e.properties ->> $6=input.properties ->> $6 ",
			"returning input.start_id,input.end_id,input.kind_id,input.properties) ",
			"insert into ", managedRelation(graphTarget.Partitions.Edge.Name), " as e ",
			"(graph_id,start_id,end_id,kind_id,properties) ",
			"select $1,input.start_id,input.end_id,input.kind_id,input.properties from input ",
			"where not exists (select 1 from updated where updated.start_id=input.start_id ",
			"and updated.end_id=input.end_id and updated.kind_id=input.kind_id) ",
			"on conflict (start_id,end_id,kind_id,graph_id) do update ",
			"set properties=e.properties||excluded.properties;")
	}
	return join("insert into ", managedRelation(graphTarget.Partitions.Edge.Name), " as e ",
		"(graph_id, start_id, end_id, kind_id, properties) ",
		"select $1, pg_catalog.unnest($2::int8[]), pg_catalog.unnest($3::int8[]), pg_catalog.unnest($4::int2[]), pg_catalog.unnest($5::jsonb[]) ",
		formatConflictMatcher(identityProperties, "start_id, end_id, kind_id, graph_id"),
		"do update set properties = e.properties || excluded.properties;",
	)
}

type NodeUpdate struct {
	IDFuture *Future[graph.ID]
	Node     *graph.Node
}

// NodeUpdateBatch
//
// TODO: See note below
//
// Some assumptions were made here regarding identity kind matching since this data model does not directly require the
// kind of a node to enforce a constraint
type NodeUpdateBatch struct {
	IdentityProperties []string
	Updates            map[string]*NodeUpdate
}

func NewNodeUpdateBatch() *NodeUpdateBatch {
	return &NodeUpdateBatch{
		Updates: map[string]*NodeUpdate{},
	}
}

func (s *NodeUpdateBatch) Add(update graph.NodeUpdate) (*Future[graph.ID], error) {
	if len(s.IdentityProperties) > 0 && len(update.IdentityProperties) != len(s.IdentityProperties) {
		return nil, fmt.Errorf("node update mixes identity properties with pre-existing updates")
	}

	for _, expectedIdentityProperty := range s.IdentityProperties {
		found := false

		for _, updateIdentityProperty := range update.IdentityProperties {
			if expectedIdentityProperty == updateIdentityProperty {
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("node update mixes identity properties with pre-existing updates")
		}
	}

	if key, err := update.Key(); err != nil {
		return nil, err
	} else {
		update.Node.AddKinds(update.Node.Kinds...)

		if len(s.IdentityProperties) == 0 {
			s.IdentityProperties = make([]string, len(update.IdentityProperties))
			copy(s.IdentityProperties, update.IdentityProperties)
		}

		if existingUpdate, hasExisting := s.Updates[key]; hasExisting {
			existingUpdate.Node.Merge(update.Node)
			return existingUpdate.IDFuture, nil
		} else {
			newIDFuture := NewFuture(graph.ID(0))

			s.Updates[key] = &NodeUpdate{
				IDFuture: newIDFuture,
				Node:     update.Node,
			}

			return newIDFuture, nil
		}

	}
}

func ValidateNodeUpdateByBatch(updates []graph.NodeUpdate) (*NodeUpdateBatch, error) {
	updateBatch := NewNodeUpdateBatch()

	for _, update := range updates {
		if _, err := updateBatch.Add(update); err != nil {
			return nil, err
		}
	}

	return updateBatch, nil
}

type Future[T any] struct {
	Value T
}

func NewFuture[T any](value T) *Future[T] {
	return &Future[T]{
		Value: value,
	}
}

type RelationshipUpdate struct {
	StartID      *Future[graph.ID]
	EndID        *Future[graph.ID]
	Relationship *graph.Relationship
}

type RelationshipUpdateBatch struct {
	NodeUpdates        *NodeUpdateBatch
	IdentityProperties []string
	Updates            map[string]*RelationshipUpdate
}

func NewRelationshipUpdateBatch() *RelationshipUpdateBatch {
	return &RelationshipUpdateBatch{
		NodeUpdates: NewNodeUpdateBatch(),
		Updates:     map[string]*RelationshipUpdate{},
	}
}

func (s *RelationshipUpdateBatch) Add(update graph.RelationshipUpdate) error {
	if len(update.IdentityProperties) > 1 {
		return fmt.Errorf("relationship property identity supports at most one field")
	}
	if len(s.IdentityProperties) > 0 && len(update.IdentityProperties) != len(s.IdentityProperties) {
		return fmt.Errorf("relationship update mixes identity properties with pre-existing updates")
	}

	for _, expectedIdentityProperty := range s.IdentityProperties {
		found := false

		for _, updateIdentityProperty := range update.IdentityProperties {
			if expectedIdentityProperty == updateIdentityProperty {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("relationship update mixes identity properties with pre-existing updates")
		}
	}

	if startNodeID, err := s.NodeUpdates.Add(graph.NodeUpdate{
		Node:               update.Start,
		IdentityKind:       update.StartIdentityKind,
		IdentityProperties: update.StartIdentityProperties,
	}); err != nil {
		return err
	} else if endNodeID, err := s.NodeUpdates.Add(graph.NodeUpdate{
		Node:               update.End,
		IdentityKind:       update.EndIdentityKind,
		IdentityProperties: update.EndIdentityProperties,
	}); err != nil {
		return err
	} else if key, err := update.Key(); err != nil {
		return err
	} else {
		if len(s.IdentityProperties) == 0 {
			s.IdentityProperties = make([]string, len(update.IdentityProperties))
			copy(s.IdentityProperties, update.IdentityProperties)
		}

		if existingUpdate, hasExisting := s.Updates[key]; hasExisting {
			existingUpdate.Relationship.Merge(update.Relationship)
		} else {
			s.Updates[key] = &RelationshipUpdate{
				StartID:      startNodeID,
				EndID:        endNodeID,
				Relationship: update.Relationship,
			}
		}
	}

	return nil
}

func ValidateRelationshipUpdateByBatch(updates []graph.RelationshipUpdate) (*RelationshipUpdateBatch, error) {
	updateBatch := NewRelationshipUpdateBatch()

	for _, update := range updates {
		if err := updateBatch.Add(update); err != nil {
			return nil, err
		}
	}

	return updateBatch, nil
}
