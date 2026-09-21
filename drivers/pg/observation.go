package pg

import (
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	ObservationReadiness         = "layout_readiness"
	ObservationLeafProvisioning  = "leaf_provisioning"
	ObservationRuntimeGuard      = "runtime_guard"
	ObservationStartupValidation = "startup_validation"
	ObservationBackendValidation = "backend_validation"
)

// Observation is deliberately bounded-cardinality. It contains no database,
// role, graph, kind, relation, query, DSN, property, or migration identifiers.
type Observation struct {
	Event               string          `json:"event"`
	Result              string          `json:"result"`
	LayoutState         string          `json:"layout_state,omitempty"`
	LayoutErrorCode     LayoutErrorCode `json:"layout_error_code,omitempty"`
	TopologyFingerprint string          `json:"topology_fingerprint,omitempty"`
	GraphParentCount    int64           `json:"graph_parent_count,omitempty"`
	KindCount           int64           `json:"kind_count,omitempty"`
	LeafCount           int64           `json:"leaf_count,omitempty"`
	ServerMajor         int             `json:"server_major,omitempty"`
	OperationCount      int             `json:"operation_count,omitempty"`
	StatementCount      int             `json:"statement_count,omitempty"`
	Duration            time.Duration   `json:"duration_ns"`
}

type ObservationSink interface {
	Observe(Observation)
}

type observationState struct {
	lock sync.RWMutex
	sink ObservationSink
}

func (s *observationState) set(sink ObservationSink) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.sink = sink
}

func (s *observationState) emit(value Observation) {
	s.lock.RLock()
	sink := s.sink
	s.lock.RUnlock()
	if sink != nil {
		sink.Observe(value)
	}
}

func boundedErrorResult(err error) (string, LayoutErrorCode) {
	if err == nil {
		return "ok", ""
	}
	var layoutError *LayoutError
	if errors.As(err, &layoutError) {
		return "refused", layoutError.Code
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return "sqlstate_" + postgresError.Code, ""
	}
	return "error", ""
}
