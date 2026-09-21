package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

type commitTestTx struct{ commitErr error }

func (s *commitTestTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("unused") }
func (s *commitTestTx) Commit(context.Context) error          { return s.commitErr }
func (s *commitTestTx) Rollback(context.Context) error        { return nil }
func (s *commitTestTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unused")
}
func (s *commitTestTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (s *commitTestTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (s *commitTestTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unused")
}
func (s *commitTestTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unused")
}
func (s *commitTestTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unused")
}
func (s *commitTestTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (s *commitTestTx) Conn() *pgx.Conn                                  { return nil }

func TestTransactionCommitPublishesOnlyKnownSuccess(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		commitErr error
		published bool
	}{
		{name: "success", published: true},
		{name: "unknown outcome", commitErr: errors.New("connection lost during commit")},
		{name: "commit rollback", commitErr: pgx.ErrTxCommitRollback},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := NewSchemaManager(nil, 0)
			if testCase.commitErr != nil {
				manager.kindsByID[graph.StringKind("stale")] = 7
				manager.kindIDsByKind[7] = graph.StringKind("stale")
				manager.graphs["stale"] = model.Graph{ID: 7, Name: "stale"}
				manager.defaultGraph = model.Graph{ID: 7, Name: "stale"}
				manager.hasDefaultGraph = true
			}

			kind := graph.StringKind("new")
			graphValue := model.Graph{ID: 9, Name: "new"}
			tx := &transaction{ctx: context.Background(), schemaManager: manager, tx: &commitTestTx{commitErr: testCase.commitErr},
				pendingKinds: map[graph.Kind]int16{kind: 9}, pendingKindIDs: map[int16]graph.Kind{9: kind},
				pendingGraphs: map[string]model.Graph{"new": graphValue}, pendingDefault: &graphValue, pendingDefaultName: "new"}

			err := tx.Commit()
			if testCase.commitErr == nil {
				require.NoError(t, err)
				require.Equal(t, int16(9), manager.kindsByID[kind])
				require.Equal(t, graphValue, manager.graphs["new"])
				require.Equal(t, graphValue, manager.defaultGraph)
			} else {
				require.ErrorIs(t, err, testCase.commitErr)
				require.Empty(t, manager.kindsByID)
				require.Empty(t, manager.kindIDsByKind)
				require.Empty(t, manager.graphs)
				require.False(t, manager.hasDefaultGraph)
			}
			require.Empty(t, tx.pendingKinds)
			require.Empty(t, tx.pendingKindIDs)
			require.Empty(t, tx.pendingGraphs)
			require.Nil(t, tx.pendingDefault)
		})
	}
}

func TestAmbiguousTopologyCommitDemotesRuntimeUntilReassertion(t *testing.T) {
	manager := NewSchemaManager(nil, 0)
	manager.lifecycle.phase = lifecycleReady
	manager.lifecycle.generation = 7
	manager.backendGeneration = 7
	manager.validatedBackends[nil] = backendValidation{generation: 7}

	tx := &transaction{
		ctx:             context.Background(),
		schemaManager:   manager,
		tx:              &commitTestTx{commitErr: errors.New("connection lost during commit")},
		topologyTouched: true,
		pendingGraphs:   map[string]model.Graph{},
		pendingKinds:    map[graph.Kind]int16{},
		pendingKindIDs:  map[int16]graph.Kind{},
	}
	require.Error(t, tx.Commit())
	_, err := manager.lifecycle.beginOperation()
	require.ErrorIs(t, err, ErrStartupValidation)
	require.Empty(t, manager.validatedBackends)
	require.Zero(t, manager.backendGeneration)
}

func TestDefiniteTopologyRollbackKeepsAssertedGeneration(t *testing.T) {
	manager := NewSchemaManager(nil, 0)
	manager.lifecycle.phase = lifecycleReady
	manager.lifecycle.generation = 7
	tx := &transaction{
		ctx:             context.Background(),
		schemaManager:   manager,
		tx:              &commitTestTx{commitErr: pgx.ErrTxCommitRollback},
		topologyTouched: true,
		pendingGraphs:   map[string]model.Graph{},
		pendingKinds:    map[graph.Kind]int16{},
		pendingKindIDs:  map[int16]graph.Kind{},
	}
	require.ErrorIs(t, tx.Commit(), pgx.ErrTxCommitRollback)
	generation, err := manager.lifecycle.beginOperation()
	require.NoError(t, err)
	require.Equal(t, uint64(7), generation)
	manager.lifecycle.endOperation()
}
