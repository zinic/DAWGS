package query

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/graph"
)

type Query struct {
	tx graph.Transaction
}

type simpleProtocolExecutor interface {
	ExecSimple(statement string) error
}

func On(tx graph.Transaction) Query {
	return Query{
		tx: tx,
	}
}

func (s Query) exec(statement string, args map[string]any) error {
	result := s.tx.Raw(statement, args)
	defer result.Close()

	return result.Error()
}

func (s Query) describeGraphPartition(name string) (model.GraphPartition, error) {
	graphPartition := model.NewGraphPartition(name)

	if tableIndexDefinitions, err := s.SelectTableIndexDefinitions(name); err != nil {
		return graphPartition, err
	} else {
		for _, tableIndexDefinition := range tableIndexDefinitions {
			if captureGroups := pgPropertyIndexRegex.FindStringSubmatch(tableIndexDefinition); captureGroups == nil {
				// If this index does not match our expected column index format then report it as a potential error
				if !pgColumnIndexRegex.MatchString(tableIndexDefinition) {
					return graphPartition, fmt.Errorf("regex mis-match on schema definition: %s", tableIndexDefinition)
				}
			} else {
				indexName := captureGroups[pgIndexRegexGroupName]

				if captureGroups[pgIndexRegexGroupUnique] == pgIndexUniqueStr {
					graphPartition.Constraints[indexName] = graph.Constraint{
						Name:  indexName,
						Field: captureGroups[pgIndexRegexGroupUsingFields],
						Type:  parsePostgresIndexType(captureGroups[pgIndexRegexGroupIndexType]),
					}
				} else {
					graphPartition.Indexes[indexName] = graph.Index{
						Name:  indexName,
						Field: captureGroups[pgIndexRegexGroupUsingFields],
						Type:  parsePostgresIndexType(captureGroups[pgIndexRegexGroupIndexType]),
					}
				}
			}
		}
	}

	return graphPartition, nil
}

func (s Query) SelectKinds() (map[graph.Kind]int16, error) {
	var (
		kindID   int16
		kindName string

		kinds  = map[graph.Kind]int16{}
		result = s.tx.Raw(sqlSelectKinds, nil)
	)

	defer result.Close()

	for result.Next() {
		if err := result.Scan(&kindID, &kindName); err != nil {
			return nil, err
		}

		kinds[graph.StringKind(kindName)] = kindID
	}

	return kinds, result.Error()
}

func (s Query) selectGraphPartitions(graphID int32) (model.GraphPartitions, error) {
	var (
		nodePartitionName = model.NodePartitionTableName(graphID)
		edgePartitionName = model.EdgePartitionTableName(graphID)
	)

	if nodePartition, err := s.describeGraphPartition(nodePartitionName); err != nil {
		return model.GraphPartitions{}, err
	} else if edgePartition, err := s.describeGraphPartition(edgePartitionName); err != nil {
		return model.GraphPartitions{}, err
	} else {
		return model.GraphPartitions{
			Node: nodePartition,
			Edge: edgePartition,
		}, nil
	}
}

func (s Query) selectGraphPartialByName(name string) (model.Graph, error) {
	var (
		graphID int32
		result  = s.tx.Raw(sqlSelectGraphByName, map[string]any{
			"name": name,
		})
	)

	defer result.Close()

	if !result.Next() {
		return model.Graph{}, pgx.ErrNoRows
	}

	if err := result.Scan(&graphID); err != nil {
		return model.Graph{}, err
	}

	return model.Graph{
		ID:   graphID,
		Name: name,
	}, result.Error()
}

func (s Query) SelectGraphByName(name string) (model.Graph, error) {
	if definition, err := s.selectGraphPartialByName(name); err != nil {
		return model.Graph{}, err
	} else if graphPartitions, err := s.selectGraphPartitions(definition.ID); err != nil {
		return model.Graph{}, err
	} else {
		definition.Partitions = graphPartitions
		return definition, nil
	}
}

