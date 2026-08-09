package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Report struct {
	Version           int             `json:"version"`
	Action            string          `json:"action"`
	State             string          `json:"state"`
	MigrationID       string          `json:"migration_id,omitempty"`
	ManifestSHA256    string          `json:"manifest_sha256,omitempty"`
	PreflightDigest   string          `json:"preflight_digest,omitempty"`
	ValidationDigest  string          `json:"validation_digest,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
	AttemptID         string          `json:"attempt_id,omitempty"`
	ReceiptDigest     string          `json:"receipt_digest,omitempty"`
	TargetRole        string          `json:"target_role,omitempty"`
	ScopeKind         string          `json:"scope_kind,omitempty"`
	LayoutVersion     int16           `json:"layout_version,omitempty"`
	TopologySHA256    string          `json:"topology_sha256,omitempty"`
	DatabaseOID       uint32          `json:"database_oid,omitempty"`
	DatabaseName      string          `json:"database_name,omitempty"`
	SystemIdentifier  string          `json:"system_identifier,omitempty"`
	IdentityEpoch     int64           `json:"identity_epoch,omitempty"`
	OwnerRole         string          `json:"owner_role,omitempty"`
	SmokeStatus       string          `json:"smoke_status,omitempty"`
	SmokeReceipt      string          `json:"smoke_receipt,omitempty"`
	CleanupStatus     string          `json:"cleanup_status,omitempty"`
	CleanupMapSHA256  string          `json:"cleanup_map_sha256,omitempty"`
	Recovery          string          `json:"recovery,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	Blockers          []string        `json:"blockers,omitempty"`
	Progress          []GraphProgress `json:"progress,omitempty"`
}

type GraphProgress struct {
	GraphID    int32  `json:"graph_id"`
	Phase      string `json:"phase"`
	HighWater  *int64 `json:"high_water,omitempty"`
	CopiedRows int64  `json:"copied_rows"`
	TotalRows  int64  `json:"total_rows"`
}

func WriteReport(path string, report Report) error {
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".dawgs-pg-migrate-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
