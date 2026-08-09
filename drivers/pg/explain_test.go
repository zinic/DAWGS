package pg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeStructuredExplainLeafSetsAndMetrics(t *testing.T) {
	value := `[{
  "Plan":{"Node Type":"Append","Shared Hit Blocks":7,"Plans":[
    {"Node Type":"Seq Scan","Relation Name":"edge_1_2","Actual Loops":1},
    {"Node Type":"Seq Scan","Relation Name":"edge_1_3","Actual Loops":0},
    {"Node Type":"Seq Scan","Relation Name":"unmanaged","Actual Loops":1}]},
  "Planning Time":1.25,"Execution Time":2.5
}]`
	result, err := DecodeStructuredExplain(value, []LeafReference{{GraphID: 1, KindID: 1}, {GraphID: 1, KindID: 2}, {GraphID: 1, KindID: 3}})
	require.NoError(t, err)
	require.Equal(t, []LeafReference{{GraphID: 1, KindID: 2}, {GraphID: 1, KindID: 3}}, result.Leaves.Planned)
	require.Equal(t, []LeafReference{{GraphID: 1, KindID: 2}}, result.Leaves.Executed)
	require.Equal(t, []LeafReference{{GraphID: 1, KindID: 1}, {GraphID: 1, KindID: 3}}, result.Leaves.Removed)
	require.EqualValues(t, 7, result.Metrics.Buffers.SharedHit)
	require.Equal(t, 1.25, *result.Metrics.PlanningMS)
	require.Equal(t, []string{"Append", "Seq Scan"}, result.Operators)
}

func TestDecodeStructuredExplainRejectsTrailingDocument(t *testing.T) {
	_, err := DecodeStructuredExplain(`[{"Plan":{"Node Type":"Result"}}] {}`, nil)
	require.ErrorContains(t, err, "trailing JSON")
}
