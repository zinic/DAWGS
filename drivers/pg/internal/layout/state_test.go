package layout

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type rowValues struct {
	values []any
	err    error
}

func (s rowValues) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	for index, value := range s.values {
		switch target := dest[index].(type) {
		case *bool:
			*target = value.(bool)
		case *int16:
			*target = value.(int16)
		case *State:
			*target = value.(State)
		case *uint32:
			*target = value.(uint32)
		case *string:
			*target = value.(string)
		}
	}
	return nil
}

type rowsSequence struct{ rows []pgx.Row }

func (s *rowsSequence) QueryRow(context.Context, string, ...any) pgx.Row {
	row := s.rows[0]
	s.rows = s.rows[1:]
	return row
}

func TestClassifyAcceptedUsesOneStatement(t *testing.T) {
	queryer := &rowsSequence{rows: []pgx.Row{rowValues{values: []any{
		true, true, true, true, true, true, int16(2), StateV2Accepted, true,
		uint32(1), "dawgs", "cluster", uint32(2), "target", "passed", "receipt",
		uint32(1), "dawgs", "cluster", uint32(2), "target", "target", "target",
		false, uint32(3), false,
	}}}}

	facts, err := ClassifyAccepted(context.Background(), queryer)
	require.NoError(t, err)
	require.True(t, facts.Accepted)
	require.Equal(t, 1, facts.GuardStatements)
	require.Empty(t, queryer.rows)
}

func TestClassifyAcceptedRefusesIncoherentResult(t *testing.T) {
	queryer := &rowsSequence{rows: []pgx.Row{rowValues{values: []any{
		true, true, true, true, true, true, int16(2), StateV2Accepted, false,
		uint32(1), "dawgs", "cluster", uint32(2), "target", "passed", "receipt",
		uint32(1), "dawgs", "cluster", uint32(2), "target", "target", "target",
		false, uint32(3), false,
	}}}}

	facts, err := ClassifyAccepted(context.Background(), queryer)
	require.NoError(t, err)
	require.False(t, facts.Accepted)
}

func TestClassifyRecognizesOnlyExactUnmarkedBaseShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []any
		state  State
	}{
		{"empty", []any{false, false, false, false, false}, StateUninitialized},
		{"frozen v1 shape", []any{true, true, true, true, false}, StateV1},
		{"heap lookalikes", []any{true, true, true, false, false}, StateDrifted},
		{"partial objects", []any{true, true, false, false, false}, StateDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts, err := Classify(context.Background(), &rowsSequence{rows: []pgx.Row{rowValues{values: test.values}}})
			require.NoError(t, err)
			require.Equal(t, test.state, facts.State)
		})
	}
}

func TestClassifyRefusesMarkerWithoutExactStructure(t *testing.T) {
	queryer := &rowsSequence{rows: []pgx.Row{
		rowValues{values: []any{true, true, true, true, true}},
		rowValues{values: []any{true}},
		rowValues{values: []any{true, true, true, true, true, false}},
	}}
	facts, err := Classify(context.Background(), queryer)
	require.NoError(t, err)
	require.Equal(t, StateDrifted, facts.State)
	require.NotEmpty(t, facts.Drift)
}

func TestClassifyAcceptsKnownStructurallyCompleteState(t *testing.T) {
	for _, state := range []State{
		StateV2Accepted,
		StatePendingHandoff,
		StateMigrating,
		StatePending,
		StatePendingSmoke,
		StateRolledBack,
	} {
		t.Run(string(state), func(t *testing.T) {
			queryer := &rowsSequence{rows: []pgx.Row{
				rowValues{values: []any{true, true, true, true, true}},
				rowValues{values: []any{true}},
				rowValues{values: []any{true, true, true, true, true, true}},
				rowValues{values: []any{int16(2), state, true}},
			}}
			facts, err := Classify(context.Background(), queryer)
			require.NoError(t, err)
			require.Equal(t, state, facts.State)
		})
	}
}

func TestClassifyRefusesUnknownMarkerState(t *testing.T) {
	queryer := &rowsSequence{rows: []pgx.Row{
		rowValues{values: []any{true, true, true, true, true}},
		rowValues{values: []any{true}},
		rowValues{values: []any{true, true, true, true, true, true}},
		rowValues{values: []any{int16(2), State("future"), true}},
	}}
	facts, err := Classify(context.Background(), queryer)
	require.NoError(t, err)
	require.Equal(t, StateDrifted, facts.State)
}

func TestClassifyRecognizesFencedRolledBackV1WithoutRuntimeRegistries(t *testing.T) {
	queryer := &rowsSequence{rows: []pgx.Row{
		rowValues{values: []any{true, true, true, true, true}},
		rowValues{values: []any{false}},
		rowValues{values: []any{int16(2), StateRolledBack, true}},
	}}
	facts, err := Classify(context.Background(), queryer)
	require.NoError(t, err)
	require.Equal(t, StateRolledBack, facts.State)
}
