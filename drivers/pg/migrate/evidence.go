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

type CapacityEvidence struct {
	Version           int       `json:"version"`
	EvidenceID        string    `json:"evidence_id"`
	DatabaseName      string    `json:"database_name"`
	SystemIdentifier  string    `json:"system_identifier"`
	ObservedAt        time.Time `json:"observed_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	AvailableBytes    int64     `json:"available_bytes"`
	RequiredBytes     int64     `json:"required_bytes"`
	EstimatedWALBytes int64     `json:"estimated_wal_bytes"`
}

type BackupEvidence struct {
	Version          int       `json:"version"`
	EvidenceID       string    `json:"evidence_id"`
	DatabaseName     string    `json:"database_name"`
	SystemIdentifier string    `json:"system_identifier"`
	CompletedAt      time.Time `json:"completed_at"`
	RestoreTestedAt  time.Time `json:"restore_tested_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	ArtifactSHA256   string    `json:"artifact_sha256"`
}

func decodeStrictEvidence(reader io.Reader, destination any, label string) error {
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("%s evidence is not valid UTF-8", label)
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decoding %s evidence: %w", label, err)
	}
	return requireJSONEOF(decoder)
}

func DecodeCapacityEvidence(reader io.Reader) (CapacityEvidence, error) {
	var evidence CapacityEvidence
	if err := decodeStrictEvidence(reader, &evidence, "capacity"); err != nil {
		return CapacityEvidence{}, err
	}
	if evidence.Version != 1 || evidence.EvidenceID == "" || evidence.DatabaseName == "" || evidence.SystemIdentifier == "" ||
		evidence.ObservedAt.IsZero() || !evidence.ExpiresAt.After(evidence.ObservedAt) || evidence.AvailableBytes < 0 ||
		evidence.RequiredBytes < 0 || evidence.EstimatedWALBytes < 0 {
		return CapacityEvidence{}, fmt.Errorf("invalid versioned capacity evidence")
	}
	return evidence, nil
}

func DecodeBackupEvidence(reader io.Reader) (BackupEvidence, error) {
	var evidence BackupEvidence
	if err := decodeStrictEvidence(reader, &evidence, "backup"); err != nil {
		return BackupEvidence{}, err
	}
	if evidence.Version != 1 || evidence.EvidenceID == "" || evidence.DatabaseName == "" || evidence.SystemIdentifier == "" ||
		evidence.CompletedAt.IsZero() || evidence.RestoreTestedAt.Before(evidence.CompletedAt) ||
		!evidence.ExpiresAt.After(evidence.RestoreTestedAt) || len(evidence.ArtifactSHA256) != sha256.Size*2 {
		return BackupEvidence{}, fmt.Errorf("invalid versioned backup evidence")
	}
	if decoded, err := hex.DecodeString(evidence.ArtifactSHA256); err != nil || len(decoded) != sha256.Size {
		return BackupEvidence{}, fmt.Errorf("backup artifact SHA-256 must be 64 hexadecimal characters")
	}
	return evidence, nil
}

func validatePreflightEvidence(capacity *CapacityEvidence, backup *BackupEvidence, identity databaseIdentity, now time.Time) (string, string, error) {
	if capacity == nil || backup == nil {
		return "", "", fmt.Errorf("preflight_evidence_required: capacity and backup/restore evidence are required")
	}
	if capacity.DatabaseName != identity.Name || capacity.SystemIdentifier != identity.SystemIdentifier || !capacity.ExpiresAt.After(now) {
		return "", "", fmt.Errorf("capacity evidence is stale or bound to a different database")
	}
	if capacity.AvailableBytes < capacity.RequiredBytes+capacity.EstimatedWALBytes {
		return "", "", fmt.Errorf("capacity evidence reports insufficient bytes")
	}
	if backup.DatabaseName != identity.Name || backup.SystemIdentifier != identity.SystemIdentifier || !backup.ExpiresAt.After(now) {
		return "", "", fmt.Errorf("backup evidence is stale or bound to a different database")
	}
	capacityDigest, _, err := canonicalDigest(capacity)
	if err != nil {
		return "", "", err
	}
	backupDigest, _, err := canonicalDigest(backup)
	return capacityDigest, backupDigest, err
}
