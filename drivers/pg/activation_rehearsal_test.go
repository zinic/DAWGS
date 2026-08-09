//go:build dawgs_rehearsal

package pg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRehearsalBuildOpensOnlyTaggedActivationSeam(t *testing.T) {
	require.True(t, activationReady())
}
