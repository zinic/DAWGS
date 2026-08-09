package pg

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDynamicMutationStatementsAreSchemaQualifiedAndIdentifierSafe(t *testing.T) {
	require.Equal(t, `"public"."node_7"`, qualifiedManagedRelation("node_7"))
	require.Equal(t, `"public"."edge_7"`, qualifiedManagedRelation("public.edge_7"))
	require.Equal(t, `"public"."edge_7; drop table public.node"`, qualifiedManagedRelation("edge_7; drop table public.node"))

	nodeSQL := fmt.Sprintf(createNodeStatement, qualifiedManagedRelation("node_7"))
	edgeSQL := fmt.Sprintf(createEdgeStatement, qualifiedManagedRelation("edge_7"))
	require.Contains(t, nodeSQL, `insert into "public"."node_7"`)
	require.Contains(t, nodeSQL, `::public.nodeComposite`)
	require.Contains(t, edgeSQL, `insert into "public"."edge_7"`)
	require.Contains(t, edgeSQL, `::public.edgeComposite`)
}
