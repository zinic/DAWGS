package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
	"github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/drivers/pg/query"
	"github.com/specterops/dawgs/graph"
	"github.com/specterops/dawgs/util/size"
)

type KindMapper interface {
	MapKindID(ctx context.Context, kindID int16) (graph.Kind, error)
	MapKindIDs(ctx context.Context, kindIDs []int16) (graph.Kinds, error)
	MapKind(ctx context.Context, kind graph.Kind) (int16, error)
	MapKinds(ctx context.Context, kinds graph.Kinds) ([]int16, error)
	AssertKinds(ctx context.Context, kinds graph.Kinds) ([]int16, error)
}

func KindMapperFromGraphDatabase(graphDB graph.Database) (KindMapper, error) {
	switch typedGraphDB := graphDB.(type) {
	case *Driver:
		return typedGraphDB.SchemaManager, nil
	default:
		return nil, fmt.Errorf("unsupported graph database type: %T", typedGraphDB)
	}
}

type SchemaManager struct {
	defaultGraph          model.Graph
	pool                  *pgxpool.Pool
	hasDefaultGraph       bool
	graphs                map[string]model.Graph
	kindsByID             map[graph.Kind]int16
	kindIDsByKind         map[int16]graph.Kind
	lock                  *sync.RWMutex
	graphQueryMemoryLimit size.Size
	observations          observationState
	lifecycle             lifecycleCoordinator
	backendLock           sync.Mutex
	validatedBackends     map[*pgx.Conn]backendValidation
	backendGeneration     uint64
	backendClock          uint64
	maxValidatedBackends  int
	expectedRuntime       runtimeReadiness
}

type backendValidation struct {
	generation uint64
	lastUsed   uint64
}

func NewSchemaManager(pool *pgxpool.Pool, graphQueryMemoryLimit size.Size) *SchemaManager {
	maxValidatedBackends := 1
	if pool != nil && pool.Config().MaxConns > 0 {
		maxValidatedBackends = int(pool.Config().MaxConns)
	}
	return &SchemaManager{
		pool:                  pool,
		hasDefaultGraph:       false,
		graphs:                map[string]model.Graph{},
		kindsByID:             map[graph.Kind]int16{},
		kindIDsByKind:         map[int16]graph.Kind{},
		lock:                  &sync.RWMutex{},
		graphQueryMemoryLimit: graphQueryMemoryLimit,
		lifecycle:             newLifecycleCoordinator(),
		validatedBackends:     map[*pgx.Conn]backendValidation{},
		maxValidatedBackends:  maxValidatedBackends,
	}
}

func requireActivation() error {
	if !activationReady() {
		return &LayoutError{Code: LayoutErrorActivationNotReady, State: "v2_accepted", Err: ErrActivationNotReady}
	}
	return nil
}

// acquireOperational is the transaction hot-path gate. Startup performs the
// exhaustive catalog, topology, receipt, database, and role validation once;
// ordinary operations only consult local state before acquiring a connection.
func (s *SchemaManager) acquireOperational(ctx context.Context) (*operationalLease, error) {
	generation, err := s.lifecycle.beginOperation()
	if err != nil {
		return nil, err
	}
	if err := requireActivation(); err != nil {
		s.lifecycle.endOperation()
		return nil, err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.lifecycle.endOperation()
		return nil, err
	}
	if !s.lifecycle.isReadyGeneration(generation) {
		conn.Release()
		s.lifecycle.endOperation()
		return nil, &LayoutError{Code: LayoutErrorStartupValidation, State: "asserting", Err: ErrStartupValidation}
	}
	if err := s.ensureBackend(ctx, conn, generation); err != nil {
		discardPooledConnection(ctx, conn)
		s.lifecycle.endOperation()
		return nil, err
	}
	return &operationalLease{manager: s, conn: conn}, nil
}

