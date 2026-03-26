package cardinality

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHyperLogLog64_Add(t *testing.T) {
	sketch := NewHyperLogLog64()

	sketch.Add(1)
	require.Equal(t, uint64(1), sketch.Cardinality())

	sketch.Add(1)
	require.Equal(t, uint64(1), sketch.Cardinality())

	sketch.Add(2)
	require.Equal(t, uint64(2), sketch.Cardinality())

	sketch.Add(2)
	require.Equal(t, uint64(2), sketch.Cardinality())
}
