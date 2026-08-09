package migrate

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvidenceDecodersAreStrict(t *testing.T) {
	capacityJSON := `{"version":1,"evidence_id":"capacity-1","database_name":"db","system_identifier":"system","observed_at":"2026-08-07T00:00:00Z","expires_at":"2026-08-08T00:00:00Z","available_bytes":100,"required_bytes":40,"estimated_wal_bytes":20}`
	capacity, err := DecodeCapacityEvidence(strings.NewReader(capacityJSON))
	require.NoError(t, err)
	require.Equal(t, int64(100), capacity.AvailableBytes)

	backupJSON := `{"version":1,"evidence_id":"backup-1","database_name":"db","system_identifier":"system","completed_at":"2026-08-07T00:00:00Z","restore_tested_at":"2026-08-07T01:00:00Z","expires_at":"2026-08-08T00:00:00Z","artifact_sha256":"` + strings.Repeat("a", 64) + `"}`
	backup, err := DecodeBackupEvidence(strings.NewReader(backupJSON))
	require.NoError(t, err)
	require.Equal(t, "backup-1", backup.EvidenceID)

	for _, input := range []string{capacityJSON + `{}`, strings.Replace(capacityJSON, `"version":1`, `"version":1,"version":1`, 1)} {
		_, err := DecodeCapacityEvidence(strings.NewReader(input))
		require.Error(t, err)
	}
}

func TestPreflightEvidenceIsIdentityCapacityAndExpiryBound(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	identity := databaseIdentity{Name: "db", SystemIdentifier: "system"}
	capacity := &CapacityEvidence{Version: 1, EvidenceID: "c", DatabaseName: "db", SystemIdentifier: "system",
		ObservedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), AvailableBytes: 100, RequiredBytes: 40, EstimatedWALBytes: 20}
	backup := &BackupEvidence{Version: 1, EvidenceID: "b", DatabaseName: "db", SystemIdentifier: "system",
		CompletedAt: now.Add(-time.Hour), RestoreTestedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ArtifactSHA256: strings.Repeat("a", 64)}
	capacityDigest, backupDigest, err := validatePreflightEvidence(capacity, backup, identity, now)
	require.NoError(t, err)
	require.Len(t, capacityDigest, 64)
	require.Len(t, backupDigest, 64)

	capacity.AvailableBytes = 50
	_, _, err = validatePreflightEvidence(capacity, backup, identity, now)
	require.ErrorContains(t, err, "insufficient")
}