func (s *SchemaManager) ensureBackend(ctx context.Context, conn *pgxpool.Conn, generation uint64) error {
	physical := conn.Conn()
	s.backendLock.Lock()
	s.backendClock++
	if validation, ok := s.validatedBackends[physical]; ok && validation.generation == generation && s.backendGeneration == generation {
		validation.lastUsed = s.backendClock
		s.validatedBackends[physical] = validation
		s.backendLock.Unlock()
		return nil
	}
	expected := s.expectedRuntime
	expectedGeneration := s.backendGeneration
	s.backendLock.Unlock()
	if expectedGeneration != generation || !s.lifecycle.isReadyGeneration(generation) {
		return &LayoutError{Code: LayoutErrorStartupValidation, State: "asserting", Err: ErrStartupValidation}
	}
	if err := s.validatePhysicalBackend(ctx, conn, expected); err != nil {
		return err
	}
	if !s.lifecycle.isReadyGeneration(generation) {
		return &LayoutError{Code: LayoutErrorStartupValidation, State: "asserting", Err: ErrStartupValidation}
	}
	s.markBackendValidated(physical, generation)
	return nil
}

func (s *SchemaManager) validatePhysicalBackend(ctx context.Context, conn *pgxpool.Conn, expected runtimeReadiness) (returnErr error) {
	started := time.Now()
	defer func() {
		result, code := boundedErrorResult(returnErr)
		s.observations.emit(Observation{Event: ObservationBackendValidation, Result: result,
			LayoutErrorCode: code, StatementCount: 1, Duration: time.Since(started)})
	}()
	if err := validateBackendIdentity(ctx, conn, expected); err != nil {
		return err
	}
	if err := AfterPooledConnectionEstablished(ctx, conn.Conn()); err != nil {
		return err
	}
	return nil
}

func (s *SchemaManager) markBackendValidated(conn *pgx.Conn, generation uint64) {
	s.backendLock.Lock()
	defer s.backendLock.Unlock()
	s.backendClock++
	s.validatedBackends[conn] = backendValidation{generation: generation, lastUsed: s.backendClock}
	for len(s.validatedBackends) > s.maxValidatedBackends {
		var oldest *pgx.Conn
		var oldestUse uint64
		for candidate, validation := range s.validatedBackends {
			if candidate == conn {
				continue
			}
			if oldest == nil || validation.lastUsed < oldestUse {
				oldest = candidate
				oldestUse = validation.lastUsed
			}
		}
		if oldest == nil {
			break
		}
		delete(s.validatedBackends, oldest)
	}
}

func (s *SchemaManager) prepareBackendGeneration(generation uint64, expected runtimeReadiness) {
	s.backendLock.Lock()
	defer s.backendLock.Unlock()
	s.validatedBackends = map[*pgx.Conn]backendValidation{}
	s.backendGeneration = generation
	s.expectedRuntime = expected
}

func (s *SchemaManager) clearValidatedBackends() {
	s.backendLock.Lock()
	defer s.backendLock.Unlock()
	s.validatedBackends = map[*pgx.Conn]backendValidation{}
	s.backendGeneration = 0
	s.expectedRuntime = runtimeReadiness{}
}

func discardPooledConnection(ctx context.Context, conn *pgxpool.Conn) {
	physical := conn.Hijack()
	_ = physical.Close(ctx)
}

func (s *SchemaManager) WriteTransaction(ctx context.Context, txDelegate graph.TransactionDelegate, options ...graph.TransactionOption) error {
	if cfg, err := renderConfig(batchWriteSize, readWriteTxOptions, options); err != nil {
		return err
	} else if conn, err := s.acquireOperational(ctx); err != nil {
		return err
	} else {
		defer conn.Release()

		if tx, err := newTransactionWrapper(ctx, conn.Conn(), s, cfg, true); err != nil {
			return err
		} else {
			defer tx.Close()

			if err := txDelegate(tx); err != nil {
				return err
			}

			return tx.Commit()
		}
	}
}

