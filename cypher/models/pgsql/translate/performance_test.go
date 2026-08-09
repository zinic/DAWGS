package translate

import (
	"context"
	"testing"

	"github.com/specterops/dawgs/cypher/frontend"
	"github.com/specterops/dawgs/drivers/pg/pgutil"
	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

var pipelineBenchmarkQueries = map[string]string{
	"node_count":            `match (n) return count(n)`,
	"typed_edge_count":      `match ()-[r:EdgeKind1]->() return count(r)`,
	"untyped_edge_count":    `match ()-[r]->() return count(r)`,
	"one_hop":               `match (s)-[:EdgeKind1]->(e) where id(s) = $start_id return id(e)`,
	"typed_variable_path":   `match p=(s)-[:EdgeKind1|EdgeKind2*1..]->(e) where id(s) = $start_id return p`,
	"untyped_variable_path": `match p=(s)-[*1..]->(e) where id(s) = $start_id return p`,
	"shortest_path":         `match p=shortestPath((s)-[:EdgeKind1|EdgeKind2*1..]->(e)) where id(s) = $start_id and id(e) = $end_id return p`,
}

func pipelineBenchmarkMapper() *pgutil.InMemoryKindMapper {
	mapper := pgutil.NewInMemoryKindMapper()
	mapper.Put(graph.StringKind("EdgeKind1"))
	mapper.Put(graph.StringKind("EdgeKind2"))
	return mapper
}

func BenchmarkPipelineParse(b *testing.B) {
	for name, cypher := range pipelineBenchmarkQueries {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_, err := frontend.ParseCypher(frontend.NewContext(), cypher)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPipelineTranslate(b *testing.B) {
	mapper := pipelineBenchmarkMapper()
	params := map[string]any{"start_id": int64(1), "end_id": int64(2)}
	for name, cypher := range pipelineBenchmarkQueries {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				parsed, err := frontend.ParseCypher(frontend.NewContext(), cypher)
				require.NoError(b, err)
				b.StartTimer()
				if _, err := Translate(context.Background(), parsed, mapper, params, 42); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPipelineRender(b *testing.B) {
	mapper := pipelineBenchmarkMapper()
	params := map[string]any{"start_id": int64(1), "end_id": int64(2)}
	for name, cypher := range pipelineBenchmarkQueries {
		parsed, err := frontend.ParseCypher(frontend.NewContext(), cypher)
		require.NoError(b, err)
		translation, err := Translate(context.Background(), parsed, mapper, params, 42)
		require.NoError(b, err)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := Translated(translation); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
