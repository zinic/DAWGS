package frontend

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequireReadOnlyRejectsMutationClauses(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
	}{
		{"create", `create (n:Thing) return n`},
		{"set", `match (n:Thing) set n.value = 1 return n`},
		{"delete", `match (n:Thing) delete n`},
		{"multipart", `match (n:Thing) set n.value = 1 with n return n`},
	} {
		t.Run(test.name, func(t *testing.T) {
			query, err := ParseCypher(NewContext(), test.query)
			require.NoError(t, err)
			require.ErrorIs(t, RequireReadOnly(query), ErrMutationNotAllowed)
		})
	}
	query, err := ParseCypher(NewContext(), `match (n:Thing) return n`)
	require.NoError(t, err)
	require.NoError(t, RequireReadOnly(query))
}