func (s *SchemaManager) fetch(tx graph.Transaction) error {
	if kinds, err := query.On(tx).SelectKinds(); err != nil {
		return err
	} else {
		if pending, ok := tx.(*transaction); ok && pending.tx != nil {
			for kind, kindID := range kinds {
				pending.pendingKinds[kind] = kindID
				pending.pendingKindIDs[kindID] = kind
			}
		} else {
			s.lock.Lock()
			defer s.lock.Unlock()
			s.kindsByID = kinds
			s.kindIDsByKind = make(map[int16]graph.Kind, len(kinds))
			for kind, kindID := range s.kindsByID {
				s.kindIDsByKind[kindID] = kind
			}
		}
	}

	return nil
}

func (s *SchemaManager) GetKindIDsByKind() map[int16]graph.Kind {
	s.lock.RLock()
	defer s.lock.RUnlock()
	result := make(map[int16]graph.Kind, len(s.kindIDsByKind))
	for kindID, kind := range s.kindIDsByKind {
		result[kindID] = kind
	}
	return result
}

func (s *SchemaManager) Fetch(ctx context.Context) error {
	return s.ReadTransaction(ctx, func(tx graph.Transaction) error {
		return s.fetch(tx)
	}, OptionSetQueryExecMode(pgx.QueryExecModeSimpleProtocol))
}

func (s *SchemaManager) defineKinds(tx graph.Transaction, kinds graph.Kinds) error {
	for _, kind := range kinds {
		if kindID, err := query.On(tx).InsertOrGetKind(kind); err != nil {
			return err
		} else {
			if pending, ok := tx.(*transaction); ok && pending.tx != nil {
				pending.pendingKinds[kind] = kindID
				pending.pendingKindIDs[kindID] = kind
			} else {
				s.kindsByID[kind] = kindID
				s.kindIDsByKind[kindID] = kind
			}
		}
	}

	return nil
}

func (s *SchemaManager) mapKindsInTransaction(tx *transaction, kinds graph.Kinds) ([]int16, graph.Kinds) {
	ids, missing := s.mapKinds(kinds)
	if len(missing) == 0 {
		return ids, nil
	}
	ids = ids[:0]
	missing = missing[:0]
	for _, kind := range kinds {
		if id, found := tx.pendingKinds[kind]; found {
			ids = append(ids, id)
		} else if id, found := s.kindsByID[kind]; found {
			ids = append(ids, id)
		} else {
			missing = append(missing, kind)
		}
	}
	return ids, missing
}

func (s *SchemaManager) assertKindsInTransaction(tx *transaction, kinds graph.Kinds) ([]int16, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	ids, missing := s.mapKindsInTransaction(tx, kinds)
	if len(missing) > 0 {
		if err := s.defineKinds(tx, missing); err != nil {
			return nil, err
		}
		ids, missing = s.mapKindsInTransaction(tx, kinds)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unable to assert kinds: %s", strings.Join(missing.Strings(), ", "))
	}
	return ids, nil
}

func (s *SchemaManager) publishPending(tx *transaction) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for kind, id := range tx.pendingKinds {
		s.kindsByID[kind] = id
		s.kindIDsByKind[id] = kind
	}
	for name, definition := range tx.pendingGraphs {
		s.graphs[name] = definition
	}
	if tx.pendingDefault != nil && !s.hasDefaultGraph {
		s.defaultGraph = *tx.pendingDefault
		s.hasDefaultGraph = true
		s.graphs[tx.pendingDefaultName] = *tx.pendingDefault
	}
	tx.discardPending()
}

func (s *SchemaManager) invalidateCaches() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.defaultGraph = model.Graph{}
	s.hasDefaultGraph = false
	s.graphs = map[string]model.Graph{}
	s.kindsByID = map[graph.Kind]int16{}
	s.kindIDsByKind = map[int16]graph.Kind{}
}

func (s *SchemaManager) mapKinds(kinds graph.Kinds) ([]int16, graph.Kinds) {
	var (
		missingKinds = make(graph.Kinds, 0, len(kinds))
		ids          = make([]int16, 0, len(kinds))
	)

	for _, kind := range kinds {
		if id, hasID := s.kindsByID[kind]; hasID {
			ids = append(ids, id)
		} else {
			missingKinds = append(missingKinds, kind)
		}
	}

	return ids, missingKinds
}

