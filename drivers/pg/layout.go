package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/specterops/dawgs/drivers/pg/internal/layout"
)

var (
	ErrMigrationRequired        = errors.New("PostgreSQL schema migration required")
	ErrMigrationInProgress      = errors.New("PostgreSQL schema migration in progress")
	ErrSchemaUninitialized      = errors.New("PostgreSQL schema is uninitialized")
	ErrLayoutDrift              = errors.New("PostgreSQL schema layout drift")
	ErrPendingAcceptance        = errors.New("PostgreSQL schema pending acceptance")
	ErrSmokePending             = errors.New("PostgreSQL target smoke is not current and passed")
	ErrWrongRuntimeRole         = errors.New("PostgreSQL session is not the recorded runtime target")
	ErrRestoreRebind            = errors.New("PostgreSQL database identity changed; restore rebind required")
	ErrActivationNotReady       = errors.New("PostgreSQL V2 activation gate is not ready")
	ErrStartupValidation        = errors.New("PostgreSQL startup validation has not completed")
	ErrStartupAssertionConflict = errors.New("a different PostgreSQL startup assertion is already in progress")
	ErrUnsafePoolHooks          = errors.New("PostgreSQL pool checkout hooks can change session identity")
)

type LayoutErrorCode string

const (
	LayoutErrorMigrationRequired        LayoutErrorCode = "migration_required"
	LayoutErrorMigrationInProgress      LayoutErrorCode = "migration_in_progress"
	LayoutErrorSchemaUninitialized      LayoutErrorCode = "schema_uninitialized"
	LayoutErrorDrift                    LayoutErrorCode = "layout_drift"
	LayoutErrorPendingAcceptance        LayoutErrorCode = "pending_acceptance"
	LayoutErrorSmokePending             LayoutErrorCode = "smoke_pending"
	LayoutErrorWrongRuntimeRole         LayoutErrorCode = "wrong_runtime_role"
	LayoutErrorRestoreRebind            LayoutErrorCode = "restore_rebind_required"
	LayoutErrorActivationNotReady       LayoutErrorCode = "activation_not_ready"
	LayoutErrorStartupValidation        LayoutErrorCode = "startup_validation_required"
	LayoutErrorStartupAssertionConflict LayoutErrorCode = "startup_assertion_conflict"
	LayoutErrorUnsafePoolHooks          LayoutErrorCode = "unsafe_pool_session_hooks"
)

type LayoutError struct {
	Code  LayoutErrorCode
	State string
	Err   error
}

func (s *LayoutError) Error() string {
	return fmt.Sprintf("%s (layout state %s); run dawgs-pg-migrate status for recovery guidance", s.Err, s.State)
}
func (s *LayoutError) Unwrap() error { return s.Err }

type LayoutHealth struct {
	LayoutVersion       int16    `json:"layout_version"`
	State               string   `json:"state"`
	TopologyFingerprint string   `json:"topology_fingerprint,omitempty"`
	GraphParentCount    int64    `json:"graph_parent_count"`
	KindCount           int64    `json:"kind_count"`
	LeafCount           int64    `json:"leaf_count"`
	ServerVersion       string   `json:"server_version,omitempty"`
	ServerVersionNum    int      `json:"server_version_num,omitempty"`
	DatabaseOID         uint32   `json:"database_oid,omitempty"`
	DatabaseName        string   `json:"database_name,omitempty"`
	SystemIdentifier    string   `json:"system_identifier,omitempty"`
	IdentityEpoch       int64    `json:"identity_epoch,omitempty"`
	OwnerRoleOID        uint32   `json:"owner_role_oid,omitempty"`
	OwnerRoleName       string   `json:"owner_role_name,omitempty"`
	TargetRoleOID       uint32   `json:"target_role_oid,omitempty"`
	TargetRoleName      string   `json:"target_role_name,omitempty"`
	SmokeStatus         string   `json:"smoke_status,omitempty"`
	SmokeReceiptDigest  string   `json:"smoke_receipt_digest,omitempty"`
	Drift               []string `json:"drift,omitempty"`
}

type LayoutHealthProvider interface {
	LayoutHealth(context.Context) (LayoutHealth, error)
}

