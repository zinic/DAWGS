package translate

import (
	"context"
	"testing"

	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/cypher/models/pgsql"
	"github.com/specterops/dawgs/drivers/pg/pgutil"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

func TestTranslationScopesPersistentRelationsAndCarriesMetadata(t *testing.T) {
	mapper := pgutil.NewInMemoryKindMapper()
	mapper.Put(graph.StringKind("NodeKind1"))
	mapper.Put(graph.StringKind("NodeKind2"))
	mapper.Put(graph.StringKind("EdgeKind1"))
	query, err := frontend.ParseCypher(frontend.NewContext(), `CREATE (a:NodeKind1)-[:EdgeKind1]->(b:NodeKind2) RETURN a`)
	require.NoError(t, err)
	translation, err := Translate(context.Background(), query, mapper, nil, 42)
	require.NoError(t, err)
	formatted, err := Translated(translation)
	require.NoError(t, err)
	require.Contains(t, formatted, "public.node_42")
	require.Contains(t, formatted, "insert into public.edge_42")
	require.NotContains(t, formatted, "from node ")
	require.Equal(t, int32(42), translation.GraphID)
	require.True(t, translation.HasMutations)
	require.Equal(t, []int16{3}, translation.RequiredEdgeKindIDs)
}

func TestFinalizePreservesSQLLookingParameterStrings(t *testing.T) {
	translator := &Translator{
		graphID: 41,
		query:   &Query{},
		translation: Result{Parameters: map[string]any{
			"from":   "from node where value = 'join edge '",
			"update": "update node set value = 'delete from edge '",
		}},
		requiredEdgeKindIDs: map[int16]struct{}{},
	}
	translator.translation.Statement = pgsql.Query{}
	result := translator.finalize()
	require.Equal(t, "from node where value = 'join edge '", result.Parameters["from"])
	require.Equal(t, "update node set value = 'delete from edge '", result.Parameters["update"])
}

func TestTypedPathHydrationCarriesExactKnownKindUnion(t *testing.T) {
	mapper := pgutil.NewInMemoryKindMapper()
	mapper.Put(graph.StringKind("NodeKind1"))
	mapper.Put(graph.StringKind("NodeKind2"))
	mapper.Put(graph.StringKind("EdgeKind1"))
	mapper.Put(graph.StringKind("EdgeKind2"))
	query, err := frontend.ParseCypher(frontend.NewContext(), `match p = (:NodeKind1)-[:EdgeKind1|EdgeKind2*1..]->(:NodeKind2) return p`)
	require.NoError(t, err)
	translation, err := Translate(context.Background(), query, mapper, nil, 42)
	require.NoError(t, err)
	formatted, err := Translated(translation)
	require.NoError(t, err)
	require.Contains(t, formatted, "join public.edge_42 _edge on _edge.id = _path.id and _edge.kind_id = any(")
	require.Contains(t, formatted, "array [3, 4]::int2[]")
}

func TestUntypedPathHydrationDoesNotInventKindFilter(t *testing.T) {
	mapper := pgutil.NewInMemoryKindMapper()
	mapper.Put(graph.StringKind("NodeKind1"))
	mapper.Put(graph.StringKind("NodeKind2"))
	query, err := frontend.ParseCypher(frontend.NewContext(), `match p = (:NodeKind1)-[*1..]->(:NodeKind2) return p`)
	require.NoError(t, err)
	translation, err := Translate(context.Background(), query, mapper, nil, 42)
	require.NoError(t, err)
	formatted, err := Translated(translation)
	require.NoError(t, err)
	require.Contains(t, formatted, "join public.edge_42 _edge on _edge.id = _path.id)")
	require.NotContains(t, formatted, "join public.edge_42 _edge on _edge.id = _path.id and")
}