func (s *SchemaManager) MapKind(ctx context.Context, kind graph.Kind) (int16, error) {
	s.lock.RLock()

	if id, hasID := s.kindsByID[kind]; hasID {
		s.lock.RUnlock()
		return id, nil
	}

	s.lock.RUnlock()

	if err := s.Fetch(ctx); err != nil {
		return -1, err
	}

	s.lock.RLock()
	defer s.lock.RUnlock()
	if id, hasID := s.kindsByID[kind]; hasID {
		return id, nil
	} else {
		return -1, fmt.Errorf("unable to map kind: %s", kind.String())
	}
}

func (s *SchemaManager) MapKinds(ctx context.Context, kinds graph.Kinds) ([]int16, error) {
	s.lock.RLock()

	if mappedKinds, missingKinds := s.mapKinds(kinds); len(missingKinds) == 0 {
		s.lock.RUnlock()
		return mappedKinds, nil
	}

	s.lock.RUnlock()

	if err := s.Fetch(ctx); err != nil {
		return nil, err
	}

	s.lock.RLock()
	defer s.lock.RUnlock()
	if mappedKinds, missingKinds := s.mapKinds(kinds); len(missingKinds) == 0 {
		return mappedKinds, nil
	} else {
		return nil, fmt.Errorf("unable to map kinds: %s", strings.Join(missingKinds.Strings(), ", "))
	}
}
func (s *SchemaManager) ReadTransaction(ctx context.Context, txDelegate graph.TransactionDelegate, options ...graph.TransactionOption) error {
	if cfg, err := renderConfig(batchWriteSize, readOnlyTxOptions, options); err != nil {
		return err
	} else if conn, err := s.acquireOperational(ctx); err != nil {
		return err
	} else {
		defer conn.Release()

		return txDelegate(&transaction{
			schemaManager:   s,
			queryExecMode:   cfg.QueryExecMode,
			ctx:             ctx,
			conn:            conn.Conn(),
			targetSchemaSet: false,
		})
	}
}

func (s *SchemaManager) mapKindIDs(kindIDs []int16) (graph.Kinds, []int16) {
	var (
		missingIDs = make([]int16, 0, len(kindIDs))
		kinds      = make(graph.Kinds, 0, len(kindIDs))
	)

	for _, kindID := range kindIDs {
		if kind, hasKind := s.kindIDsByKind[kindID]; hasKind {
			kinds = append(kinds, kind)
		} else {
			missingIDs = append(missingIDs, kindID)
		}
	}

	return kinds, missingIDs
}

func (s *SchemaManager) MapKindID(ctx context.Context, kindID int16) (graph.Kind, error) {
	if kindIDs, err := s.MapKindIDs(ctx, []int16{kindID}); err != nil {
		return nil, err
	} else {
		return kindIDs[0], nil
	}
}

func (s *SchemaManager) MapKindIDs(ctx context.Context, kindIDs []int16) (graph.Kinds, error) {
	s.lock.RLock()

	if kinds, missingKinds := s.mapKindIDs(kindIDs); len(missingKinds) == 0 {
		s.lock.RUnlock()
		return kinds, nil
	}

	s.lock.RUnlock()

	if err := s.Fetch(ctx); err != nil {
		return nil, err
	}

	s.lock.RLock()
	defer s.lock.RUnlock()
	if kinds, missingKinds := s.mapKindIDs(kindIDs); len(missingKinds) == 0 {
		return kinds, nil
	} else {
		return nil, fmt.Errorf("unable to map kind ids: %v", missingKinds)
	}
}

func (s *SchemaManager) assertKinds(ctx context.Context, kinds graph.Kinds) ([]int16, error) {
	s.lock.RLock()
	_, missingKinds := s.mapKinds(kinds)
	s.lock.RUnlock()
	if len(missingKinds) > 0 {
		if err := s.WriteTransaction(ctx, func(tx graph.Transaction) error {
			_, err := s.assertKindsInTransaction(tx.(*transaction), missingKinds)
			return err
		}, OptionSetQueryExecMode(pgx.QueryExecModeSimpleProtocol)); err != nil {
			return nil, err
		}
	}

	s.lock.RLock()
	defer s.lock.RUnlock()
	kindIDs, missingKinds := s.mapKinds(kinds)
	if len(missingKinds) > 0 {
		return nil, fmt.Errorf("unable to assert kinds: %s", strings.Join(missingKinds.Strings(), ", "))
	}
	return kindIDs, nil
}

