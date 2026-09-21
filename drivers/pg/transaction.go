package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/specterops/dawgs/cypher/models/pgsql"
	"github.com/specterops/dawgs/cypher/models/pgsql/translate"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/graph"
	"github.com/specterops/dawgs/query"
	"github.com/specterops/dawgs/util/size"
)

type driver interface {
	Exec(ctx context.Context, sql string, arguments ...any) (commandTag pgconn.CommandTag, err error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

type inspectingDriver struct {
	upstreamDriver driver
}

func (s inspectingDriver) Exec(ctx context.Context, sql string, arguments ...any) (commandTag pgconn.CommandTag, err error) {
	inspector().Inspect(sql, arguments)
	return s.upstreamDriver.Exec(ctx, sql, arguments...)
}

func (s inspectingDriver) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	inspector().Inspect(sql, arguments)
	return s.upstreamDriver.Query(ctx, sql, arguments...)
}

func (s inspectingDriver) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	inspector().Inspect(sql, arguments)
	return s.upstreamDriver.QueryRow(ctx, sql, arguments...)
}

type transaction struct {
	schemaManager      *SchemaManager
	queryExecMode      pgx.QueryExecMode
	queryResultsFormat pgx.QueryResultFormats
	ctx                context.Context
	conn               *pgxpool.Conn
	tx                 pgx.Tx
	targetSchema       graph.Graph
	targetSchemaSet    bool
	pendingGraphs      map[string]model.Graph
	pendingKinds       map[graph.Kind]int16
	pendingKindIDs     map[int16]graph.Kind
	pendingDefault     *model.Graph
	pendingDefaultName string
	topologyTouched    bool
	writable           bool
}

func newTransactionWrapper(ctx context.Context, conn *pgxpool.Conn, schemaManager *SchemaManager, cfg *Config, allocateTransaction bool) (*transaction, error) {
	wrapper := &transaction{
		schemaManager:      schemaManager,
		queryExecMode:      cfg.QueryExecMode,
		queryResultsFormat: cfg.QueryResultFormats,
		ctx:                ctx,
		conn:               conn,
		targetSchemaSet:    false,
		pendingGraphs:      map[string]model.Graph{},
		pendingKinds:       map[graph.Kind]int16{},
		pendingKindIDs:     map[int16]graph.Kind{},
		writable:           cfg.Options.AccessMode != pgx.ReadOnly,
	}

	if allocateTransaction {
		if pgxTx, err := conn.BeginTx(ctx, cfg.Options); err != nil {
			return nil, err
		} else {
			wrapper.tx = pgxTx
		}
	}

	return wrapper, nil
}

func (s *transaction) driver() driver {
	if s.tx != nil {
		return inspectingDriver{
			upstreamDriver: s.tx,
		}
	}

	return inspectingDriver{
		upstreamDriver: s.conn,
	}
}

func (s *transaction) GraphQueryMemoryLimit() size.Size {
	return s.schemaManager.graphQueryMemoryLimit
}

func (s *transaction) WithGraph(schema graph.Graph) graph.Transaction {
	s.targetSchema = schema
	s.targetSchemaSet = true

	return s
}

func (s *transaction) Close() {
	if s.tx != nil {
		s.tx.Rollback(s.ctx)
		s.tx = nil
	}
	s.discardPending()
}

func (s *transaction) getTargetGraph() (model.Graph, error) {
	if !s.targetSchemaSet {
		// Look for a default graph target
		if defaultGraph, hasDefaultGraph := s.schemaManager.DefaultGraph(); !hasDefaultGraph {
			return model.Graph{}, fmt.Errorf("driver operation requires a graph target to be set")
		} else {
			return defaultGraph, nil
		}
	}

	if s.writable {
		return s.schemaManager.AssertGraph(s, s.targetSchema)
	}
	return s.schemaManager.lookupGraph(s, s.targetSchema.Name)
}

func (s *transaction) targetGraphID() (int32, error) {
	if graphTarget, err := s.getTargetGraph(); err != nil {
		return 0, err
	} else {
		return graphTarget.ID, nil
	}
}

func (s *transaction) CreateNode(properties *graph.Properties, kinds ...graph.Kind) (*graph.Node, error) {
	if graphTarget, err := s.getTargetGraph(); err != nil {
		return nil, err
	} else if kindIDSlice, err := s.schemaManager.assertKindsInTransaction(s, kinds); err != nil {
		return nil, err
	} else if propertiesJSONB, err := pgsql.PropertiesToJSONB(properties); err != nil {
		return nil, err
	} else {
		var (
			node   graph.Node
			result = s.Raw(fmt.Sprintf(createNodeStatement, qualifiedManagedRelation(graphTarget.Partitions.Node.Name)), map[string]any{
				"graph_id":   graphTarget.ID,
				"kind_ids":   kindIDSlice,
				"properties": propertiesJSONB,
			})
		)

		defer result.Close()

		if !result.Next() {
			return nil, result.Error()
		}

		return &node, result.Scan(&node)
	}
}

