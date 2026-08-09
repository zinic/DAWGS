package pg

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

type collectingObservationSink struct {
	lock   sync.Mutex
	values []Observation
}

func (s *collectingObservationSink) Observe(value Observation) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.values = append(s.values, value)
}

func TestObservationSinkIsOptionalConcurrentAndBounded(t *testing.T) {
	driver := &Driver{SchemaManager: &SchemaManager{}}
	sink := &collectingObservationSink{}
	driver.SetObservationSink(sink)
	value := Observation{Event: ObservationReadiness, Result: "refused", LayoutState: "v1",
		LayoutErrorCode: LayoutErrorMigrationRequired, TopologyFingerprint: strings.Repeat("a", 64),
		GraphParentCount: 2, KindCount: 3, LeafCount: 6, ServerMajor: 18, Duration: time.Millisecond}

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			driver.SchemaManager.observations.emit(value)
		}()
	}
	wait.Wait()
	require.Len(t, sink.values, 8)
	content, err := json.Marshal(value)
	require.NoError(t, err)
	for _, forbidden := range []string{"database_name", "role", "graph_id", "kind_id", "relation", "query", "dsn", "property", "migration_id"} {
		require.NotContains(t, string(content), forbidden)
	}
}

func TestBoundedErrorResultUsesOnlyTypedCodeOrSQLState(t *testing.T) {
	result, code := boundedErrorResult(&LayoutError{Code: LayoutErrorDrift, State: "secret-state", Err: ErrLayoutDrift})
	require.Equal(t, "refused", result)
	require.Equal(t, LayoutErrorDrift, code)

	result, code = boundedErrorResult(&pgconn.PgError{Code: "23505", Message: "contains sensitive value"})
	require.Equal(t, "sqlstate_23505", result)
	require.Empty(t, code)

	result, code = boundedErrorResult(errors.New("contains sensitive value"))
	require.Equal(t, "error", result)
	require.Empty(t, code)
}