func (s *SchemaManager) AssertKinds(ctx context.Context, kinds graph.Kinds) ([]int16, error) {
	// Acquire a read-lock first to fast-pass validate if we're missing any kind definitions
	s.lock.RLock()

	if kindIDs, missingKinds := s.mapKinds(kinds); len(missingKinds) == 0 {
		// All kinds are defined. Release the read-lock here before returning
		s.lock.RUnlock()
		return kindIDs, nil
	}

	// Release the read-lock here so that we can acquire a write-lock
	s.lock.RUnlock()
	return s.assertKinds(ctx, kinds)
}

// EnsureEdgeKindPartitions creates every required V2 leaf in the caller's
// transaction. The registry row and physical relation are therefore committed
// or rolled back together and no cache entry can outlive failed DDL.
func (s *SchemaManager) EnsureEdgeKindPartitions(tx graph.Transaction, graphID int32, kindIDs []int16) (map[int16]model.EdgeKindPartition, error) {
	if pending, ok := tx.(*transaction); ok && pending.tx != nil && len(kindIDs) > 0 {
		// Provisioning is idempotent, but an ambiguous commit may still have
		// created a leaf. Fail closed until startup reasserts canonical topology.
		pending.topologyTouched = true
	}
	started := time.Now()
	partitions := make(map[int16]model.EdgeKindPartition, len(kindIDs))
	seen := make(map[int16]struct{}, len(kindIDs))
	for _, kindID := range kindIDs {
		if _, duplicate := seen[kindID]; duplicate {
			continue
		}
		seen[kindID] = struct{}{}
		partition, err := query.On(tx).CreateEdgeKindPartition(graphID, kindID)
		if err != nil {
			result, _ := boundedErrorResult(err)
			s.observations.emit(Observation{Event: ObservationLeafProvisioning, Result: result,
				OperationCount: len(seen), Duration: time.Since(started)})
			return nil, fmt.Errorf("provisioning edge kind leaf for graph %d kind %d: %w", graphID, kindID, err)
		}
		partitions[kindID] = partition
	}
	s.observations.emit(Observation{Event: ObservationLeafProvisioning, Result: "ok",
		OperationCount: len(partitions), Duration: time.Since(started)})
	return partitions, nil
}

func (s *SchemaManager) setDefaultGraph(defaultGraph model.Graph, schema graph.Graph) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.hasDefaultGraph {
		// Another actor has already asserted or otherwise set a default graph
		return
	}

	s.graphs[schema.Name] = defaultGraph

	s.defaultGraph = defaultGraph
	s.hasDefaultGraph = true
}

func (s *SchemaManager) stageDefaultGraph(tx graph.Transaction, defaultGraph model.Graph, schema graph.Graph) {
	if pending, ok := tx.(*transaction); ok && pending.tx != nil {
		pending.pendingDefault = &defaultGraph
		pending.pendingDefaultName = schema.Name
		pending.pendingGraphs[schema.Name] = defaultGraph
		return
	}
	s.setDefaultGraph(defaultGraph, schema)
}

func (s *SchemaManager) SetDefaultGraph(ctx context.Context, schema graph.Graph) error {
	return s.ReadTransaction(ctx, func(tx graph.Transaction) error {
		// Validate the schema if the graph already exists in the database
		if graphModel, err := query.On(tx).SelectGraphByName(schema.Name); err != nil {
			return err
		} else {
			s.stageDefaultGraph(tx, graphModel, schema)
			return nil
		}
	})
}

