package pg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/graph"
)

type lifecyclePhase uint8

const (
	lifecycleUnasserted lifecyclePhase = iota
	lifecycleAsserting
	lifecycleReady
	lifecycleClosing
	lifecycleClosed
)

type startupFlight struct {
	key        string
	generation uint64
	done       chan struct{}
	err        error
}

// lifecycleCoordinator owns service admission. Assertions are exclusive and
// operations hold a shared lease for their complete lifetime. Waiting callers
// never mutate the result of the assertion leader.
type lifecycleCoordinator struct {
	mu         sync.Mutex
	phase      lifecyclePhase
	generation uint64
	active     int
	drained    chan struct{}
	flight     *startupFlight
}

type assertionTicket struct {
	coordinator *lifecycleCoordinator
	flight      *startupFlight
	drained     <-chan struct{}
}

func newLifecycleCoordinator() lifecycleCoordinator {
	drained := make(chan struct{})
	close(drained)
	return lifecycleCoordinator{phase: lifecycleUnasserted, drained: drained}
}

func startupRequestKey(schema *graph.Schema) (string, error) {
	if schema == nil {
		return "runtime", nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "schema:" + hex.EncodeToString(digest[:]), nil
}

func (s *lifecycleCoordinator) beginAssertion(ctx context.Context, key string) (*assertionTicket, error) {
	s.mu.Lock()
	if s.phase == lifecycleClosed || s.phase == lifecycleClosing {
		s.mu.Unlock()
		return nil, &LayoutError{Code: LayoutErrorStartupValidation, State: "closed", Err: ErrStartupValidation}
	}
	if s.flight != nil {
		flight := s.flight
		if flight.key != key {
			s.mu.Unlock()
			return nil, &LayoutError{Code: LayoutErrorStartupAssertionConflict, State: "asserting", Err: ErrStartupAssertionConflict}
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return nil, flight.err
		}
	}

	s.generation++
	flight := &startupFlight{key: key, generation: s.generation, done: make(chan struct{})}
	s.flight = flight
	s.phase = lifecycleAsserting
	drained := s.drained
	s.mu.Unlock()
	return &assertionTicket{coordinator: s, flight: flight, drained: drained}, nil
}

func (s *assertionTicket) waitForDrain(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.drained:
		return nil
	}
}

func (s *assertionTicket) generation() uint64 {
	return s.flight.generation
}

func (s *assertionTicket) finish(err error) {
	s.coordinator.mu.Lock()
	defer s.coordinator.mu.Unlock()
	if s.coordinator.flight != s.flight {
		return
	}
	s.flight.err = err
	if err == nil {
		s.coordinator.phase = lifecycleReady
	} else {
		s.coordinator.phase = lifecycleUnasserted
	}
	s.coordinator.flight = nil
	close(s.flight.done)
}

func (s *lifecycleCoordinator) beginOperation() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != lifecycleReady {
		state := "unasserted"
		if s.phase == lifecycleClosed || s.phase == lifecycleClosing {
			state = "closed"
		} else if s.phase == lifecycleAsserting {
			state = "asserting"
		}
		return 0, &LayoutError{Code: LayoutErrorStartupValidation, State: state, Err: ErrStartupValidation}
	}
	if s.active == 0 {
		s.drained = make(chan struct{})
	}
	s.active++
	return s.generation, nil
}

func (s *lifecycleCoordinator) endOperation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == 0 {
		return
	}
	s.active--
	if s.active == 0 {
		close(s.drained)
	}
}

func (s *lifecycleCoordinator) isReadyGeneration(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase == lifecycleReady && s.generation == generation
}

func (s *lifecycleCoordinator) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// invalidateReady fails closed after an operation may have committed a
// topology change without returning a definitive outcome. An assertion that
// is already waiting for the active operation to drain remains authoritative
// and will publish its own generation when it completes.
func (s *lifecycleCoordinator) invalidateReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != lifecycleReady {
		return false
	}
	s.generation++
	s.phase = lifecycleUnasserted
	return true
}

func (s *lifecycleCoordinator) close(ctx context.Context) error {
	for {
		s.mu.Lock()
		if s.phase == lifecycleClosed {
			s.mu.Unlock()
			return nil
		}
		if s.flight != nil {
			done := s.flight.done
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				continue
			}
		}
		s.phase = lifecycleClosing
		drained := s.drained
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.phase == lifecycleClosing {
				s.phase = lifecycleUnasserted
			}
			s.mu.Unlock()
			return ctx.Err()
		case <-drained:
			s.mu.Lock()
			s.phase = lifecycleClosed
			s.mu.Unlock()
			return nil
		}
	}
}

type operationalLease struct {
	manager *SchemaManager
	conn    *pgxpool.Conn
	once    sync.Once
}

func (s *operationalLease) Conn() *pgxpool.Conn {
	return s.conn
}

func (s *operationalLease) Release() {
	s.once.Do(func() {
		if s.conn != nil {
			s.conn.Release()
		}
		s.manager.lifecycle.endOperation()
	})
}
