//go:build pg_physical

package pg

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/schema"
	pgmodel "github.com/specterops/dawgs/drivers/pg/model"
	"github.com/specterops/dawgs/drivers/pg/query"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

func TestPGPhysicalEdgePropertyConstraintRegistryLifecycle(t *testing.T) {
	if os.Getenv("DAWGS_TEST_POSTGRES_DISPOSABLE") != "1" {
		t.Skip("destructive pg_physical test requires DAWGS_TEST_POSTGRES_DISPOSABLE=1")
	}
	connectionString := os.Getenv("CONNECTION_STRING")
	if connectionString == "" {
		t.Fatal("CONNECTION_STRING is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, connectionString)
	require.NoError(t, err)
	defer pool.Close()
	connection, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer connection.Release()
	_, err = connection.Conn().PgConn().Exec(ctx, schema.Up()).ReadAll()
	require.NoError(t, err)
	_, err = connection.Conn().PgConn().Exec(ctx, `
insert into public.graph(id,name) values(1,'constraints');
insert into public.kind(id,name) values(1,'KIND_A'),(2,'KIND_B');
create table public.node_1 partition of public.node for values in (1);
create table public.edge_1 partition of public.edge for values in (1) partition by list(kind_id);
create table public.edge_1_1 partition of public.edge_1 for values in (1);
create table public.edge_1_2 partition of public.edge_1 for values in (2);
insert into public.dawgs_edge_kind_leaf(graph_id,kind_id,lifecycle_state) values(1,1,'active'),(1,2,'active');
insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values
  (1,1,10,11,1,'{"identity":"A"}'),(2,1,12,13,2,'{"identity":"B"}');`).ReadAll()
	require.NoError(t, err)

	constraint := graph.Constraint{Name: "identity", Field: "identity", Type: graph.BTreeIndex}
	logicalName := pgmodel.ConstraintName(pgmodel.EdgePartitionTableName(1), constraint)
	require.NoError(t, runPhysicalTransaction(ctx, connection, func(tx *transaction) error {
		return query.On(tx).CreateEdgePropertyConstraint(1, constraint)
	}))

	t.Run("concurrent property and identity claims serialize", func(t *testing.T) {
		start := make(chan struct{})
		errorsByInsert := make([]error, 2)
		statements := []string{
			`insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values(100,1,1000,1001,1,'{"identity":"CONCURRENT"}')`,
			`insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values(101,1,1002,1003,2,'{"identity":"CONCURRENT"}')`,
		}
		var wait sync.WaitGroup
		wait.Add(len(statements))
		for index, statement := range statements {
			go func() {
				defer wait.Done()
				<-start
				_, errorsByInsert[index] = pool.Exec(ctx, statement)
			}()
		}
		close(start)
		wait.Wait()
		successes := 0
		for _, insertErr := range errorsByInsert {
			if insertErr == nil {
				successes++
			}
		}
		require.Equal(t, 1, successes, "exactly one concurrent property claimant must commit: %v", errorsByInsert)
		var propertyClaims int
		require.NoError(t, connection.QueryRow(ctx, `select count(*) from public.edge_property_unique_value where graph_id=1 and property_value='CONCURRENT'`).Scan(&propertyClaims))
		require.Equal(t, 1, propertyClaims)
		_, err = connection.Exec(ctx, `delete from public.edge where graph_id=1 and id in(100,101)`)
		require.NoError(t, err)

		start = make(chan struct{})
		errorsByInsert = make([]error, 2)
		statements = []string{
			`insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values(200,1,2000,2001,1,'{"identity":"ID-A"}')`,
			`insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties) values(200,1,2002,2003,2,'{"identity":"ID-B"}')`,
		}
		wait.Add(len(statements))
		for index, statement := range statements {
			go func() {
				defer wait.Done()
				<-start
				_, errorsByInsert[index] = pool.Exec(ctx, statement)
			}()
		}
		close(start)
		wait.Wait()
		successes = 0
		for _, insertErr := range errorsByInsert {
			if insertErr == nil {
				successes++
			}
		}
		require.Equal(t, 1, successes, "exactly one graph-wide edge ID claimant must commit: %v", errorsByInsert)
		var identityClaims int
		require.NoError(t, connection.QueryRow(ctx, `select count(*) from public.edge_identity where graph_id=1 and edge_id=200`).Scan(&identityClaims))
		require.Equal(t, 1, identityClaims)
		_, err = connection.Exec(ctx, `delete from public.edge where graph_id=1 and id=200`)
		require.NoError(t, err)
	})
	_, err = connection.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties)
values(3,1,14,15,2,'{"identity":"A"}')`)
	require.Error(t, err)
	_, err = connection.Exec(ctx, `insert into public.edge_1_2(id,graph_id,start_id,end_id,kind_id,properties)
values(3,1,14,15,2,'{"identity":"A"}')`)
	require.Error(t, err)
	_, err = connection.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties)
values(1,1,16,17,2,'{"identity":"C"}')`)
	require.Error(t, err)
	_, err = connection.Exec(ctx, `update public.edge_1_1 set kind_id=2 where graph_id=1 and id=1`)
	require.ErrorContains(t, err, "routing columns are immutable")
	_, err = connection.Conn().CopyFrom(ctx, pgx.Identifier{"public", "edge_1_2"},
		[]string{"id", "graph_id", "start_id", "end_id", "kind_id", "properties"},
		pgx.CopyFromRows([][]any{{int64(4), int32(1), int64(18), int64(19), int16(2), `{"identity":"A"}`}}))
	require.Error(t, err)

	require.NoError(t, runPhysicalTransaction(ctx, connection, func(tx *transaction) error {
		return query.On(tx).DropEdgePropertyConstraint(1, logicalName)
	}))
	_, err = connection.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties)
values(3,1,14,15,2,'{"identity":"A"}')`)
	require.NoError(t, err)

	err = runPhysicalTransaction(ctx, connection, func(tx *transaction) error {
		return query.On(tx).CreateEdgePropertyConstraint(1, constraint)
	})
	require.Error(t, err)
	var lifecycle string
	require.NoError(t, connection.QueryRow(ctx, `select lifecycle_state from public.edge_property_constraint
where graph_id=1 and property_name='identity'`).Scan(&lifecycle))
	require.Equal(t, "tombstoned", lifecycle)

	_, err = connection.Exec(ctx, `delete from public.edge where graph_id=1 and id=3`)
	require.NoError(t, err)
	require.NoError(t, runPhysicalTransaction(ctx, connection, func(tx *transaction) error {
		return query.On(tx).CreateEdgePropertyConstraint(1, constraint)
	}))
	require.NoError(t, connection.QueryRow(ctx, `select lifecycle_state from public.edge_property_constraint
where graph_id=1 and property_name='identity'`).Scan(&lifecycle))
	require.Equal(t, "active", lifecycle)
}

func runPhysicalTransaction(ctx context.Context, connection *pgxpool.Conn, delegate func(*transaction) error) error {
	wrapper, err := newTransactionWrapper(ctx, connection, NewSchemaManager(nil, 0), &Config{
		Options: pgx.TxOptions{}, QueryExecMode: pgx.QueryExecModeCacheStatement,
	}, true)
	if err != nil {
		return err
	}
	defer wrapper.Close()
	if err := delegate(wrapper); err != nil {
		return err
	}
	return wrapper.Commit()
}
