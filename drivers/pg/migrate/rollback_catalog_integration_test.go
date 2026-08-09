//go:build rollback_integration

package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/catalog"
	"github.com/stretchr/testify/require"
)

func frozenV1StructuralCatalog(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	snapshot, err := catalog.ReadSnapshot(ctx, pool)
	if err != nil {
		return nil, err
	}
	relationNames := map[uint32]string{}
	excluded := map[uint32]bool{}
	for _, relation := range snapshot.Relations {
		relationNames[relation.OID] = relation.Schema + "." + relation.Name
		if strings.HasPrefix(relation.Name, "dawgs_") {
			excluded[relation.OID] = true
		}
	}
	var facts []string
	for _, relation := range snapshot.Relations {
		if excluded[relation.OID] {
			continue
		}
		parent := ""
		if relation.ParentOID != nil {
			parent = relationNames[*relation.ParentOID]
		}
		if err := appendCatalogFact(&facts, "relation", struct {
			Name, Kind, Persistence, ReplicaIdentity, Tablespace, Comment, Parent string
			RowSecurity, ForceRowSecurity                                         bool
			StorageParameters                                                     []string
			Partition                                                             *catalog.Partition
		}{relationNames[relation.OID], relation.Kind, relation.Persistence, relation.ReplicaIdentity, relation.Tablespace,
			relation.Comment, parent, relation.RowSecurity, relation.ForceRowSecurity, relation.StorageParameters, relation.Partition}); err != nil {
			return nil, err
		}
	}
	for _, index := range snapshot.Indexes {
		if excluded[index.RelationOID] {
			continue
		}
		if err := appendCatalogFact(&facts, "index", struct {
			Relation, Name, Definition    string
			Unique, Primary, Valid, Ready bool
		}{relationNames[index.RelationOID], index.Name, index.Definition, index.Unique, index.Primary, index.Valid, index.Ready}); err != nil {
			return nil, err
		}
	}
	for _, constraint := range snapshot.Constraints {
		if excluded[constraint.RelationOID] {
			continue
		}
		if err := appendCatalogFact(&facts, "constraint", struct {
			Relation, Name, Type, Definition string
			Validated, Deferrable, Deferred  bool
		}{relationNames[constraint.RelationOID], constraint.Name, constraint.Type, constraint.Definition,
			constraint.Validated, constraint.Deferrable, constraint.Deferred}); err != nil {
			return nil, err
		}
	}
	for _, trigger := range snapshot.Triggers {
		if excluded[trigger.RelationOID] || strings.HasPrefix(trigger.Name, "dawgs_") {
			continue
		}
		if err := appendCatalogFact(&facts, "trigger", struct {
			Relation, Name, Enabled, FunctionSchema, FunctionName, Definition string
			Internal                                                          bool
		}{relationNames[trigger.RelationOID], trigger.Name, trigger.Enabled, trigger.FunctionSchema,
			trigger.FunctionName, trigger.Definition, trigger.Internal}); err != nil {
			return nil, err
		}
	}
	for _, routine := range snapshot.Routines {
		if strings.HasPrefix(routine.Name, "dawgs_") {
			continue
		}
		routine.OID, routine.Owner = 0, ""
		if err := appendCatalogFact(&facts, "routine", routine); err != nil {
			return nil, err
		}
	}
	for _, sequence := range snapshot.Sequences {
		if strings.HasPrefix(sequence.Name, "dawgs_") {
			continue
		}
		sequence.OID, sequence.Owner = 0, ""
		if err := appendCatalogFact(&facts, "sequence", sequence); err != nil {
			return nil, err
		}
	}
	for _, dependency := range snapshot.Dependencies {
		if excluded[dependency.ObjectOID] || excluded[dependency.ReferencedObjectOID] ||
			strings.HasPrefix(dependency.ObjectName, "dawgs_") || strings.HasPrefix(dependency.ReferencedName, "dawgs_") {
			continue
		}
		// PostgreSQL synthesizes new internal RI trigger identities when an
		// existing retained table is attached as a partition. They are neither
		// user-addressable nor stable across OIDs; the FK constraint itself and
		// its dependencies remain part of this comparison.
		if dependency.ObjectType == "trigger" && strings.Contains(dependency.ObjectIdentity, "RI_ConstraintTrigger_") {
			continue
		}
		dependency.ClassOID, dependency.ObjectOID, dependency.ReferencedClassOID, dependency.ReferencedObjectOID = 0, 0, 0, 0
		if err := appendCatalogFact(&facts, "dependency", dependency); err != nil {
			return nil, err
		}
	}
	for _, rule := range snapshot.Rules {
		if !strings.HasPrefix(rule.Relation, "dawgs_") {
			if err := appendCatalogFact(&facts, "rule", rule); err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(facts)
	return facts, nil
}

func appendCatalogFact(destination *[]string, kind string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	*destination = append(*destination, kind+":"+string(content))
	return nil
}

func assertFrozenV1HandoffCatalog(t *testing.T, ctx context.Context, pool *pgxpool.Pool, baseline []string, preparationRole string) {
	t.Helper()
	after, err := frozenV1StructuralCatalog(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, baseline, after, "rollback must restore the complete OID-independent V1 structural catalog")

	quotedRole := quoteLiteral(preparationRole)
	var missingPrivileges int
	require.NoError(t, pool.QueryRow(ctx, fmt.Sprintf(`select count(*) from (
select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and c.relkind in ('r','p') and pg_catalog.left(c.relname,6)<>'dawgs_'
and not pg_catalog.has_table_privilege(%s,c.oid,'SELECT')
union all
select c.oid from pg_catalog.pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
where n.nspname='public' and pg_catalog.left(c.relname,6)<>'dawgs_'
and not case when c.relkind='S' then pg_catalog.has_sequence_privilege(%s,c.oid,'USAGE,SELECT,UPDATE') else true end
union all
select p.oid from pg_catalog.pg_proc p join pg_catalog.pg_namespace n on n.oid=p.pronamespace
where n.nspname='public' and pg_catalog.left(p.proname,6)<>'dawgs_'
and not pg_catalog.has_function_privilege(%s,p.oid,'EXECUTE')) missing`, quotedRole, quotedRole, quotedRole)).Scan(&missingPrivileges))
	require.Zero(t, missingPrivileges)

	for _, item := range []struct{ sequence, relation string }{{"graph_id_seq", "graph"}, {"kind_id_seq", "kind"}, {"node_id_seq", "node"}, {"edge_id_seq", "edge"}} {
		var last, maximum int64
		require.NoError(t, pool.QueryRow(ctx, fmt.Sprintf(`select last_value,(select coalesce(max(id),0) from public.%s) from public.%s`, item.relation, item.sequence)).Scan(&last, &maximum))
		require.GreaterOrEqual(t, last, maximum, item.sequence)
	}
}