func (s *SchemaManager) AssertDefaultGraph(ctx context.Context, schema graph.Graph) error {
	return s.WriteTransaction(ctx, func(tx graph.Transaction) error {
		if graphModel, err := s.AssertGraph(tx, schema); err != nil {
			return err
		} else if kindIDs, err := s.assertKindsInTransaction(tx.(*transaction), schema.Edges); err != nil {
			return err
		} else if leaves, err := s.EnsureEdgeKindPartitions(tx, graphModel.ID, kindIDs); err != nil {
			return err
		} else {
			graphModel.Partitions.EdgeKindPartitions = leaves
			s.stageDefaultGraph(tx, graphModel, schema)
		}

		return nil
	})
}

func (s *SchemaManager) DefaultGraph() (model.Graph, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	return s.defaultGraph, s.hasDefaultGraph
}

func (s *SchemaManager) assertGraph(tx graph.Transaction, schema graph.Graph) (model.Graph, error) {
	var assertedGraph model.Graph
	if pending, ok := tx.(*transaction); ok && pending.tx != nil {
		pending.topologyTouched = true
	}

	// Validate the schema if the graph already exists in the database
	if definition, err := query.On(tx).SelectGraphByName(schema.Name); err != nil {
		// ErrNoRows is ignored as it signifies that this graph must be created
		if !errors.Is(err, pgx.ErrNoRows) {
			return model.Graph{}, err
		}

		if newDefinition, err := query.On(tx).CreateGraph(schema); err != nil {
			return model.Graph{}, err
		} else {
			assertedGraph = newDefinition
		}
	} else if assertedDefinition, err := query.On(tx).AssertGraph(schema, definition); err != nil {
		return model.Graph{}, err
	} else {
		// Graph existed and may have been updated
		assertedGraph = assertedDefinition
	}

	// Cache the graph definition and return it
	if pending, ok := tx.(*transaction); ok && pending.tx != nil {
		pending.pendingGraphs[schema.Name] = assertedGraph
	} else {
		s.graphs[schema.Name] = assertedGraph
	}
	return assertedGraph, nil
}

func (s *SchemaManager) AssertGraph(tx graph.Transaction, schema graph.Graph) (model.Graph, error) {
	if pending, ok := tx.(*transaction); ok {
		if graphInstance, found := pending.pendingGraphs[schema.Name]; found {
			return graphInstance, nil
		}
	}
	// Acquire a read-lock first to fast-pass validate if we're missing the graph definitions
	s.lock.RLock()

	if graphInstance, isDefined := s.graphs[schema.Name]; isDefined {
		// The graph is defined. Release the read-lock here before returning
		s.lock.RUnlock()
		return graphInstance, nil
	}

	// Release the read-lock here so that we can acquire a write-lock next
	s.lock.RUnlock()

	s.lock.Lock()
	defer s.lock.Unlock()

	if graphInstance, isDefined := s.graphs[schema.Name]; isDefined {
		// The graph was defined by a different actor between the read unlock and the write lock, return it
		return graphInstance, nil
	}

	return s.assertGraph(tx, schema)
}

// lookupGraph resolves an existing graph without provisioning or reconciling
// schema. It is the target-resolution path for read-only transactions.
func (s *SchemaManager) lookupGraph(tx graph.Transaction, name string) (model.Graph, error) {
	s.lock.RLock()
	graphModel, found := s.graphs[name]
	s.lock.RUnlock()
	if found {
		return graphModel, nil
	}

	graphModel, err := query.On(tx).SelectGraphByName(name)
	if err != nil {
		return model.Graph{}, err
	}
	s.lock.Lock()
	if cached, exists := s.graphs[name]; exists {
		graphModel = cached
	} else {
		s.graphs[name] = graphModel
	}
	s.lock.Unlock()
	return graphModel, nil
}

