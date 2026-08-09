//go:build pg_physical

package catalog

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/schema"
	"github.com/stretchr/testify/require"
)

func TestPGPhysicalCatalogSnapshotDecodesManagedFacts(t *testing.T) {
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
create or replace view public.dawgs_catalog_test_view as select id,name from public.kind;
drop rule if exists dawgs_catalog_test_rule on public.kind;
create rule dawgs_catalog_test_rule as on update to public.kind do also notify dawgs_catalog_test;
drop publication if exists dawgs_catalog_test;
create publication dawgs_catalog_test;
alter default privileges in schema public grant select on tables to public;`).ReadAll()
	require.NoError(t, err)
	var role, database string
	require.NoError(t, connection.QueryRow(ctx, `select session_user::text,pg_catalog.current_database()`).Scan(&role, &database))
	_, err = connection.Exec(ctx, fmt.Sprintf(`alter role %s in database %s set statement_timeout='30s'`,
		pgx.Identifier{role}.Sanitize(), pgx.Identifier{database}.Sanitize()))
	require.NoError(t, err)

	snapshot, err := ReadSnapshot(ctx, connection)
	require.NoError(t, err)
	relations := map[string]Relation{}
	for _, relation := range snapshot.Relations {
		relations[relation.Name] = relation
	}
	require.Equal(t, "r", relations["graph"].Kind)
	require.Equal(t, "p", relations["node"].Kind)
	require.Equal(t, "LIST (graph_id)", relations["node"].Partition.KeyDefinition)
	require.Equal(t, "v", relations["dawgs_catalog_test_view"].Kind)
	require.NotEmpty(t, relations["graph"].ACL)
	require.NotEmpty(t, snapshot.Indexes)
	require.NotEmpty(t, snapshot.Constraints)
	require.NotEmpty(t, snapshot.Triggers)
	require.NotEmpty(t, snapshot.Routines)
	require.NotEmpty(t, snapshot.Sequences)
	require.NotEmpty(t, snapshot.Dependencies)
	require.NotEmpty(t, snapshot.Rules)
	require.Condition(t, func() bool {
		for _, publication := range snapshot.Publications {
			if publication.Name == "dawgs_catalog_test" && publication.Owner == role {
				return true
			}
		}
		return false
	})
	require.Condition(t, func() bool {
		for _, setting := range snapshot.RoleSettings {
			if setting.Database == database && setting.Role == role {
				return true
			}
		}
		return false
	})
	require.Condition(t, func() bool {
		for _, defaults := range snapshot.DefaultACLs {
			for _, acl := range defaults.ACL {
				if defaults.Owner == role && defaults.Schema == "public" && acl.Grantee == "PUBLIC" && acl.Privilege == "SELECT" {
					return true
				}
			}
		}
		return false
	})
}
