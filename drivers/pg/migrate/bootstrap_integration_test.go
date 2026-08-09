//go:build bootstrap_integration

package migrate

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/cypher/models/pgsql/translate"
	pgdriver "github.com/specterops/dawgs/drivers/pg"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

func TestBootstrapRequiresDirectTargetHandoffAndRetainsActivationInterlock(t *testing.T) {
	if os.Getenv("DAWGS_TEST_POSTGRES_DISPOSABLE") != "1" {
		t.Skip("bootstrap integration requires the disposable role-aware harness")
	}
	operatorConnection := os.Getenv("CONNECTION_STRING")
	targetConnection := os.Getenv("PG_TEST_TARGET_CONNECTION_STRING")
	if operatorConnection == "" || targetConnection == "" {
		t.Fatal("operator and target connections are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := New(ctx, operatorConnection)
	require.NoError(t, err)
	defer engine.Close()
	engine.AllowUnreleasedDisposable = true
	engine.RetirementConnectionString = os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING")
	require.NotEmpty(t, engine.RetirementConnectionString)

	installation, err := engine.Bootstrap(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_pending_handoff", installation.State)
	require.NotEmpty(t, installation.MigrationID)

	confirmation, err := engine.ConfirmBootstrap(ctx, installation.MigrationID, targetConnection)
	require.NoError(t, err)
	accepted, err := engine.CompleteBootstrap(ctx, installation.MigrationID, confirmation.ConfirmationToken, targetConnection)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", accepted.State)
	require.Len(t, accepted.Blockers, 1)
	require.Contains(t, accepted.Blockers[0], "activation_not_ready")

	status, err := engine.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2_accepted", status.State)
	var recordedOwner, objectOwner string
	require.NoError(t, engine.Pool.QueryRow(ctx, `select owner_role_name from public.dawgs_schema_layout where singleton`).Scan(&recordedOwner))
	require.NoError(t, engine.Pool.QueryRow(ctx, `select pg_catalog.pg_get_userbyid(relowner) from pg_catalog.pg_class where oid='public.edge'::pg_catalog.regclass`).Scan(&objectOwner))
	require.Equal(t, os.Getenv("PG_TEST_OWNER_ROLE"), recordedOwner)
	require.Equal(t, recordedOwner, objectOwner)
	var cleanupApplicable bool
	var cleanupEntries int64
	var cleanupDigest string
	require.NoError(t, engine.Pool.QueryRow(ctx, `select c.applicable,c.map_digest,
(select count(*) from public.dawgs_legacy_cleanup_map m where m.scope_kind=c.scope_kind and m.scope_id=c.scope_id)
from public.dawgs_legacy_cleanup_scope c where c.scope_kind='bootstrap' and c.scope_id=$1`, installation.MigrationID).Scan(
		&cleanupApplicable, &cleanupDigest, &cleanupEntries))
	require.False(t, cleanupApplicable)
	require.Len(t, cleanupDigest, 64)
	require.Zero(t, cleanupEntries)
	targetConnectionHandle, err := pgx.Connect(ctx, targetConnection)
	require.NoError(t, err)
	var graphID int32
	require.NoError(t, targetConnectionHandle.QueryRow(ctx, `select public.dawgs_provision_graph('bootstrap_graph')`).Scan(&graphID))
	require.EqualValues(t, 1, graphID)
	var kindID int16
	require.NoError(t, targetConnectionHandle.QueryRow(ctx, `insert into public.kind(name) values('BOOTSTRAP_KIND') returning id`).Scan(&kindID))
	var leafName string
	require.NoError(t, targetConnectionHandle.QueryRow(ctx, `select public.dawgs_provision_edge_kind_leaf($1,$2)`, graphID, kindID).Scan(&leafName))
	require.Equal(t, "edge_1_1", leafName)
	_, err = targetConnectionHandle.Exec(ctx, `insert into public.edge(id,graph_id,start_id,end_id,kind_id,properties)
values(1,1,10,11,1,'{}')`)
	require.NoError(t, err)
	_, err = targetConnectionHandle.Exec(ctx, `insert into public.dawgs_edge_kind_leaf(graph_id,kind_id) values(1,1)`)
	require.Error(t, err)
	_, err = targetConnectionHandle.Exec(ctx, `insert into public.graph(name) values('forbidden_direct_graph')`)
	require.Error(t, err)
	_, err = targetConnectionHandle.Exec(ctx, `create table public.forbidden_target_ddl(id integer)`)
	require.Error(t, err)
	require.NoError(t, targetConnectionHandle.Close(ctx))

	targetPool, err := pgxpool.New(ctx, targetConnection)
	require.NoError(t, err)
	defer targetPool.Close()
	targetDriver := pgdriver.NewDriver(0, targetPool)
	observations := &bootstrapObservationSink{}
	targetDriver.SetObservationSink(observations)
	health, readinessErr := targetDriver.Readiness(ctx)
	require.Equal(t, "v2_accepted", health.State)
	require.Equal(t, targetRoleFromEnvironment(t), health.TargetRoleName)
	require.NotEmpty(t, health.TopologyFingerprint)
	if rehearsalActivation {
		require.NoError(t, readinessErr)
		rehearsalSchema := graph.Schema{Graphs: []graph.Graph{{
			Name:  "accepted_rehearsal_graph",
			Nodes: graph.Kinds{graph.StringKind("REHEARSAL_NODE")},
			Edges: graph.Kinds{graph.StringKind("REHEARSAL_EDGE_A"), graph.StringKind("REHEARSAL_EDGE_B")},
		}}}
		require.NoError(t, targetDriver.AssertSchema(ctx, rehearsalSchema))
		var provisionedGraphs, provisionedLeaves int64
		require.NoError(t, targetPool.QueryRow(ctx, `select count(*) from public.graph where name='accepted_rehearsal_graph'`).Scan(&provisionedGraphs))
		require.NoError(t, targetPool.QueryRow(ctx, `select count(*) from public.dawgs_edge_kind_leaf l join public.graph g on g.id=l.graph_id where g.name='accepted_rehearsal_graph' and l.lifecycle_state='active'`).Scan(&provisionedLeaves))
		require.EqualValues(t, 1, provisionedGraphs)
		require.EqualValues(t, 2, provisionedLeaves)
		var graphXminBefore, leafXminBefore, graphXminAfter, leafXminAfter string
		require.NoError(t, targetPool.QueryRow(ctx, `select
(select xmin::text from public.graph where name='accepted_rehearsal_graph'),
(select pg_catalog.md5(pg_catalog.string_agg(l.xmin::text,',' order by l.kind_id)) from public.dawgs_edge_kind_leaf l join public.graph g on g.id=l.graph_id where g.name='accepted_rehearsal_graph')`).Scan(&graphXminBefore, &leafXminBefore))
		require.NoError(t, targetDriver.AssertSchema(ctx, rehearsalSchema))
		require.NoError(t, targetPool.QueryRow(ctx, `select
(select xmin::text from public.graph where name='accepted_rehearsal_graph'),
(select pg_catalog.md5(pg_catalog.string_agg(l.xmin::text,',' order by l.kind_id)) from public.dawgs_edge_kind_leaf l join public.graph g on g.id=l.graph_id where g.name='accepted_rehearsal_graph')`).Scan(&graphXminAfter, &leafXminAfter))
		require.Equal(t, graphXminBefore, graphXminAfter)
		require.Equal(t, leafXminBefore, leafXminAfter)
		assertionErrors := make(chan error, 4)
		var assertions sync.WaitGroup
		for range 4 {
			assertions.Add(1)
			go func() {
				defer assertions.Done()
				assertionErrors <- targetDriver.AssertSchema(ctx, rehearsalSchema)
			}()
		}
		assertions.Wait()
		close(assertionErrors)
		for assertionErr := range assertionErrors {
			require.NoError(t, assertionErr)
		}
		provisioningBeforeRead := observations.leafProvisioningCount
		require.NoError(t, targetDriver.ReadTransaction(ctx, func(tx graph.Transaction) error {
			result := tx.WithGraph(rehearsalSchema.Graphs[0]).Query(`match ()-[r:REHEARSAL_EDGE_A]->() return count(r)`, nil)
			defer result.Close()
			if !result.Next() {
				return result.Error()
			}
			var count int64
			return result.Scan(&count)
		}))
		require.Equal(t, provisioningBeforeRead, observations.leafProvisioningCount)

		var rehearsalGraphID int32
		require.NoError(t, targetPool.QueryRow(ctx, `select id from public.graph where name='accepted_rehearsal_graph'`).Scan(&rehearsalGraphID))
		parsed, err := frontend.ParseCypher(frontend.NewContext(), `match p = (:REHEARSAL_NODE)-[:REHEARSAL_EDGE_A|REHEARSAL_EDGE_B*1..]->(:REHEARSAL_NODE) return p`)
		require.NoError(t, err)
		translated, err := translate.Translate(ctx, parsed, targetDriver.KindMapper(), nil, rehearsalGraphID)
		require.NoError(t, err)
		sqlQuery, err := translate.Translated(translated)
		require.NoError(t, err)
		var explainJSON []byte
		require.NoError(t, targetPool.QueryRow(ctx, `explain (analyze, buffers, format json) `+sqlQuery).Scan(&explainJSON))
		typedKindIDs, err := targetDriver.KindMapper().MapKinds(ctx, rehearsalSchema.Graphs[0].Edges)
		require.NoError(t, err)
		inventory := make([]pgdriver.LeafReference, 0, len(typedKindIDs))
		for _, kindID := range typedKindIDs {
			inventory = append(inventory, pgdriver.LeafReference{GraphID: rehearsalGraphID, KindID: kindID})
		}
		structured, err := pgdriver.DecodeStructuredExplain(explainJSON, inventory)
		require.NoError(t, err)
		require.ElementsMatch(t, inventory, structured.Leaves.Planned)
		require.NoError(t, targetDriver.ReadTransaction(ctx, func(graph.Transaction) error { return nil }))
		require.Equal(t, "ok", observations.lastStartupValidation.Result)
		require.Positive(t, observations.lastStartupValidation.StatementCount)
		require.Zero(t, observations.runtimeGuardCount)

		admin, err := pgx.Connect(ctx, os.Getenv("PG_TEST_ADMIN_CONNECTION_STRING"))
		require.NoError(t, err)
		_, err = admin.Exec(ctx, `alter table public.edge disable trigger dawgs_10_claim_edge`)
		require.NoError(t, err)
		require.NoError(t, admin.Close(ctx))
		// Warm transactions intentionally do not rescan topology. Explicit
		// readiness and the next service-start assertion detect managed drift.
		delegateCalled := false
		require.NoError(t, targetDriver.ReadTransaction(ctx, func(graph.Transaction) error {
			delegateCalled = true
			return nil
		}))
		require.True(t, delegateCalled)
		_, err = targetDriver.Readiness(ctx)
		require.ErrorIs(t, err, pgdriver.ErrLayoutDrift)
		require.ErrorIs(t, targetDriver.AssertRuntime(ctx), pgdriver.ErrLayoutDrift)
	} else {
		require.ErrorIs(t, readinessErr, pgdriver.ErrActivationNotReady)
		require.ErrorIs(t, targetDriver.AssertRuntime(ctx), pgdriver.ErrActivationNotReady)
		require.ErrorIs(t, targetDriver.ReadTransaction(ctx, func(graph.Transaction) error { return nil }), pgdriver.ErrStartupValidation)
	}
}

type bootstrapObservationSink struct {
	lock                  sync.Mutex
	lastStartupValidation pgdriver.Observation
	leafProvisioningCount int
	runtimeGuardCount     int
}

func (s *bootstrapObservationSink) Observe(observation pgdriver.Observation) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if observation.Event == pgdriver.ObservationStartupValidation {
		s.lastStartupValidation = observation
	}
	if observation.Event == pgdriver.ObservationRuntimeGuard {
		s.runtimeGuardCount++
	}
	if observation.Event == pgdriver.ObservationLeafProvisioning {
		s.leafProvisioningCount++
	}
}

func targetRoleFromEnvironment(t *testing.T) string {
	t.Helper()
	role := os.Getenv("PG_TEST_TARGET_ROLE")
	require.NotEmpty(t, role)
	return role
}
