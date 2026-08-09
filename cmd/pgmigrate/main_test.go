package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/specterops/dawgs/drivers/pg/migrate"
	"github.com/stretchr/testify/require"
)

func TestLoadConnectionRequiresExactPrivateModeAndStrictEnvelope(t *testing.T) {
	t.Setenv("CONNECTION_STRING", "")
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.json")
	require.NoError(t, os.WriteFile(valid, []byte(`{"version":1,"connection_string":"postgresql://example/db"}`), 0o600))
	connectionString, err := loadConnection(valid)
	require.NoError(t, err)
	require.Equal(t, "postgresql://example/db", connectionString)

	for _, mode := range []os.FileMode{0o400, 0o640, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(directory, "mode-"+mode.String()+".json")
			require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"connection_string":"postgresql://example/db"}`), 0o600))
			require.NoError(t, os.Chmod(path, mode))
			_, err := loadConnection(path)
			require.ErrorContains(t, err, "mode 0600")
		})
	}

	trailing := filepath.Join(directory, "trailing.json")
	require.NoError(t, os.WriteFile(trailing, []byte(`{"version":1,"connection_string":"postgresql://example/db"}{}`), 0o600))
	_, err = loadConnection(trailing)
	require.ErrorContains(t, err, "exactly one value")
}

func TestValidateArchiveDoesNotRequireDatabaseCredentials(t *testing.T) {
	t.Setenv("CONNECTION_STRING", "")
	root := t.TempDir()
	kinds := []string{"binary", "module", "frozen_v1", "schema", "runbook", "evidence_schema"}
	artifacts := make([]migrate.ArchiveArtifact, 0, len(kinds))
	for _, kind := range kinds {
		content := []byte(kind)
		path := kind + ".artifact"
		require.NoError(t, os.WriteFile(filepath.Join(root, path), content, 0o600))
		digest := sha256.Sum256(content)
		artifacts = append(artifacts, migrate.ArchiveArtifact{Path: path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(content)), Kind: kind})
	}
	created := time.Now().UTC()
	manifest := migrate.ArchiveManifest{Version: 1, ArchiveID: "archive", SourceRevision: "revision", CreatedAt: created,
		EOLAt: created.AddDate(1, 0, 0), PublicationOwner: "release", RetentionOwner: "operations", IncidentOwner: "postgres",
		RetrievalCoordinates: "immutable://archive", Cadence: "weekly", PostgreSQLMajors: []int{16, 18}, Artifacts: artifacts}
	content, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestPath := filepath.Join(root, "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, content, 0o600))

	var stdout, stderr bytes.Buffer
	require.NoError(t, run(t.Context(), []string{"validate-archive", "--archive-manifest", manifestPath, "--archive-root", root}, &stdout, &stderr))
	require.Contains(t, stdout.String(), `"files_verified": true`)
}

func TestValidateFleetDoesNotRequireDatabaseCredentials(t *testing.T) {
	t.Setenv("CONNECTION_STRING", "")
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	payload := fmt.Sprintf(`{"version":1,"ledger_id":"fleet","source_revision":"rev","generated_at":%q,"postgresql_majors":[16,18],"installations":[{"installation_id":"site","state":"accepted_v2","scope_kind":"migration","database_name":"db","system_identifier":"system","identity_epoch":1,"owner_role":"owner","target_role":"target","application_version":"2","runtime_sha256":%q,"migrator_version":"2","migrator_sha256":%q,"manifest_sha256":%q,"postgresql_version_num":180000,"inventory_digest":%q,"capacity_evidence_digest":%q,"backup_evidence_digest":%q,"writer_evidence_digest":%q,"acceptance_receipt_digest":%q,"smoke_receipt_digest":%q,"cleanup_receipt_digest":%q,"final_health_digest":%q,"old_writer_retired":true}]}`,
		time.Now().UTC().Format(time.RFC3339Nano), digest, digest, digest, digest, digest, digest, digest, digest, digest, digest, digest)
	path := filepath.Join(t.TempDir(), "fleet.json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	var stdout, stderr bytes.Buffer
	require.NoError(t, run(t.Context(), []string{"validate-fleet", "--fleet-ledger", path}, &stdout, &stderr))
	require.Contains(t, stdout.String(), `"closure_ready": true`)
}