func (s *SchemaManager) assertSchema(tx graph.Transaction, schema graph.Schema) error {
	if err := query.On(tx).CreateSchema(); err != nil {
		return err
	}

	if err := s.fetch(tx); err != nil {
		return err
	}

	for _, graphSchema := range schema.Graphs {
		if _, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Nodes); err != nil {
			return err
		}

		if _, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Edges); err != nil {
			return err
		}
	}

	for _, graphSchema := range schema.Graphs {
		graphModel, err := s.assertGraph(tx, graphSchema)
		if err != nil {
			return err
		}
		kindIDs, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Edges)
		if err != nil {
			return err
		}
		leaves, err := s.EnsureEdgeKindPartitions(tx, graphModel.ID, kindIDs)
		if err != nil {
			return err
		}
		graphModel.Partitions.EdgeKindPartitions = leaves
		tx.(*transaction).pendingGraphs[graphSchema.Name] = graphModel
	}

	return nil
}

// reconcileAcceptedSchema uses only the restricted accepted-target contract:
// kind rows plus the canonical security-definer graph and leaf provisioners.
// Installation and owner-managed index/constraint reconciliation are kept out
// of this path deliberately.
func (s *SchemaManager) reconcileAcceptedSchema(tx graph.Transaction, schema graph.Schema) error {
	if err := s.fetch(tx); err != nil {
		return fmt.Errorf("refreshing accepted kinds: %w", err)
	}

	for _, graphSchema := range schema.Graphs {
		if _, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Nodes); err != nil {
			return fmt.Errorf("asserting node kinds for graph %q: %w", graphSchema.Name, err)
		}
		if _, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Edges); err != nil {
			return fmt.Errorf("asserting edge kinds for graph %q: %w", graphSchema.Name, err)
		}
	}

	for _, graphSchema := range schema.Graphs {
		graphModel, err := query.On(tx).SelectGraphByName(graphSchema.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			tx.(*transaction).topologyTouched = true
			graphModel, err = query.On(tx).ProvisionGraph(graphSchema.Name)
		}
		if err != nil {
			return fmt.Errorf("selecting or provisioning graph %q: %w", graphSchema.Name, err)
		}
		kindIDs, err := s.assertKindsInTransaction(tx.(*transaction), graphSchema.Edges)
		if err != nil {
			return fmt.Errorf("mapping edge kinds for graph %q: %w", graphSchema.Name, err)
		}
		leaves, err := s.EnsureEdgeKindPartitions(tx, graphModel.ID, kindIDs)
		if err != nil {
			return fmt.Errorf("provisioning edge leaves for graph %q: %w", graphSchema.Name, err)
		}
		graphModel.Partitions.EdgeKindPartitions = leaves
		tx.(*transaction).pendingGraphs[graphSchema.Name] = graphModel
	}

	return nil
}

func (s *SchemaManager) AssertSchema(ctx context.Context, schema graph.Schema) error {
	return s.assertStartup(ctx, &schema)
}

// AssertRuntime validates an already-provisioned target without changing its
// schema. It is the startup entry point for read-only services and tools.
func (s *SchemaManager) AssertRuntime(ctx context.Context) error {
	return s.assertStartup(ctx, nil)
}

