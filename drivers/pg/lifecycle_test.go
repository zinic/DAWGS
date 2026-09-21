package pg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func readyLifecycle(t *testing.T) *lifecycleCoordinator {
	t.Helper()
	lifecycle := newLifecycleCoordinator()
	ticket, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)
	require.NoError(t, ticket.waitForDrain(context.Background()))
	ticket.finish(nil)
	return &lifecycle
}

func TestLifecycleAssertionDrainsAdmittedOperations(t *testing.T) {
	lifecycle := readyLifecycle(t)
	_, err := lifecycle.beginOperation()
	require.NoError(t, err)

	ticket, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)
	drained := make(chan error, 1)
	go func() { drained <- ticket.waitForDrain(context.Background()) }()

	select {
	case <-drained:
		t.Fatal("assertion passed the drain barrier while an operation was active")
	case <-time.After(20 * time.Millisecond):
	}
	lifecycle.endOperation()
	require.NoError(t, <-drained)
	ticket.finish(nil)
	_, err = lifecycle.beginOperation()
	require.NoError(t, err)
	lifecycle.endOperation()
}

func TestCancelledAssertionFollowerDoesNotChangeLeaderResult(t *testing.T) {
	lifecycle := readyLifecycle(t)
	leader, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lifecycle.beginAssertion(ctx, "runtime")
	require.ErrorIs(t, err, context.Canceled)

	leader.finish(nil)
	_, err = lifecycle.beginOperation()
	require.NoError(t, err)
	lifecycle.endOperation()
}

func TestDifferentConcurrentAssertionsFailWithTypedConflict(t *testing.T) {
	lifecycle := readyLifecycle(t)
	leader, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)

	_, err = lifecycle.beginAssertion(context.Background(), "schema:different")
	require.ErrorIs(t, err, ErrStartupAssertionConflict)
	leader.finish(nil)
}

func TestAssertionFailureLeavesLifecycleClosedToOperationsAndRetryable(t *testing.T) {
	lifecycle := readyLifecycle(t)
	leader, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)
	leader.finish(errors.New("validation failed"))

	_, err = lifecycle.beginOperation()
	require.ErrorIs(t, err, ErrStartupValidation)
	retry, err := lifecycle.beginAssertion(context.Background(), "runtime")
	require.NoError(t, err)
	retry.finish(nil)
	_, err = lifecycle.beginOperation()
	require.NoError(t, err)
	lifecycle.endOperation()
}

func TestBackendValidationCacheIsBoundedAndGenerationScoped(t *testing.T) {
	manager := NewSchemaManager(nil, 0)
	manager.maxValidatedBackends = 2
	first := &pgx.Conn{}
	second := &pgx.Conn{}
	third := &pgx.Conn{}
	manager.markBackendValidated(first, 1)
	manager.markBackendValidated(second, 1)
	manager.markBackendValidated(first, 1)
	manager.markBackendValidated(third, 1)

	require.Len(t, manager.validatedBackends, 2)
	require.Contains(t, manager.validatedBackends, first)
	require.Contains(t, manager.validatedBackends, third)
	manager.prepareBackendGeneration(2, runtimeReadiness{Checked: true})
	require.Empty(t, manager.validatedBackends)
	require.Equal(t, uint64(2), manager.backendGeneration)
}

func TestStaticSessionPoolRejectsPerCheckoutHooks(t *testing.T) {
	for _, configure := range []func(*pgxpool.Config){
		func(cfg *pgxpool.Config) { cfg.BeforeAcquire = func(context.Context, *pgx.Conn) bool { return true } },
		func(cfg *pgxpool.Config) {
			cfg.PrepareConn = func(context.Context, *pgx.Conn) (bool, error) { return true, nil }
		},
		func(cfg *pgxpool.Config) { cfg.AfterRelease = func(*pgx.Conn) bool { return true } },
	} {
		cfg, err := pgxpool.ParseConfig("postgres://example:example@127.0.0.1:1/example?connect_timeout=1")
		require.NoError(t, err)
		configure(cfg)
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		require.NoError(t, err)
		require.ErrorIs(t, validateStaticSessionPool(pool), ErrUnsafePoolHooks)
		pool.Close()
	}
}