type ReadinessProvider interface {
	Readiness(context.Context) (LayoutHealth, error)
}

// RuntimeAssertionProvider performs the once-per-service-start assertion for
// read-only consumers that must not reconcile schema.
type RuntimeAssertionProvider interface {
	AssertRuntime(context.Context) error
}

type runtimeReadiness struct {
	Checked                  bool
	RecordedDatabaseOID      uint32
	RecordedDatabaseName     string
	RecordedSystemIdentifier string
	RecordedTargetRoleOID    uint32
	RecordedTargetRoleName   string
	SmokeStatus              string
	SmokeReceiptDigest       string
	CurrentDatabaseOID       uint32
	CurrentDatabaseName      string
	CurrentSystemIdentifier  string
	CurrentRoleOID           uint32
	CurrentRoleName          string
	SessionUser              string
	CurrentUser              string
	CurrentRoleSuperuser     bool
	CurrentDatabaseOwnerOID  uint32
	CurrentRoleHasMembership bool
}

type operationalFacts struct {
	layout.Facts
	Readiness runtimeReadiness
}

func classifyPool(ctx context.Context, pool *pgxpool.Pool) (layout.Facts, error) {
	return layout.Classify(ctx, pool)
}

func classifyConnection(ctx context.Context, conn *pgxpool.Conn) (operationalFacts, error) {
	return classifyQuerier(ctx, conn)
}

func classifyQuerier(ctx context.Context, queryer layout.Querier) (operationalFacts, error) {
	// Establish the layout shape before issuing the accepted-state query. The
	// accepted query necessarily names V2 relations; using its expected
	// undefined-relation error as flow control would abort a caller-owned
	// PostgreSQL transaction before the fallback classifier could run.
	facts, err := layout.Classify(ctx, queryer)
	if err != nil || facts.State != layout.StateV2Accepted {
		return operationalFacts{Facts: facts}, err
	}
	accepted, err := layout.ClassifyAccepted(ctx, queryer)
	if err != nil {
		return operationalFacts{Facts: facts}, err
	}
	if !accepted.Accepted {
		return operationalFacts{Facts: facts}, &LayoutError{Code: LayoutErrorDrift, State: string(facts.State), Err: ErrLayoutDrift}
	}
	accepted.Facts.GuardStatements += facts.GuardStatements
	return operationalFacts{Facts: accepted.Facts, Readiness: readinessFromIdentity(accepted.Identity)}, nil
}

func readinessFromIdentity(identity layout.RuntimeIdentity) runtimeReadiness {
	return runtimeReadiness{
		Checked:                  true,
		RecordedDatabaseOID:      identity.RecordedDatabaseOID,
		RecordedDatabaseName:     identity.RecordedDatabaseName,
		RecordedSystemIdentifier: identity.RecordedSystemIdentifier,
		RecordedTargetRoleOID:    identity.RecordedTargetRoleOID,
		RecordedTargetRoleName:   identity.RecordedTargetRoleName,
		SmokeStatus:              identity.SmokeStatus,
		SmokeReceiptDigest:       identity.SmokeReceiptDigest,
		CurrentDatabaseOID:       identity.CurrentDatabaseOID,
		CurrentDatabaseName:      identity.CurrentDatabaseName,
		CurrentSystemIdentifier:  identity.CurrentSystemIdentifier,
		CurrentRoleOID:           identity.CurrentRoleOID,
		CurrentRoleName:          identity.CurrentRoleName,
		SessionUser:              identity.SessionUser,
		CurrentUser:              identity.CurrentUser,
		CurrentRoleSuperuser:     identity.CurrentRoleSuperuser,
		CurrentDatabaseOwnerOID:  identity.CurrentDatabaseOwnerOID,
		CurrentRoleHasMembership: identity.CurrentRoleHasMembership,
	}
}

