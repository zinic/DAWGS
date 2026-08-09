package migrate

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRestoreEvidenceStrictValidation(t *testing.T) {
	now := time.Now().UTC()
	value := RestoreEvidence{Version: 1, EvidenceID: "restore-1", ScopeKind: "migration", ScopeID: "scope",
		SourceDatabaseOID: 1, SourceDatabaseName: "source", SourceSystemIdentifier: "cluster-a", SourceIdentityEpoch: 1,
		TargetDatabaseOID: 2, TargetDatabaseName: "target", TargetSystemIdentifier: "cluster-b",
		SourceResourceID: "resource-a", TargetResourceID: "resource-b", RestoreIncarnation: "restore-incarnation-1",
		ArtifactSHA256: strings.Repeat("a", 64), OwnerRoleName: "owner", TargetRoleName: "target",
		ObservedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, value.Validate(now))
	digest, err := value.Digest()
	require.NoError(t, err)
	require.Len(t, digest, 64)

	value.TargetDatabaseOID = value.SourceDatabaseOID
	value.TargetDatabaseName = value.SourceDatabaseName
	value.TargetSystemIdentifier = value.SourceSystemIdentifier
	require.ErrorContains(t, value.Validate(now), "detectably new")
}
