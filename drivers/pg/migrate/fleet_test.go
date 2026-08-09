package migrate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validFleetLedger() FleetLedger {
	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	return FleetLedger{Version: 1, LedgerID: "fleet-1", SourceRevision: "revision", GeneratedAt: now,
		PostgreSQLMajors: []int{16, 18}, Installations: []FleetInstallation{{
			InstallationID: "site-1", State: FleetStateAcceptedV2, ScopeKind: FleetScopeMigration,
			DatabaseName: "db", SystemIdentifier: "system", IdentityEpoch: 1, OwnerRole: "owner", TargetRole: "target",
			ApplicationVersion: "2", RuntimeSHA256: digest, MigratorVersion: "2", MigratorSHA256: digest,
			ManifestSHA256: digest, PostgreSQLVersionNum: 180000, InventoryDigest: digest,
			CapacityEvidenceDigest: digest, BackupEvidenceDigest: digest, WriterEvidenceDigest: digest,
			AcceptanceReceiptDigest: digest, SmokeReceiptDigest: digest, CleanupReceiptDigest: digest,
			FinalHealthDigest: digest, OldWriterRetired: true,
		}}}
}

func TestFleetLedgerValidationAndStrictDecode(t *testing.T) {
	value := validFleetLedger()
	summary, err := value.Validate()
	require.NoError(t, err)
	require.True(t, summary.ClosureReady)
	require.Len(t, summary.Digest, 64)

	payload, err := json.Marshal(value)
	require.NoError(t, err)
	decoded, err := DecodeFleetLedger(bytes.NewReader(payload))
	require.NoError(t, err)
	require.Equal(t, value.LedgerID, decoded.LedgerID)

	payload = append(payload[:len(payload)-1], []byte(`,"unknown":true}`)...)
	_, err = DecodeFleetLedger(bytes.NewReader(payload))
	require.Error(t, err)
}

func TestFleetLedgerFrozenAndDecommissionContracts(t *testing.T) {
	value := validFleetLedger()
	value.Installations[0].State = FleetStateOpen
	value.Installations[0].Blockers = []string{"unreachable"}
	frozen := value.GeneratedAt.Add(time.Minute)
	value.FrozenAt = &frozen
	_, err := value.Validate()
	require.ErrorContains(t, err, "closure-ready")

	value = validFleetLedger()
	value.Installations[0].State = FleetStateDecommissioned
	value.Installations[0].TargetRole = ""
	value.Installations[0].Decommission = &DecommissionRecord{Version: 1, ApproverIdentity: "approver", Reason: "retired",
		OwnershipProofDigest: strings.Repeat("b", 64), LastKnownState: "v1", WriterRetirementDigest: strings.Repeat("c", 64),
		DecommissionedAt: time.Now().UTC(), EvidenceDigest: strings.Repeat("d", 64)}
	_, err = value.Validate()
	require.NoError(t, err)
}
