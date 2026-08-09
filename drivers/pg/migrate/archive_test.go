package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validArchiveManifest(t *testing.T, root string) ArchiveManifest {
	t.Helper()
	kinds := []string{"binary", "module", "frozen_v1", "schema", "runbook", "evidence_schema"}
	artifacts := make([]ArchiveArtifact, 0, len(kinds))
	for _, kind := range kinds {
		path := kind + ".artifact"
		content := []byte(kind)
		require.NoError(t, os.WriteFile(filepath.Join(root, path), content, 0o600))
		digest := sha256.Sum256(content)
		artifacts = append(artifacts, ArchiveArtifact{Path: path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(content)), Kind: kind})
	}
	created := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	return ArchiveManifest{Version: 1, ArchiveID: "release", SourceRevision: "revision", CreatedAt: created,
		EOLAt: created.AddDate(1, 0, 0), PublicationOwner: "release", RetentionOwner: "operations", IncidentOwner: "postgres",
		RetrievalCoordinates: "immutable://release", Cadence: "weekly", PostgreSQLMajors: []int{16, 18}, Artifacts: artifacts}
}

func TestArchiveManifestValidatesAndVerifiesFiles(t *testing.T) {
	root := t.TempDir()
	manifest := validArchiveManifest(t, root)
	verification, err := manifest.VerifyFiles(root)
	require.NoError(t, err)
	require.True(t, verification.FilesVerified)
	require.Len(t, verification.Digest, 64)

	manifest.Artifacts[0].Path = "../escape"
	_, err = manifest.Validate()
	require.ErrorContains(t, err, "invalid archive artifact")
}

func TestArchiveManifestRejectsMissingKindAndTampering(t *testing.T) {
	root := t.TempDir()
	manifest := validArchiveManifest(t, root)
	manifest.Artifacts = manifest.Artifacts[1:]
	_, err := manifest.Validate()
	require.ErrorContains(t, err, "lacks required binary")

	manifest = validArchiveManifest(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, manifest.Artifacts[0].Path), []byte("tampered"), 0o600))
	_, err = manifest.VerifyFiles(root)
	require.Error(t, err)

	manifest = validArchiveManifest(t, root)
	realPath := filepath.Join(root, manifest.Artifacts[0].Path)
	require.NoError(t, os.Remove(realPath))
	require.NoError(t, os.Symlink(filepath.Join(root, manifest.Artifacts[1].Path), realPath))
	_, err = manifest.VerifyFiles(root)
	require.ErrorContains(t, err, "link")
}