func (s Query) selectGraphPartials() ([]model.Graph, error) {
	var (
		graphID   int32
		graphName string
		graphs    []model.Graph

		result = s.tx.Raw(sqlSelectGraphs, nil)
	)

	defer result.Close()

	for result.Next() {
		if err := result.Scan(&graphID, &graphName); err != nil {
			return nil, err
		} else {
			graphs = append(graphs, model.Graph{
				ID:   graphID,
				Name: graphName,
			})
		}
	}

	return graphs, result.Error()
}

func (s Query) SelectGraphs() (map[string]model.Graph, error) {
	if definitions, err := s.selectGraphPartials(); err != nil {
		return nil, err
	} else {
		indexed := map[string]model.Graph{}

		for _, definition := range definitions {
			if graphPartitions, err := s.selectGraphPartitions(definition.ID); err != nil {
				return nil, err
			} else {
				definition.Partitions = graphPartitions
				indexed[definition.Name] = definition
			}
		}

		return indexed, nil
	}
}

func (s Query) CreatePropertyIndex(indexName, tableName, fieldName string, indexType graph.IndexType) error {
	return s.exec(formatCreatePropertyIndex(indexName, tableName, fieldName, indexType), nil)
}

func (s Query) CreatePropertyConstraint(indexName, tableName, fieldName string, indexType graph.IndexType) error {
	if indexType != graph.BTreeIndex {
		return fmt.Errorf("only b-tree indexing is supported for property constraints")
	}

	return s.exec(formatCreatePropertyConstraint(indexName, tableName, fieldName, indexType), nil)
}

func (s Query) CreateEdgePropertyConstraint(graphID int32, constraint graph.Constraint) error {
	if constraint.Type != graph.BTreeIndex {
		return fmt.Errorf("only b-tree indexing is supported for edge property constraints")
	}
	logicalName := model.ConstraintName(model.EdgePartitionTableName(graphID), constraint)
	result := s.tx.Raw(`insert into public.edge_property_constraint
  (graph_id, logical_name, property_name, constraint_type, collation_name, lifecycle_state)
values (@graph_id, @logical_name, @property_name, 'btree', 'default', 'pending')
on conflict (graph_id, property_name) do update
set logical_name=excluded.logical_name,lifecycle_state='pending'
where edge_property_constraint.constraint_type='btree'
  and edge_property_constraint.collation_name='default'
	returning constraint_id`, map[string]any{
		"graph_id": graphID, "logical_name": logicalName, "property_name": constraint.Field,
	})
	if !result.Next() {
		if err := result.Error(); err != nil {
			result.Close()
			return err
		}
		result.Close()
		return fmt.Errorf("edge property constraint %q conflicts with incompatible registry semantics", constraint.Field)
	}
	var constraintID int64
	if err := result.Scan(&constraintID); err != nil {
		result.Close()
		return err
	}
	if err := result.Error(); err != nil {
		result.Close()
		return err
	}
	result.Close()
	if err := s.exec(`lock table public.edge in share row exclusive mode`, nil); err != nil {
		return err
	}
	if err := s.exec(`delete from public.edge_property_unique_value
where graph_id=@graph_id and constraint_id=@constraint_id`, map[string]any{
		"graph_id": graphID, "constraint_id": constraintID,
	}); err != nil {
		return err
	}
	if err := s.exec(`insert into public.edge_property_unique_value (graph_id,constraint_id,property_value,edge_id)
select e.graph_id,@constraint_id,e.properties ->> @property_name,e.id
from public.edge e where e.graph_id=@graph_id and e.properties ->> @property_name is not null`, map[string]any{
		"graph_id": graphID, "constraint_id": constraintID, "property_name": constraint.Field,
	}); err != nil {
		return err
	}
	return s.exec(`update public.edge_property_constraint set lifecycle_state='active'
where graph_id=@graph_id and constraint_id=@constraint_id and lifecycle_state='pending'`, map[string]any{
		"graph_id": graphID, "constraint_id": constraintID,
	})
}

