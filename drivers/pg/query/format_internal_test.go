package query

import (
	"testing"

	"github.com/specterops/dawgs/graph"
	"github.com/stretchr/testify/require"
)

func TestManagedDDLQuotesIdentifiersAndPropertyLiterals(t *testing.T) {
	statement := formatCreatePropertyConstraint(`dawgs_uq_safe`, `edge_7`, `quoted'property`, graph.BTreeIndex)
	require.Equal(t, `create unique index "public"."dawgs_uq_safe" on "public"."edge_7" using btree ((properties ->> 'quoted''property'));`, statement)
	require.NotContains(t, statement, `quoted'property`)

	partition := formatCreatePartitionTable(`node_7`, `node`, 7)
	require.Equal(t, `create table "public"."node_7" partition of "public"."node" for values in (7)`, partition)
}
