//go:build !dawgs_rehearsal

package pg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseBuildKeepsActivationClosed(t *testing.T) {
	require.False(t, activationReady())
}