func (s Query) SelectEdgePropertyConstraints(graphID int32) (map[string]graph.Constraint, error) {
	result := s.tx.Raw(`select logical_name,property_name,constraint_type
from public.edge_property_constraint where graph_id=@graph_id and lifecycle_state in ('pending','active')
order by logical_name`, map[string]any{"graph_id": graphID})
	defer result.Close()
	constraints := map[string]graph.Constraint{}
	for result.Next() {
		var logicalName, propertyName, constraintType string
		if err := result.Scan(&logicalName, &propertyName, &constraintType); err != nil {
			return nil, err
		}
		if constraintType != "btree" {
			return nil, fmt.Errorf("unsupported edge property constraint type %q", constraintType)
		}
		constraints[logicalName] = graph.Constraint{Name: logicalName, Field: propertyName, Type: graph.BTreeIndex}
	}
	return constraints, result.Error()
}

func (s Query) DropEdgePropertyConstraint(graphID int32, logicalName string) error {
	if err := s.exec(`update public.edge_property_constraint set lifecycle_state='draining'
where graph_id=@graph_id and logical_name=@logical_name and lifecycle_state in ('pending','active')`, map[string]any{
		"graph_id": graphID, "logical_name": logicalName,
	}); err != nil {
		return err
	}
	if err := s.exec(`lock table public.edge in share row exclusive mode`, nil); err != nil {
		return err
	}
	if err := s.exec(`delete from public.edge_property_unique_value v using public.edge_property_constraint c
where c.graph_id=@graph_id and c.logical_name=@logical_name and c.lifecycle_state='draining'
  and v.graph_id=c.graph_id and v.constraint_id=c.constraint_id`, map[string]any{
		"graph_id": graphID, "logical_name": logicalName,
	}); err != nil {
		return err
	}
	return s.exec(`update public.edge_property_constraint set lifecycle_state='tombstoned'
where graph_id=@graph_id and logical_name=@logical_name and lifecycle_state='draining'`, map[string]any{
		"graph_id": graphID, "logical_name": logicalName,
	})
}

func (s Query) DropIndex(indexName string) error {
	return s.exec(formatDropPropertyIndex(indexName), nil)
}

func (s Query) DropConstraint(constraintName string) error {
	return s.exec(formatDropPropertyConstraint(constraintName), nil)
}

func (s Query) CreateSchema() error {
	if executor, ok := s.tx.(simpleProtocolExecutor); ok {
		return executor.ExecSimple(sqlSchemaUp)
	}
	if err := s.exec(sqlSchemaUp, nil); err != nil {
		return err
	}

	return nil
}

func (s Query) DropSchema() error {
	if executor, ok := s.tx.(simpleProtocolExecutor); ok {
		return executor.ExecSimple(sqlSchemaDown)
	}
	if err := s.exec(sqlSchemaDown, nil); err != nil {
		return err
	}

	return nil
}

func (s Query) insertGraph(name string) (model.Graph, error) {
	var (
		graphID int32
		result  = s.tx.Raw(`select public.dawgs_provision_graph(@name)`, map[string]any{
			"name": name,
		})
	)

	defer result.Close()

	if !result.Next() {
		return model.Graph{}, result.Error()
	}

	if err := result.Scan(&graphID); err != nil {
		return model.Graph{}, fmt.Errorf("failed mapping ID from graph entry creation: %w", err)
	}

	return model.Graph{
		ID:   graphID,
		Name: name,
	}, nil
}

func (s Query) CreatePartitionTable(name, parent string, graphID int32) (model.GraphPartition, error) {
	if err := s.exec(formatCreatePartitionTable(name, parent, graphID), nil); err != nil {
		return model.GraphPartition{}, err
	}

	return model.GraphPartition{
		Name: name,
	}, nil
}

