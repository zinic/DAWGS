package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/specterops/dawgs/drivers/pg/internal/layout"
	"github.com/specterops/dawgs/graph"
	"github.com/specterops/dawgs/util/size"
	"github.com/stretchr/testify/require"
)

func TestOperationsRefuseLocallyBeforeStartupAssertion(t *testing.T) {
	driver := NewDriver(size.Gibibyte, nil)
	ctx := context.Background()
	delegateCalled := false

	operations := []func() error{
		func() error {
			return driver.ReadTransaction(ctx, func(graph.Transaction) error { delegateCalled = true; return nil })
		},
		func() error {
			return driver.WriteTransaction(ctx, func(graph.Transaction) error { delegateCalled = true; return nil })
		},
		func() error {
			return driver.BatchOperation(ctx, func(graph.Batch) error { delegateCalled = true; return nil })
		},
		func() error { return driver.Run(ctx, "select 1", nil) },
		func() error { return driver.WipeGraph(ctx, nil) },
		func() error { return driver.OptimizeStorage(ctx) },
		func() error { return driver.DeleteNodesByKinds(ctx, nil, nil) },
		func() error { return driver.DeleteRelationshipsByKinds(ctx, graph.Kinds{graph.StringKind("EDGE")}) },
		func() error { return driver.SetDefaultGraph(ctx, graph.Graph{Name: "default"}) },
		func() error { return driver.RefreshKinds(ctx) },
		func() error { return driver.SchemaManager.Fetch(ctx) },
		func() error { _, err := driver.MapKind(ctx, graph.StringKind("KIND")); return err },
		func() error { _, err := driver.MapKinds(ctx, graph.Kinds{graph.StringKind("KIND")}); return err },
		func() error { _, err := driver.MapKindID(ctx, 1); return err },
		func() error { _, err := driver.MapKindIDs(ctx, []int16{1}); return err },
		func() error { _, err := driver.AssertKinds(ctx, graph.Kinds{graph.StringKind("KIND")}); return err },
	}
	for _, operation := range operations {
		require.ErrorIs(t, operation(), ErrStartupValidation)
	}
	require.False(t, delegateCalled)
}

func TestSessionIdentityStatementsAreRejected(t *testing.T) {
	for _, statement := range []string{
		"SET ROLE operator", " reset role", "SET SESSION AUTHORIZATION operator",
		"RESET SESSION AUTHORIZATION", "select set_config('role', 'operator', false)",
		"/* caller */ SET ROLE operator", "DISCARD ALL", "SET SESSION ROLE operator",
		"SET LOCAL /* split */ ROLE operator", "select 1; set role operator",
		"select pg_catalog.set_config('session_authorization', 'operator', false)",
		"select set_config($1, 'operator', false)", "select set_config($$role$$, 'operator', false)",
		"select set_config('work_mem', '4MB', true)", "SET work_mem = '4MB'",
		"RESET ALL", "CREATE TEMP TABLE edge(id bigint)",
		"DO $$ begin execute 'set role operator'; end $$",
	} {
		require.True(t, isSessionIdentityStatement(statement), statement)
	}
	for _, statement := range []string{
		"select 'set role operator'",
		"set local work_mem = '4MB'", "select reset, role from application_table",
		"insert into application_table(id) values(1) on conflict(id) do nothing",
	} {
		require.False(t, isSessionIdentityStatement(statement), statement)
	}
}

func FuzzSessionIdentityStatementScanner(f *testing.F) {
	for _, seed := range []string{"", "select 1", "/* nested /* comment */ */ set role x", "select 'unterminated"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, statement string) {
		_ = isSessionIdentityStatement(statement)
	})
}

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
