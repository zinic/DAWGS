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

type RestoreEvidence struct {
	Version                int       `json:"version"`
	EvidenceID             string    `json:"evidence_id"`
	ScopeKind              string    `json:"scope_kind"`
	ScopeID                string    `json:"scope_id"`
	SourceDatabaseOID      uint32    `json:"source_database_oid"`
	SourceDatabaseName     string    `json:"source_database_name"`
	SourceSystemIdentifier string    `json:"source_system_identifier"`
	SourceIdentityEpoch    int64     `json:"source_identity_epoch"`
	TargetDatabaseOID      uint32    `json:"target_database_oid"`
	TargetDatabaseName     string    `json:"target_database_name"`
	TargetSystemIdentifier string    `json:"target_system_identifier"`
	SourceResourceID       string    `json:"source_resource_id"`
	TargetResourceID       string    `json:"target_resource_id"`
	RestoreIncarnation     string    `json:"restore_incarnation"`
	ArtifactSHA256         string    `json:"artifact_sha256"`
	OwnerRoleName          string    `json:"owner_role_name"`
	TargetRoleName         string    `json:"target_role_name"`
	ObservedAt             time.Time `json:"observed_at"`
	ExpiresAt              time.Time `json:"expires_at"`
}

func DecodeRestoreEvidence(reader io.Reader) (RestoreEvidence, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return RestoreEvidence{}, err
	}
	if !utf8.Valid(content) {
		return RestoreEvidence{}, fmt.Errorf("restore evidence is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return RestoreEvidence{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var evidence RestoreEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return RestoreEvidence{}, fmt.Errorf("decoding restore evidence: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return RestoreEvidence{}, err
	}
	if err := evidence.Validate(time.Now().UTC()); err != nil {
		return RestoreEvidence{}, err
	}
	return evidence, nil
}

func (s RestoreEvidence) Validate(now time.Time) error {
	if s.Version != 1 || s.EvidenceID == "" || (s.ScopeKind != "bootstrap" && s.ScopeKind != "migration") || s.ScopeID == "" ||
		s.SourceDatabaseOID == 0 || s.SourceDatabaseName == "" || s.SourceSystemIdentifier == "" || s.SourceIdentityEpoch < 1 ||
		s.TargetDatabaseOID == 0 || s.TargetDatabaseName == "" || s.TargetSystemIdentifier == "" ||
		s.SourceResourceID == "" || s.TargetResourceID == "" || s.RestoreIncarnation == "" || s.OwnerRoleName == "" || s.TargetRoleName == "" ||
		s.ObservedAt.IsZero() || s.ExpiresAt.IsZero() || !s.ExpiresAt.After(s.ObservedAt) || !s.ExpiresAt.After(now) || s.ObservedAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("restore evidence is missing required versioned identity, scope, role, resource, or freshness facts")
	}
	if len(s.ArtifactSHA256) != sha256.Size*2 {
		return fmt.Errorf("restore artifact SHA-256 must contain 64 lowercase hexadecimal characters")
	}
	if decoded, err := hex.DecodeString(s.ArtifactSHA256); err != nil || hex.EncodeToString(decoded) != s.ArtifactSHA256 {
		return fmt.Errorf("restore artifact SHA-256 is invalid")
	}
	if s.SourceDatabaseOID == s.TargetDatabaseOID && s.SourceDatabaseName == s.TargetDatabaseName && s.SourceSystemIdentifier == s.TargetSystemIdentifier {
		return fmt.Errorf("restore evidence does not identify a detectably new database incarnation")
	}
	return nil
}

func (s RestoreEvidence) Digest() (string, error) {
	digest, _, err := canonicalDigest(s)
	return digest, err
}