func (s Query) CreateEdgeParentTable(name, parent string, graphID int32) (model.GraphPartition, error) {
	if err := s.exec(formatCreateEdgeParentTable(name, parent, graphID), nil); err != nil {
		return model.GraphPartition{}, err
	}

	return model.NewGraphPartition(name), nil
}

// CreateEdgeKindPartition ensures the physical leaf for a graph/kind pair.
// Callers must have already established the graph-scoped edge parent.
func (s Query) CreateEdgeKindPartition(graphID int32, kindID int16) (model.EdgeKindPartition, error) {
	name := model.EdgeKindPartitionTableName(graphID, kindID)
	if err := s.exec(`select public.dawgs_provision_edge_kind_leaf(@graph_id,@kind_id)`, map[string]any{
		"graph_id": graphID,
		"kind_id":  kindID,
	}); err != nil {
		return model.EdgeKindPartition{}, err
	}

	return model.EdgeKindPartition{KindID: kindID, Name: name}, nil
}

func (s Query) SelectTableIndexDefinitions(tableName string) ([]string, error) {
	var (
		definition  string
		definitions []string

		result = s.tx.Raw(sqlSelectTableIndexes, map[string]any{
			"tablename": tableName,
		})
	)

	defer result.Close()

	for result.Next() {
		if err := result.Scan(&definition); err != nil {
			return nil, err
		}

		definitions = append(definitions, strings.ToLower(definition))
	}

	return definitions, result.Error()
}

func (s Query) SelectKindID(kind graph.Kind) (int16, error) {
	var (
		kindID int16
		result = s.tx.Raw(sqlSelectKindID, map[string]any{
			"name": kind.String(),
		})
	)

	defer result.Close()

	if !result.Next() {
		return -1, pgx.ErrNoRows
	}

	if err := result.Scan(&kindID); err != nil {
		return -1, err
	}

	return kindID, result.Error()
}

func (s Query) assertGraphPartitionIndexes(graphID int32, partitions model.GraphPartitions, indexChanges model.IndexChangeSet) error {
	for _, indexToRemove := range append(indexChanges.NodeIndexesToRemove, indexChanges.EdgeIndexesToRemove...) {
		if err := s.DropIndex(indexToRemove); err != nil {
			return err
		}
	}

	for _, constraintToRemove := range indexChanges.NodeConstraintsToRemove {
		if err := s.DropConstraint(constraintToRemove); err != nil {
			return err
		}
	}
	for _, constraintToRemove := range indexChanges.EdgeConstraintsToRemove {
		if err := s.DropEdgePropertyConstraint(graphID, constraintToRemove); err != nil {
			return err
		}
	}

	for indexName, index := range indexChanges.NodeIndexesToAdd {
		if err := s.CreatePropertyIndex(indexName, partitions.Node.Name, index.Field, index.Type); err != nil {
			return err
		}
	}

	for constraintName, constraint := range indexChanges.NodeConstraintsToAdd {
		if err := s.CreatePropertyConstraint(constraintName, partitions.Node.Name, constraint.Field, constraint.Type); err != nil {
			return err
		}
	}

	for indexName, index := range indexChanges.EdgeIndexesToAdd {
		if err := s.CreatePropertyIndex(indexName, partitions.Edge.Name, index.Field, index.Type); err != nil {
			return err
		}
	}

	for constraintName, constraint := range indexChanges.EdgeConstraintsToAdd {
		_ = constraintName
		if err := s.CreateEdgePropertyConstraint(graphID, constraint); err != nil {
			return err
		}
	}

	return nil
}