func (s *SchemaManager) assertStartup(ctx context.Context, schema *graph.Schema) (returnErr error) {
	key, err := startupRequestKey(schema)
	if err != nil {
		return err
	}
	ticket, err := s.lifecycle.beginAssertion(ctx, key)
	if ticket == nil {
		return err
	}
	defer func() { ticket.finish(returnErr) }()
	if err := ticket.waitForDrain(ctx); err != nil {
		return err
	}
	if err := validateStaticSessionPool(s.pool); err != nil {
		return err
	}

	started := time.Now()
	var observedState string
	var validationStatements int
	s.invalidateCaches()
	s.clearValidatedBackends()
	defer func() {
		result, code := boundedErrorResult(returnErr)
		s.observations.emit(Observation{Event: ObservationStartupValidation, Result: result,
			LayoutState: observedState, LayoutErrorCode: code, StatementCount: validationStatements,
			Duration: time.Since(started)})
		if returnErr != nil {
			s.invalidateCaches()
			s.clearValidatedBackends()
		}
	}()

	cfg, err := renderConfig(batchWriteSize, readWriteTxOptions, []graph.TransactionOption{OptionSetQueryExecMode(pgx.QueryExecModeSimpleProtocol)})
	if err != nil {
		return err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}

	if schema != nil {
		err = func() error {
			tx, err := newTransactionWrapper(ctx, conn, s, cfg, true)
			if err != nil {
				return err
			}
			defer tx.Close()
			// Serialize schema assertion across service instances while retaining
			// transaction-scoped, automatically released ownership.
			if _, err = tx.tx.Exec(ctx, "select pg_catalog.pg_advisory_xact_lock($1)", int64(0x44415747535632)); err != nil {
				return err
			}
			facts, err := classifyQuerier(ctx, tx.tx)
			if err != nil {
				return err
			}
			observedState = string(facts.State)
			validationStatements += facts.GuardStatements
			if err := requireOperational(facts, true); err != nil {
				return err
			}

			if facts.State == layout.StateV2Accepted {
				err = s.reconcileAcceptedSchema(tx, *schema)
			} else {
				err = s.assertSchema(tx, *schema)
			}
			if err == nil && schema.DefaultGraph.Name != "" {
				defaultGraphSchema := schema.DefaultGraph
				for _, graphSchema := range schema.Graphs {
					if graphSchema.Name == schema.DefaultGraph.Name {
						defaultGraphSchema = graphSchema
						break
					}
				}
				var graphModel model.Graph
				graphModel, err = s.AssertGraph(tx, defaultGraphSchema)
				if err == nil {
					var kindIDs []int16
					kindIDs, err = s.assertKindsInTransaction(tx, defaultGraphSchema.Edges)
					if err == nil {
						var leaves map[int16]model.EdgeKindPartition
						leaves, err = s.EnsureEdgeKindPartitions(tx, graphModel.ID, kindIDs)
						if err == nil {
							graphModel.Partitions.EdgeKindPartitions = leaves
							s.stageDefaultGraph(tx, graphModel, defaultGraphSchema)
						}
					}
				}
			}
			if err != nil {
				return err
			}
			return tx.Commit()
		}()
		conn.Release()
		if err != nil {
			return err
		}
	} else {
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			conn.Release()
			return err
		}
		if _, err = tx.Exec(ctx, "select pg_catalog.pg_advisory_xact_lock_shared($1)", int64(0x44415747535632)); err == nil {
			var facts operationalFacts
			facts, err = classifyQuerier(ctx, tx)
			if err == nil {
				observedState = string(facts.State)
				validationStatements += facts.GuardStatements
				err = requireOperational(facts, false)
			}
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		conn.Release()
		if err != nil {
			return err
		}
	}

	// Schema DDL can replace composite OIDs. Drain all pre-assertion backends,
	// then prove the committed state and initialize types on a fresh backend.
	if schema != nil {
		s.pool.Reset()
		s.clearValidatedBackends()
	}
	conn, err = s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	releasePostconditionConnection := true
	defer func() {
		if releasePostconditionConnection {
			conn.Release()
		}
	}()
	postFacts, err := classifyConnection(ctx, conn)
	if err != nil {
		return err
	}
	observedState = string(postFacts.State)
	validationStatements += postFacts.GuardStatements
	if err := requireOperational(postFacts, false); err != nil {
		return err
	}
	if _, err := layoutHealth(ctx, conn, postFacts.Facts); err != nil {
		return err
	}
	if err := s.validatePhysicalBackend(ctx, conn, postFacts.Readiness); err != nil {
		discardPooledConnection(ctx, conn)
		releasePostconditionConnection = false
		return err
	}
	s.prepareBackendGeneration(ticket.generation(), postFacts.Readiness)
	s.markBackendValidated(conn.Conn(), ticket.generation())

	readWrapper, err := newTransactionWrapper(ctx, conn, s, &Config{
		Options: readOnlyTxOptions, QueryExecMode: pgx.QueryExecModeSimpleProtocol,
		QueryResultFormats: pgx.QueryResultFormats{pgx.BinaryFormatCode}, BatchWriteSize: batchWriteSize,
	}, false)
	if err != nil {
		return err
	}
	if err := s.fetch(readWrapper); err != nil {
		return err
	}
	return nil
}
