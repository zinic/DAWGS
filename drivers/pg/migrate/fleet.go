package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"time"
)

const (
	FleetStateOpen           = "open"
	FleetStateAcceptedV2     = "accepted_v2"
	FleetStateDecommissioned = "decommissioned"
	FleetScopeMigration      = "migration"
	FleetScopeBootstrap      = "bootstrap"
)

// FleetLedger is the versioned, portable fleet-transition record. It contains
// no credentials or DSNs; fields ending in Digest or SHA256 refer to retained
// immutable evidence stored outside the operational database.
type FleetLedger struct {
	Version          int                 `json:"version"`
	LedgerID         string              `json:"ledger_id"`
	SourceRevision   string              `json:"source_revision"`
	GeneratedAt      time.Time           `json:"generated_at"`
	FrozenAt         *time.Time          `json:"frozen_at,omitempty"`
	PostgreSQLMajors []int               `json:"postgresql_majors"`
	Installations    []FleetInstallation `json:"installations"`
}

type FleetInstallation struct {
	InstallationID          string              `json:"installation_id"`
	State                   string              `json:"state"`
	ScopeKind               string              `json:"scope_kind,omitempty"`
	DatabaseName            string              `json:"database_name"`
	SystemIdentifier        string              `json:"system_identifier"`
	IdentityEpoch           int64               `json:"identity_epoch"`
	OwnerRole               string              `json:"owner_role"`
	TargetRole              string              `json:"target_role,omitempty"`
	ApplicationVersion      string              `json:"application_version"`
	RuntimeSHA256           string              `json:"runtime_sha256"`
	MigratorVersion         string              `json:"migrator_version"`
	MigratorSHA256          string              `json:"migrator_sha256"`
	ManifestSHA256          string              `json:"manifest_sha256"`
	PostgreSQLVersionNum    int                 `json:"postgresql_version_num"`
	InventoryDigest         string              `json:"inventory_digest"`
	CapacityEvidenceDigest  string              `json:"capacity_evidence_digest"`
	BackupEvidenceDigest    string              `json:"backup_evidence_digest"`
	WriterEvidenceDigest    string              `json:"writer_evidence_digest"`
	MigrationReceiptDigest  string              `json:"migration_receipt_digest,omitempty"`
	ValidationReceiptDigest string              `json:"validation_receipt_digest,omitempty"`
	AcceptanceReceiptDigest string              `json:"acceptance_receipt_digest,omitempty"`
	SmokeReceiptDigest      string              `json:"smoke_receipt_digest,omitempty"`
	CleanupReceiptDigest    string              `json:"cleanup_receipt_digest,omitempty"`
	RestoreReceiptDigest    string              `json:"restore_receipt_digest,omitempty"`
	FinalHealthDigest       string              `json:"final_health_digest,omitempty"`
	OldWriterRetired        bool                `json:"old_writer_retired"`
	Blockers                []string            `json:"blockers,omitempty"`
	Decommission            *DecommissionRecord `json:"decommission,omitempty"`
}

type DecommissionRecord struct {
	Version                int       `json:"version"`
	ApproverIdentity       string    `json:"approver_identity"`
	Reason                 string    `json:"reason"`
	OwnershipProofDigest   string    `json:"ownership_proof_digest"`
	LastKnownState         string    `json:"last_known_state"`
	WriterRetirementDigest string    `json:"writer_retirement_digest"`
	DecommissionedAt       time.Time `json:"decommissioned_at"`
	EvidenceDigest         string    `json:"evidence_digest"`
}

type FleetLedgerSummary struct {
	Digest         string `json:"digest"`
	Installations  int    `json:"installations"`
	Accepted       int    `json:"accepted"`
	Decommissioned int    `json:"decommissioned"`
	Open           int    `json:"open"`
	ClosureReady   bool   `json:"closure_ready"`
}

func DecodeFleetLedger(reader io.Reader) (FleetLedger, error) {
	var value FleetLedger
	if err := decodeStrictEvidence(reader, &value, "fleet ledger"); err != nil {
		return FleetLedger{}, err
	}
	if _, err := value.Validate(); err != nil {
		return FleetLedger{}, err
	}
	return value, nil
}