func (s *transaction) UpdateNode(node *graph.Node) error {
	var (
		properties       = node.Properties
		updateStatements []graph.Criteria
	)

	if addedKinds := node.AddedKinds; len(addedKinds) > 0 {
		updateStatements = append(updateStatements, query.AddKinds(query.Node(), addedKinds))
	}

	if deletedKinds := node.DeletedKinds; len(deletedKinds) > 0 {
		updateStatements = append(updateStatements, query.DeleteKinds(query.Node(), deletedKinds))
	}

	if modifiedProperties := properties.ModifiedProperties(); len(modifiedProperties) > 0 {
		updateStatements = append(updateStatements, query.SetProperties(query.Node(), modifiedProperties))
	}

	if deletedProperties := properties.DeletedProperties(); len(deletedProperties) > 0 {
		updateStatements = append(updateStatements, query.DeleteProperties(query.Node(), deletedProperties...))
	}

	return s.Nodes().Filter(query.Equals(query.NodeID(), node.ID)).Query(func(results graph.Result) error {
		// We don't need to exhaust the result set as the defered close with discard it for us
		return results.Error()
	}, updateStatements...)
}

func (s *transaction) Nodes() graph.NodeQuery {
	return &nodeQuery{
		liveQuery: newLiveQuery(s.ctx, s, s, s.targetGraphID),
	}
}

func (s *transaction) CreateRelationshipByIDs(startNodeID, endNodeID graph.ID, kind graph.Kind, properties *graph.Properties) (*graph.Relationship, error) {
	if graphTarget, err := s.getTargetGraph(); err != nil {
		return nil, err
	} else if kindIDSlice, err := s.schemaManager.assertKindsInTransaction(s, graph.Kinds{kind}); err != nil {
		return nil, err
	} else if _, err := s.schemaManager.EnsureEdgeKindPartitions(s, graphTarget.ID, kindIDSlice); err != nil {
		return nil, err
	} else if propertiesJSONB, err := pgsql.PropertiesToJSONB(properties); err != nil {
		return nil, err
	} else {
		var (
			edge   graph.Relationship
			result = s.Raw(fmt.Sprintf(createEdgeStatement, qualifiedManagedRelation(graphTarget.Partitions.Edge.Name)), map[string]any{
				"graph_id":   graphTarget.ID,
				"start_id":   startNodeID,
				"end_id":     endNodeID,
				"kind_id":    kindIDSlice[0],
				"properties": propertiesJSONB,
			})
		)

		defer result.Close()

		if !result.Next() {
			return nil, result.Error()
		}

		return &edge, result.Scan(&edge)
	}
}

func (s *transaction) UpdateRelationship(relationship *graph.Relationship) error {
	var (
		modifiedProperties    = relationship.Properties.ModifiedProperties()
		deletedProperties     = relationship.Properties.DeletedProperties()
		numModifiedProperties = len(modifiedProperties)
		numDeletedProperties  = len(deletedProperties)

		statement string
		arguments []any
	)

	if numModifiedProperties > 0 {
		if jsonbArgument, err := pgsql.ValueToJSONB(modifiedProperties); err != nil {
			return err
		} else {
			arguments = append(arguments, jsonbArgument)
		}

		if numDeletedProperties > 0 {
			if textArrayArgument, err := pgsql.StringSliceToTextArray(deletedProperties); err != nil {
				return err
			} else {
				arguments = append(arguments, textArrayArgument)
			}

			statement = edgePropertySetAndDeleteStatement
		} else {
			statement = edgePropertySetOnlyStatement
		}
	} else if numDeletedProperties > 0 {
		if textArrayArgument, err := pgsql.StringSliceToTextArray(deletedProperties); err != nil {
			return err
		} else {
			arguments = append(arguments, textArrayArgument)
		}

		statement = edgePropertyDeleteOnlyStatement
	}

	graphTarget, err := s.getTargetGraph()
	if err != nil {
		return err
	}
	_, err = s.driver().Exec(s.ctx, fmt.Sprintf(statement, qualifiedManagedRelation(graphTarget.Partitions.Edge.Name)), append(arguments, relationship.ID)...)
	return err
}

func (s *transaction) Relationships() graph.RelationshipQuery {
	return &relationshipQuery{
		liveQuery: newLiveQuery(s.ctx, s, s, s.targetGraphID),
	}
}

func (s *transaction) query(query string, parameters map[string]any) (pgx.Rows, error) {
	queryArgs := []any{s.queryExecMode, s.queryResultsFormat}

	if len(parameters) > 0 {
		queryArgs = append(queryArgs, pgx.NamedArgs(parameters))
	}

	return s.driver().Query(s.ctx, query, queryArgs...)
}