func (s Query) AssertGraph(schema graph.Graph, definition model.Graph) (model.Graph, error) {
	var (
		requiredNodePartition = model.NewGraphPartitionFromSchema(definition.Partitions.Node.Name, schema.NodeIndexes, schema.NodeConstraints)
		requiredEdgePartition = model.NewGraphPartitionFromSchema(definition.Partitions.Edge.Name, schema.EdgeIndexes, schema.EdgeConstraints)
		indexChangeSet        = model.NewIndexChangeSet()
	)

	if presentNodePartition, err := s.describeGraphPartition(definition.Partitions.Node.Name); err != nil {
		return model.Graph{}, err
	} else {
		for presentNodeIndexName := range presentNodePartition.Indexes {
			if _, hasMatchingDefinition := requiredNodePartition.Indexes[presentNodeIndexName]; !hasMatchingDefinition {
				indexChangeSet.NodeIndexesToRemove = append(indexChangeSet.NodeIndexesToRemove, presentNodeIndexName)
			}
		}

		for presentNodeConstraintName := range presentNodePartition.Constraints {
			if _, hasMatchingDefinition := requiredNodePartition.Constraints[presentNodeConstraintName]; !hasMatchingDefinition {
				indexChangeSet.NodeConstraintsToRemove = append(indexChangeSet.NodeConstraintsToRemove, presentNodeConstraintName)
			}
		}

		for requiredNodeIndexName, requiredNodeIndex := range requiredNodePartition.Indexes {
			if presentNodeIndex, hasMatchingDefinition := presentNodePartition.Indexes[requiredNodeIndexName]; !hasMatchingDefinition {
				indexChangeSet.NodeIndexesToAdd[requiredNodeIndexName] = requiredNodeIndex
			} else if requiredNodeIndex.Type != presentNodeIndex.Type {
				indexChangeSet.NodeIndexesToRemove = append(indexChangeSet.NodeIndexesToRemove, requiredNodeIndexName)
				indexChangeSet.NodeIndexesToAdd[requiredNodeIndexName] = requiredNodeIndex
			}
		}

		for requiredNodeConstraintName, requiredNodeConstraint := range requiredNodePartition.Constraints {
			if presentNodeConstraint, hasMatchingDefinition := presentNodePartition.Constraints[requiredNodeConstraintName]; !hasMatchingDefinition {
				indexChangeSet.NodeConstraintsToAdd[requiredNodeConstraintName] = requiredNodeConstraint
			} else if requiredNodeConstraint.Type != presentNodeConstraint.Type {
				indexChangeSet.NodeConstraintsToRemove = append(indexChangeSet.NodeConstraintsToRemove, requiredNodeConstraintName)
				indexChangeSet.NodeConstraintsToAdd[requiredNodeConstraintName] = requiredNodeConstraint
			}
		}
	}

	if presentEdgePartition, err := s.describeGraphPartition(definition.Partitions.Edge.Name); err != nil {
		return model.Graph{}, err
	} else {
		if presentEdgePartition.Constraints, err = s.SelectEdgePropertyConstraints(definition.ID); err != nil {
			return model.Graph{}, err
		}
		for presentEdgeIndexName := range presentEdgePartition.Indexes {
			if _, hasMatchingDefinition := requiredEdgePartition.Indexes[presentEdgeIndexName]; !hasMatchingDefinition {
				indexChangeSet.EdgeIndexesToRemove = append(indexChangeSet.EdgeIndexesToRemove, presentEdgeIndexName)
			}
		}

		for presentEdgeConstraintName := range presentEdgePartition.Constraints {
			if _, hasMatchingDefinition := requiredEdgePartition.Constraints[presentEdgeConstraintName]; !hasMatchingDefinition {
				indexChangeSet.EdgeConstraintsToRemove = append(indexChangeSet.EdgeConstraintsToRemove, presentEdgeConstraintName)
			}
		}

		for requiredEdgeIndexName, requiredEdgeIndex := range requiredEdgePartition.Indexes {
			if presentEdgeIndex, hasMatchingDefinition := presentEdgePartition.Indexes[requiredEdgeIndexName]; !hasMatchingDefinition {
				indexChangeSet.EdgeIndexesToAdd[requiredEdgeIndexName] = requiredEdgeIndex
			} else if requiredEdgeIndex.Type != presentEdgeIndex.Type {
				indexChangeSet.EdgeIndexesToRemove = append(indexChangeSet.EdgeIndexesToRemove, requiredEdgeIndexName)
				indexChangeSet.EdgeIndexesToAdd[requiredEdgeIndexName] = requiredEdgeIndex
			}
		}

		for requiredEdgeConstraintName, requiredEdgeConstraint := range requiredEdgePartition.Constraints {
			if presentEdgeConstraint, hasMatchingDefinition := presentEdgePartition.Constraints[requiredEdgeConstraintName]; !hasMatchingDefinition {
				indexChangeSet.EdgeConstraintsToAdd[requiredEdgeConstraintName] = requiredEdgeConstraint
			} else if requiredEdgeConstraint.Type != presentEdgeConstraint.Type {
				indexChangeSet.EdgeConstraintsToRemove = append(indexChangeSet.EdgeConstraintsToRemove, requiredEdgeConstraintName)
				indexChangeSet.EdgeConstraintsToAdd[requiredEdgeConstraintName] = requiredEdgeConstraint
			}
		}
	}

	return model.Graph{
		ID:   definition.ID,
		Name: definition.Name,
		Partitions: model.GraphPartitions{
			Node: requiredNodePartition,
			Edge: requiredEdgePartition,
		},
	}, s.assertGraphPartitionIndexes(definition.ID, definition.Partitions, indexChangeSet)
}