func (s FleetLedger) Validate() (FleetLedgerSummary, error) {
	if s.Version != 1 || s.LedgerID == "" || s.SourceRevision == "" || s.GeneratedAt.IsZero() {
		return FleetLedgerSummary{}, fmt.Errorf("invalid versioned fleet ledger header")
	}
	if len(s.PostgreSQLMajors) == 0 {
		return FleetLedgerSummary{}, fmt.Errorf("fleet ledger must declare PostgreSQL majors")
	}
	majors := append([]int(nil), s.PostgreSQLMajors...)
	sort.Ints(majors)
	for index, major := range majors {
		if major != 16 && major != 18 {
			return FleetLedgerSummary{}, fmt.Errorf("unsupported PostgreSQL major %d in fleet ledger", major)
		}
		if index > 0 && majors[index-1] == major {
			return FleetLedgerSummary{}, fmt.Errorf("duplicate PostgreSQL major %d in fleet ledger", major)
		}
	}
	seen := map[string]struct{}{}
	summary := FleetLedgerSummary{Installations: len(s.Installations)}
	for _, installation := range s.Installations {
		if _, found := seen[installation.InstallationID]; installation.InstallationID == "" || found {
			return FleetLedgerSummary{}, fmt.Errorf("fleet installation IDs must be nonempty and unique")
		}
		seen[installation.InstallationID] = struct{}{}
		if err := installation.validate(majors); err != nil {
			return FleetLedgerSummary{}, fmt.Errorf("installation %q: %w", installation.InstallationID, err)
		}
		switch installation.State {
		case FleetStateAcceptedV2:
			summary.Accepted++
		case FleetStateDecommissioned:
			summary.Decommissioned++
		case FleetStateOpen:
			summary.Open++
		}
	}
	if s.FrozenAt != nil {
		if s.FrozenAt.Before(s.GeneratedAt) || summary.Open != 0 {
			return FleetLedgerSummary{}, fmt.Errorf("a frozen fleet ledger must be closure-ready and frozen at or after generation")
		}
	}
	summary.ClosureReady = summary.Open == 0 && len(s.Installations) > 0
	digest, _, err := canonicalDigest(s)
	if err != nil {
		return FleetLedgerSummary{}, err
	}
	summary.Digest = digest
	return summary, nil
}

func (s FleetInstallation) validate(majors []int) error {
	if s.DatabaseName == "" || s.SystemIdentifier == "" || s.IdentityEpoch < 1 || s.OwnerRole == "" ||
		s.ApplicationVersion == "" || s.MigratorVersion == "" || s.PostgreSQLVersionNum <= 0 {
		return fmt.Errorf("database identity, ownership, versions, and identity epoch are required")
	}
	major := s.PostgreSQLVersionNum / 10000
	if index := sort.SearchInts(majors, major); index == len(majors) || majors[index] != major {
		return fmt.Errorf("PostgreSQL version %d is outside the declared matrix", s.PostgreSQLVersionNum)
	}
	for name, value := range map[string]string{
		"runtime_sha256": s.RuntimeSHA256, "migrator_sha256": s.MigratorSHA256,
		"manifest_sha256": s.ManifestSHA256, "inventory_digest": s.InventoryDigest,
		"capacity_evidence_digest": s.CapacityEvidenceDigest, "backup_evidence_digest": s.BackupEvidenceDigest,
		"writer_evidence_digest": s.WriterEvidenceDigest,
	} {
		if !isSHA256(value) {
			return fmt.Errorf("%s must be a SHA-256 digest", name)
		}
	}
	switch s.State {
	case FleetStateOpen:
		if s.Decommission != nil {
			return fmt.Errorf("open installation cannot contain a decommission record")
		}
	case FleetStateAcceptedV2:
		if len(s.Blockers) != 0 || s.Decommission != nil || !s.OldWriterRetired || s.TargetRole == "" {
			return fmt.Errorf("accepted installation must have no blockers, a target, and retired old writer")
		}
		for name, value := range map[string]string{"acceptance": s.AcceptanceReceiptDigest, "smoke": s.SmokeReceiptDigest, "cleanup": s.CleanupReceiptDigest, "health": s.FinalHealthDigest} {
			if !isSHA256(value) {
				return fmt.Errorf("accepted installation requires %s receipt digest", name)
			}
		}
		if s.ScopeKind != FleetScopeMigration && s.ScopeKind != FleetScopeBootstrap {
			return fmt.Errorf("accepted installation scope_kind must be migration or bootstrap")
		}
	case FleetStateDecommissioned:
		if s.Decommission == nil || len(s.Blockers) != 0 {
			return fmt.Errorf("decommissioned installation requires a record and no blockers")
		}
		if err := s.Decommission.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported fleet state %q", s.State)
	}
	return nil
}

func (s DecommissionRecord) validate() error {
	if s.Version != 1 || s.ApproverIdentity == "" || s.Reason == "" || s.LastKnownState == "" || s.DecommissionedAt.IsZero() {
		return fmt.Errorf("invalid versioned decommission record")
	}
	for name, value := range map[string]string{"ownership proof": s.OwnershipProofDigest, "writer retirement": s.WriterRetirementDigest, "supporting evidence": s.EvidenceDigest} {
		if !isSHA256(value) {
			return fmt.Errorf("decommission %s must be a SHA-256 digest", name)
		}
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
