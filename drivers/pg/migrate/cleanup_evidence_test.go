package migrate

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validCleanupEvidence(now time.Time) CleanupEvidence {
	return CleanupEvidence{Version: 1, EvidenceID: "cleanup-1", DatabaseOID: 1, DatabaseName: "database",
		SystemIdentifier: "system", IdentityEpoch: 1, ScopeKind: "migration", ScopeID: "migration-1",
		CleanupMapSHA256: strings.Repeat("a", 64), SmokeReceiptSHA256: strings.Repeat("b", 64),
		AcceptedAt: now.Add(-4 * time.Hour), RequiredSoakSeconds: 3600,
		BackupResourceID: "backup/resource", BackupArtifactSHA256: strings.Repeat("c", 64),
		BackupCompletedAt: now.Add(-3 * time.Hour), RestoreTestEvidenceID: "restore-test-1",
		RestoreTargetResourceID: "restore/resource", RestoreTestedAt: now.Add(-2 * time.Hour),
		FleetLedgerSHA256: strings.Repeat("d", 64), FleetReceiptSHA256: strings.Repeat("e", 64),
		FleetTotal: 2, FleetAccepted: 2, V1RuntimeRetired: true, ApprovedBy: "release-owner",
		ApprovedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
}

func TestCleanupEvidenceStrictValidation(t *testing.T) {
	now := time.Now().UTC()
	evidence := validCleanupEvidence(now)
	require.NoError(t, evidence.Validate(now))
	digest, err := evidence.Digest()
	require.NoError(t, err)
	require.Len(t, digest, 64)
	evidence.FleetPending = 1
	require.ErrorContains(t, evidence.Validate(now), "fleet acceptance")
}
