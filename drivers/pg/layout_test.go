package pg

import (
	"errors"
	"testing"

	"github.com/specterops/dawgs/drivers/pg/internal/layout"
	"github.com/stretchr/testify/require"
)

func TestRequireOperationalRefusesEveryTransitionState(t *testing.T) {
	tests := []struct {
		state layout.State
		err   error
		code  LayoutErrorCode
	}{
		{layout.StatePendingHandoff, ErrPendingAcceptance, LayoutErrorPendingAcceptance},
		{layout.StateMigrating, ErrMigrationInProgress, LayoutErrorMigrationInProgress},
		{layout.StatePending, ErrPendingAcceptance, LayoutErrorPendingAcceptance},
		{layout.StatePendingSmoke, ErrSmokePending, LayoutErrorSmokePending},
		{layout.StateRestoreSmoke, ErrSmokePending, LayoutErrorSmokePending},
		{layout.StateRolledBack, ErrPendingAcceptance, LayoutErrorPendingAcceptance},
	}

	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			err := requireOperational(operationalFacts{Facts: layout.Facts{State: test.state}}, false)
			require.ErrorIs(t, err, test.err)
			var layoutErr *LayoutError
			require.True(t, errors.As(err, &layoutErr))
			require.Equal(t, test.code, layoutErr.Code)
			require.Equal(t, string(test.state), layoutErr.State)
		})
	}
}

func TestRequireOperationalAcceptsOnlyAcceptedV2(t *testing.T) {
	accepted := operationalFacts{Facts: layout.Facts{State: layout.StateV2Accepted}, Readiness: validRuntimeReadiness()}
	if activationReady() {
		require.NoError(t, requireOperational(accepted, false))
	} else {
		require.ErrorIs(t, requireOperational(accepted, false), ErrActivationNotReady)
	}
	require.ErrorIs(t, requireOperational(operationalFacts{Facts: layout.Facts{State: layout.StateUninitialized}}, false), ErrSchemaUninitialized)
	if activationReady() {
		require.NoError(t, requireOperational(operationalFacts{Facts: layout.Facts{State: layout.StateUninitialized}}, true))
	} else {
		require.ErrorIs(t, requireOperational(operationalFacts{Facts: layout.Facts{State: layout.StateUninitialized}}, true), ErrActivationNotReady)
	}
}

func TestRequireOperationalValidatesAcceptedIdentityAndSmoke(t *testing.T) {
	tests := []struct {
		name   string
		update func(*runtimeReadiness)
		err    error
	}{
		{"unchecked", func(value *runtimeReadiness) { value.Checked = false }, ErrRestoreRebind},
		{"database oid", func(value *runtimeReadiness) { value.CurrentDatabaseOID++ }, ErrRestoreRebind},
		{"database name", func(value *runtimeReadiness) { value.CurrentDatabaseName = "restored" }, ErrRestoreRebind},
		{"system identifier", func(value *runtimeReadiness) { value.CurrentSystemIdentifier = "restored" }, ErrRestoreRebind},
		{"smoke pending", func(value *runtimeReadiness) { value.SmokeStatus = "pending" }, ErrSmokePending},
		{"smoke receipt missing", func(value *runtimeReadiness) { value.SmokeReceiptDigest = "" }, ErrSmokePending},
		{"role oid", func(value *runtimeReadiness) { value.CurrentRoleOID++ }, ErrWrongRuntimeRole},
		{"role name", func(value *runtimeReadiness) { value.CurrentRoleName = "operator" }, ErrWrongRuntimeRole},
		{"set role", func(value *runtimeReadiness) { value.CurrentUser = "operator" }, ErrWrongRuntimeRole},
		{"superuser", func(value *runtimeReadiness) { value.CurrentRoleSuperuser = true }, ErrWrongRuntimeRole},
		{"owner", func(value *runtimeReadiness) { value.CurrentRoleOID = value.CurrentDatabaseOwnerOID }, ErrWrongRuntimeRole},
		{"membership", func(value *runtimeReadiness) { value.CurrentRoleHasMembership = true }, ErrWrongRuntimeRole},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readiness := validRuntimeReadiness()
			test.update(&readiness)
			err := requireOperational(operationalFacts{Facts: layout.Facts{State: layout.StateV2Accepted}, Readiness: readiness}, false)
			require.ErrorIs(t, err, test.err)
		})
	}
}

func validRuntimeReadiness() runtimeReadiness {
	return runtimeReadiness{
		Checked: true, RecordedDatabaseOID: 1, RecordedDatabaseName: "dawgs", RecordedSystemIdentifier: "cluster",
		RecordedTargetRoleOID: 2, RecordedTargetRoleName: "target", SmokeStatus: "passed", SmokeReceiptDigest: "digest",
		CurrentDatabaseOID: 1, CurrentDatabaseName: "dawgs", CurrentSystemIdentifier: "cluster", CurrentRoleOID: 2,
		CurrentRoleName: "target", SessionUser: "target", CurrentUser: "target", CurrentDatabaseOwnerOID: 3,
	}
}
