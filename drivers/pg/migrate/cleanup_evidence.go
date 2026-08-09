package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"unicode/utf8"
)

// CleanupEvidence is the externally retained approval envelope required before
// the migrator may consume an accepted legacy-object map. It deliberately
// binds backup/restore and fleet closure to one database identity epoch.
type CleanupEvidence struct {
	Version                 int       `json:"version"`
	EvidenceID              string    `json:"evidence_id"`
	DatabaseOID             uint32    `json:"database_oid"`
	DatabaseName            string    `json:"database_name"`
	SystemIdentifier        string    `json:"system_identifier"`
	IdentityEpoch           int64     `json:"identity_epoch"`
	ScopeKind               string    `json:"scope_kind"`
	ScopeID                 string    `json:"scope_id"`
	CleanupMapSHA256        string    `json:"cleanup_map_sha256"`
	SmokeReceiptSHA256      string    `json:"smoke_receipt_sha256"`
	AcceptedAt              time.Time `json:"accepted_at"`
	RequiredSoakSeconds     int64     `json:"required_soak_seconds"`
	BackupResourceID        string    `json:"backup_resource_id"`
	BackupArtifactSHA256    string    `json:"backup_artifact_sha256"`
	BackupCompletedAt       time.Time `json:"backup_completed_at"`
	RestoreTestEvidenceID   string    `json:"restore_test_evidence_id"`
	RestoreTargetResourceID string    `json:"restore_target_resource_id"`
	RestoreTestedAt         time.Time `json:"restore_tested_at"`
	FleetLedgerSHA256       string    `json:"fleet_ledger_sha256"`
	FleetReceiptSHA256      string    `json:"fleet_receipt_sha256"`
	FleetTotal              int64     `json:"fleet_total"`
	FleetAccepted           int64     `json:"fleet_accepted"`
	FleetPending            int64     `json:"fleet_pending"`
	FleetDrifted            int64     `json:"fleet_drifted"`
	FleetUnknown            int64     `json:"fleet_unknown"`
	V1RuntimeRetired        bool      `json:"v1_runtime_retired"`
	ApprovedBy              string    `json:"approved_by"`
	ApprovedAt              time.Time `json:"approved_at"`
	ExpiresAt               time.Time `json:"expires_at"`
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func DecodeCleanupEvidence(reader io.Reader) (CleanupEvidence, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return CleanupEvidence{}, err
	}
	if !utf8.Valid(content) {
		return CleanupEvidence{}, fmt.Errorf("cleanup evidence is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return CleanupEvidence{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var evidence CleanupEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return CleanupEvidence{}, fmt.Errorf("decoding cleanup evidence: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CleanupEvidence{}, err
	}
	if err := evidence.Validate(time.Now().UTC()); err != nil {
		return CleanupEvidence{}, err
	}
	return evidence, nil
}

func (s CleanupEvidence) Validate(now time.Time) error {
	if s.Version != 1 || s.EvidenceID == "" || s.DatabaseOID == 0 || s.DatabaseName == "" ||
		s.SystemIdentifier == "" || s.IdentityEpoch < 1 || s.ScopeKind != "migration" || s.ScopeID == "" ||
		s.BackupResourceID == "" || s.RestoreTestEvidenceID == "" || s.RestoreTargetResourceID == "" ||
		s.ApprovedBy == "" || s.AcceptedAt.IsZero() || s.RequiredSoakSeconds < 1 ||
		s.BackupCompletedAt.Before(s.AcceptedAt.Add(time.Duration(s.RequiredSoakSeconds)*time.Second)) ||
		s.RestoreTestedAt.Before(s.BackupCompletedAt) ||
		s.ApprovedAt.Before(s.RestoreTestedAt) || !s.ExpiresAt.After(s.ApprovedAt) || !s.ExpiresAt.After(now) ||
		s.ApprovedAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("cleanup evidence is missing required versioned identity, scope, backup/restore, approval, or freshness facts")
	}
	for label, value := range map[string]string{
		"cleanup map": s.CleanupMapSHA256, "smoke receipt": s.SmokeReceiptSHA256,
		"backup artifact": s.BackupArtifactSHA256, "fleet ledger": s.FleetLedgerSHA256,
		"fleet receipt": s.FleetReceiptSHA256,
	} {
		if !validLowerSHA256(value) {
			return fmt.Errorf("%s SHA-256 must contain 64 lowercase hexadecimal characters", label)
		}
	}
	if s.FleetTotal < 1 || s.FleetAccepted != s.FleetTotal || s.FleetPending != 0 ||
		s.FleetDrifted != 0 || s.FleetUnknown != 0 || !s.V1RuntimeRetired {
		return fmt.Errorf("cleanup evidence does not attest complete fleet acceptance and V1 runtime retirement")
	}
	return nil
}

func (s CleanupEvidence) Digest() (string, error) {
	digest, _, err := canonicalDigest(s)
	return digest, err
}
