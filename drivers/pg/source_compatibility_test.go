package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs"
	"github.com/specterops/dawgs/drivers/pg"
	"github.com/specterops/dawgs/graph"
	"github.com/specterops/dawgs/util/size"
	"github.com/stretchr/testify/require"
)

// Compile-time assignments freeze the public constructor, package path, and
// graph.Database surface used by downstream V1 clients.
var (
	_ func(size.Size, *pgxpool.Pool) *pg.Driver = pg.NewDriver
	_ graph.Database                            = (*pg.Driver)(nil)
	_ pg.LayoutHealthProvider                   = (*pg.Driver)(nil)
	_ pg.ReadinessProvider                      = (*pg.Driver)(nil)
	_ pg.RuntimeAssertionProvider               = (*pg.Driver)(nil)
)

func TestPostgresPublicCompatibilityContract(t *testing.T) {
	require.Equal(t, "pg", pg.DriverName)
	for _, typed := range []error{
		pg.ErrMigrationRequired, pg.ErrMigrationInProgress, pg.ErrSchemaUninitialized,
		pg.ErrLayoutDrift, pg.ErrPendingAcceptance, pg.ErrSmokePending, pg.ErrWrongRuntimeRole,
		pg.ErrRestoreRebind, pg.ErrActivationNotReady, pg.ErrStartupValidation,
		pg.ErrStartupAssertionConflict, pg.ErrUnsafePoolHooks,
	} {
		require.NotNil(t, typed)
	}

	_, err := dawgs.Open(context.Background(), "missing", dawgs.Config{})
	require.True(t, errors.Is(err, dawgs.ErrDriverMissing))
}