// validateBackendIdentity checks only connection/session identity. The
// exhaustive schema and topology contract is asserted separately at startup.
func validateBackendIdentity(ctx context.Context, conn *pgxpool.Conn, expected runtimeReadiness) error {
	actual := expected
	actual.Checked = true
	if err := conn.QueryRow(ctx, `/* dawgs:backend/identity */ select
d.oid,d.datname,(pg_catalog.pg_control_system()).system_identifier::text,
r.oid,r.rolname,session_user::text,current_user::text,r.rolsuper,d.datdba,
exists(select 1 from pg_catalog.pg_auth_members m where m.member=r.oid)
from pg_catalog.pg_database d join pg_catalog.pg_roles r on r.rolname=session_user
where d.datname=pg_catalog.current_database()`).Scan(
		&actual.CurrentDatabaseOID, &actual.CurrentDatabaseName, &actual.CurrentSystemIdentifier,
		&actual.CurrentRoleOID, &actual.CurrentRoleName, &actual.SessionUser, &actual.CurrentUser,
		&actual.CurrentRoleSuperuser, &actual.CurrentDatabaseOwnerOID, &actual.CurrentRoleHasMembership); err != nil {
		return err
	}
	return requireOperational(operationalFacts{Facts: layout.Facts{State: layout.StateV2Accepted}, Readiness: actual}, false)
}

func requireOperational(facts operationalFacts, allowUninitialized bool) error {
	switch facts.State {
	case layout.StateV2Accepted:
		readiness := facts.Readiness
		if !readiness.Checked || readiness.RecordedDatabaseOID == 0 || readiness.RecordedDatabaseOID != readiness.CurrentDatabaseOID ||
			readiness.RecordedDatabaseName != readiness.CurrentDatabaseName || readiness.RecordedSystemIdentifier == "" ||
			readiness.RecordedSystemIdentifier != readiness.CurrentSystemIdentifier {
			return &LayoutError{Code: LayoutErrorRestoreRebind, State: string(facts.State), Err: ErrRestoreRebind}
		}
		if (readiness.SmokeStatus != "passed" && readiness.SmokeStatus != "fallback") || readiness.SmokeReceiptDigest == "" {
			return &LayoutError{Code: LayoutErrorSmokePending, State: string(facts.State), Err: ErrSmokePending}
		}
		if readiness.RecordedTargetRoleOID == 0 || readiness.RecordedTargetRoleOID != readiness.CurrentRoleOID ||
			readiness.RecordedTargetRoleName != readiness.CurrentRoleName || readiness.SessionUser != readiness.CurrentUser ||
			readiness.SessionUser != readiness.CurrentRoleName || readiness.CurrentRoleSuperuser ||
			readiness.CurrentRoleOID == readiness.CurrentDatabaseOwnerOID || readiness.CurrentRoleHasMembership {
			return &LayoutError{Code: LayoutErrorWrongRuntimeRole, State: string(facts.State), Err: ErrWrongRuntimeRole}
		}
		if !activationReady() {
			return &LayoutError{Code: LayoutErrorActivationNotReady, State: string(facts.State), Err: ErrActivationNotReady}
		}
		return nil
	case layout.StateUninitialized:
		if allowUninitialized {
			if !activationReady() {
				return &LayoutError{Code: LayoutErrorActivationNotReady, State: string(facts.State), Err: ErrActivationNotReady}
			}
			return nil
		}
		return &LayoutError{Code: LayoutErrorSchemaUninitialized, State: string(facts.State), Err: ErrSchemaUninitialized}
	case layout.StateV1:
		return &LayoutError{Code: LayoutErrorMigrationRequired, State: string(facts.State), Err: ErrMigrationRequired}
	case layout.StateMigrating:
		return &LayoutError{Code: LayoutErrorMigrationInProgress, State: string(facts.State), Err: ErrMigrationInProgress}
	case layout.StatePendingSmoke, layout.StateRestoreSmoke:
		return &LayoutError{Code: LayoutErrorSmokePending, State: string(facts.State), Err: ErrSmokePending}
	case layout.StatePendingHandoff, layout.StatePending, layout.StateRolledBack:
		return &LayoutError{Code: LayoutErrorPendingAcceptance, State: string(facts.State), Err: ErrPendingAcceptance}
	default:
		return &LayoutError{Code: LayoutErrorDrift, State: string(facts.State), Err: ErrLayoutDrift}
	}
}
