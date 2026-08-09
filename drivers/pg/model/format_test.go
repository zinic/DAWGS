package model

import (
	"strings"
	"testing"

	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

func TestManagedNamesAreBoundedAndDoNotEmbedUserFields(t *testing.T) {
	field := strings.Repeat("quoted'\"unicode-λ", 20)
	indexName := IndexName("node_1", graph.Index{Field: field})
	constraintName := ConstraintName("edge_1", graph.Constraint{Field: field})
	require.LessOrEqual(t, len(indexName), 63)
	require.LessOrEqual(t, len(constraintName), 63)
	require.NotContains(t, indexName, "quoted")
	require.NotEqual(t, indexName, IndexName("node_2", graph.Index{Field: field}))
}

func TestEdgeKindPartitionTableName(t *testing.T) {
	require.Equal(t, "edge_1_2", EdgeKindPartitionTableName(1, 2))
	require.Equal(t, "edge_2147483647_-32768", EdgeKindPartitionTableName(2147483647, -32768))
}
