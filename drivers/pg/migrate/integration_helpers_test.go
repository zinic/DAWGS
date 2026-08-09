package migrate

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func connectionForDatabase(t *testing.T, raw, database string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	parsed.Path = "/" + database
	return parsed.String()
}

func connectionForRoleDatabase(t *testing.T, raw, role, password, database string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	parsed.User = url.UserPassword(role, password)
	parsed.Path = "/" + database
	return parsed.String()
}
