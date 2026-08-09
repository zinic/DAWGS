//go:build transition_containment

package pg

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/util/size"
	"github.com/stretchr/testify/require"
)

func TestPGTransitionContainmentRefusesAndDoesNotBootstrap(t *testing.T) {
	connectionString := os.Getenv("CONNECTION_STRING")
	if connectionString == "" {
		t.Skip("CONNECTION_STRING is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connectionString)
	require.NoError(t, err)
	defer pool.Close()
	driver := NewDriver(size.Gibibyte, pool)

	_, err = driver.Readiness(ctx)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrSchemaUninitialized), err)
	var managedObjects int
	require.NoError(t, pool.QueryRow(ctx, `select count(*) from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and (pg_catalog.left(c.relname,6)='dawgs_' or c.relname in ('graph','kind','node','edge'))`).Scan(&managedObjects))
	require.Zero(t, managedObjects)
}
