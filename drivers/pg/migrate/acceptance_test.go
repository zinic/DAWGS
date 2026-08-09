package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnreleasedMigrationDataMovementFailsClosed(t *testing.T) {
	_, err := (&Engine{}).Migrate(context.Background(), Manifest{}, "token")
	require.ErrorContains(t, err, "activation_not_ready")
	_, err = (&Engine{}).Cleanup(context.Background(), "migration", "token")
	require.ErrorContains(t, err, "activation_not_ready")
	_, err = (&Engine{}).Rollback(context.Background(), "migration", "token")
	require.ErrorContains(t, err, "activation_not_ready")
}

func TestMigrationPostgresMajorAdmission(t *testing.T) {
	require.NoError(t, validatePostgresVersion(160012))
	require.NoError(t, validatePostgresVersion(180004))
	for _, version := range []int{150010, 170006, 190000} {
		require.ErrorContains(t, validatePostgresVersion(version), "unsupported_postgres_major")
	}
}

func TestValidateTargetIdentityRequiresDirectRestrictedDistinctRole(t *testing.T) {
	operator := sessionIdentity{RoleOID: 30, DatabaseOwner: 10}
	valid := sessionIdentity{
		RoleOID:       20,
		RoleName:      "dawgs_target",
		SessionUser:   "dawgs_target",
		CurrentUser:   "dawgs_target",
		DatabaseOwner: 10,
	}

	require.NoError(t, validateTargetIdentity(valid, operator))

	tests := map[string]sessionIdentity{
		"set role":          withTarget(valid, func(value *sessionIdentity) { value.CurrentUser = "another_role" }),
		"role lookup drift": withTarget(valid, func(value *sessionIdentity) { value.RoleName = "another_role" }),
		"superuser":         withTarget(valid, func(value *sessionIdentity) { value.Superuser = true }),
		"database owner":    withTarget(valid, func(value *sessionIdentity) { value.RoleOID = value.DatabaseOwner }),
		"operator":          withTarget(valid, func(value *sessionIdentity) { value.RoleOID = operator.RoleOID }),
		"membership":        withTarget(valid, func(value *sessionIdentity) { value.HasMembership = true }),
	}
	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validateTargetIdentity(target, operator))
		})
	}
}

func withTarget(value sessionIdentity, update func(*sessionIdentity)) sessionIdentity {
	update(&value)
	return value
}