func (s *transaction) Query(query string, parameters map[string]any) graph.Result {
	if parsedQuery, err := frontend.ParseCypher(frontend.NewContext(), query); err != nil {
		return graph.NewErrorResult(err)
	} else if graphTarget, err := s.getTargetGraph(); err != nil {
		return graph.NewErrorResult(err)
	} else if translated, err := translate.Translate(s.ctx, parsedQuery, s, parameters, graphTarget.ID); err != nil {
		return graph.NewErrorResult(err)
	} else {
		if translated.HasMutations {
			if !s.writable {
				return graph.NewErrorResult(fmt.Errorf("mutating Cypher query requires a write transaction"))
			}
			if _, err := s.schemaManager.EnsureEdgeKindPartitions(s, graphTarget.ID, translated.RequiredEdgeKindIDs); err != nil {
				return graph.NewErrorResult(err)
			}
		}
		if sqlQuery, err := translate.Translated(translated); err != nil {
			return graph.NewErrorResult(err)
		} else {
			return s.Raw(sqlQuery, translated.Parameters)
		}
	}
}

func (s *transaction) Raw(query string, parameters map[string]any) graph.Result {
	if isSessionIdentityStatement(query) {
		return graph.NewErrorResult(fmt.Errorf("PostgreSQL runtime sessions may not persist session state or create temporary objects"))
	}
	if rows, err := s.query(query, parameters); err != nil {
		return graph.NewErrorResult(err)
	} else {
		return &queryResult{
			ctx:        s.ctx,
			rows:       rows,
			kindMapper: s,
		}
	}
}

// ExecSimple executes a trusted, parameter-free SQL script on the transaction's
// physical connection. PostgreSQL's extended protocol accepts only one command
// per prepared statement, while the embedded schema assets intentionally
// contain function bodies and multiple DDL commands.
func (s *transaction) ExecSimple(statement string) error {
	var connection *pgx.Conn
	if s.tx != nil {
		connection = s.tx.Conn()
	} else {
		connection = s.conn.Conn()
	}

	_, err := connection.PgConn().Exec(s.ctx, statement).ReadAll()
	return err
}

func (s *transaction) Commit() error {
	if s.tx != nil {
		err := s.tx.Commit(s.ctx)
		s.tx = nil
		if err != nil {
			s.schemaManager.invalidateCaches()
			if s.topologyTouched && !errors.Is(err, pgx.ErrTxCommitRollback) && s.schemaManager.lifecycle.invalidateReady() {
				s.schemaManager.clearValidatedBackends()
			}
			s.discardPending()
			return err
		}
		s.schemaManager.publishPending(s)
	}

	return nil
}

func (s *transaction) discardPending() {
	s.pendingGraphs = map[string]model.Graph{}
	s.pendingKinds = map[graph.Kind]int16{}
	s.pendingKindIDs = map[int16]graph.Kind{}
	s.pendingDefault = nil
	s.pendingDefaultName = ""
}

func (s *transaction) MapKind(ctx context.Context, kind graph.Kind) (int16, error) {
	ids, err := s.MapKinds(ctx, graph.Kinds{kind})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

func (s *transaction) MapKinds(_ context.Context, kinds graph.Kinds) ([]int16, error) {
	s.schemaManager.lock.RLock()
	defer s.schemaManager.lock.RUnlock()
	ids, missing := s.schemaManager.mapKindsInTransaction(s, kinds)
	if len(missing) > 0 {
		return nil, fmt.Errorf("unable to map kinds: %v", missing)
	}
	return ids, nil
}

func (s *transaction) AssertKinds(_ context.Context, kinds graph.Kinds) ([]int16, error) {
	return s.schemaManager.assertKindsInTransaction(s, kinds)
}

func (s *transaction) MapKindID(ctx context.Context, kindID int16) (graph.Kind, error) {
	kinds, err := s.MapKindIDs(ctx, []int16{kindID})
	if err != nil {
		return nil, err
	}
	return kinds[0], nil
}

func (s *transaction) MapKindIDs(_ context.Context, kindIDs []int16) (graph.Kinds, error) {
	s.schemaManager.lock.RLock()
	defer s.schemaManager.lock.RUnlock()
	kinds := make(graph.Kinds, 0, len(kindIDs))
	for _, kindID := range kindIDs {
		if kind, found := s.pendingKindIDs[kindID]; found {
			kinds = append(kinds, kind)
		} else if kind, found := s.schemaManager.kindIDsByKind[kindID]; found {
			kinds = append(kinds, kind)
		} else {
			return nil, fmt.Errorf("unable to map kind id %d", kindID)
		}
	}
	return kinds, nil
}
