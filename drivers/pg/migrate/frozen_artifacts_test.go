package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type frozenSourceManifest struct {
	Version        int    `json:"version"`
	SourceRevision string `json:"source_revision"`
	CapturedAt     string `json:"captured_at"`
	Artifacts      []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"artifacts"`
}

func TestFrozenV1SourceManifestChecksEveryRetainedArtifact(t *testing.T) {
	manifestPath := filepath.Join("testdata", "v1_source_manifest.json")
	content, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest frozenSourceManifest
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&manifest))
	require.Equal(t, 1, manifest.Version)
	require.Equal(t, "bd64d910ef1bfe01b886fa584796275e5c533c8e", manifest.SourceRevision)
	require.NotEmpty(t, manifest.CapturedAt)
	require.Len(t, manifest.Artifacts, 4)

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	seen := map[string]struct{}{}
	for _, artifact := range manifest.Artifacts {
		clean := filepath.Clean(artifact.Path)
		require.False(t, filepath.IsAbs(clean))
		require.NotEqual(t, "..", clean)
		require.False(t, strings.HasPrefix(clean, ".."+string(filepath.Separator)))
		_, duplicate := seen[clean]
		require.False(t, duplicate)
		seen[clean] = struct{}{}
		payload, err := os.ReadFile(filepath.Join(root, clean))
		require.NoError(t, err)
		digest := sha256.Sum256(payload)
		require.Equal(t, artifact.SHA256, hex.EncodeToString(digest[:]), clean)
	}
	require.Contains(t, seen, "drivers/pg/migrate/testdata/v1_schema.sql")
	require.Contains(t, seen, "drivers/pg/migrate/testdata/v1_schema_down.sql")
	require.Contains(t, seen, "drivers/pg/migrate/testdata/v1_client_go.mod")
	require.Contains(t, seen, "drivers/pg/migrate/testdata/v1_export_client.go.txt")
}