func (s Query) createGraphPartitions(definition model.Graph) (model.Graph, error) {
	var (
		nodePartitionName = model.NodePartitionTableName(definition.ID)
		edgePartitionName = model.EdgePartitionTableName(definition.ID)
	)

	if nodePartition, err := s.CreatePartitionTable(nodePartitionName, model.NodeTable, definition.ID); err != nil {
		return model.Graph{}, err
	} else {
		definition.Partitions.Node = nodePartition
	}
	if err := s.exec(fmt.Sprintf(`drop trigger if exists delete_node_edges on %s`, nodePartitionName), nil); err != nil {
		return model.Graph{}, err
	}
	if err := s.exec(fmt.Sprintf(`create trigger delete_node_edges after delete on %s
referencing old table as deleted_nodes for each statement
execute function public.delete_node_edges()`, nodePartitionName), nil); err != nil {
		return model.Graph{}, err
	}

	if edgePartition, err := s.CreateEdgeParentTable(edgePartitionName, model.EdgeTable, definition.ID); err != nil {
		return model.Graph{}, err
	} else {
		definition.Partitions.Edge = edgePartition
	}

	return definition, nil
}

func (s Query) CreateGraph(schema graph.Graph) (model.Graph, error) {
	if definition, err := s.insertGraph(schema.Name); err != nil {
		return model.Graph{}, err
	} else {
		return s.AssertGraph(schema, definition)
	}
}

// ProvisionGraph creates or resolves only the canonical graph partitions via
// the security-definer provisioning contract. It intentionally does not
// reconcile user-declared indexes or constraints, which remain an owner or
// operator schema-management action on an accepted restricted target.
func (s Query) ProvisionGraph(name string) (model.Graph, error) {
	if definition, err := s.insertGraph(name); err != nil {
		return model.Graph{}, err
	} else if partitions, err := s.selectGraphPartitions(definition.ID); err != nil {
		return model.Graph{}, err
	} else {
		definition.Partitions = partitions
		return definition, nil
	}
}

func (s Query) InsertOrGetKind(kind graph.Kind) (int16, error) {
	var (
		kindID int16
		result = s.tx.Raw(sqlInsertKind, map[string]any{
			"name": kind.String(),
		})
	)

	defer result.Close()

	if !result.Next() {
		return -1, pgx.ErrNoRows
	}

	if err := result.Scan(&kindID); err != nil {
		return -1, err
	}

	return kindID, result.Error()
}
