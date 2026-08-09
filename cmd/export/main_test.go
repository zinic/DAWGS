package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCountingWriterCountsOnlyWrittenBytes(t *testing.T) {
	var target bytes.Buffer
	writer := countingWriter{writer: &target}

	written, err := writer.Write([]byte("export"))
	require.NoError(t, err)
	require.Equal(t, 6, written)
	require.EqualValues(t, 6, writer.count)
	require.Equal(t, "export", target.String())
}

func TestWriteExportMetadataIsPrivateAtomicAndNoReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.metadata.json")
	metadata := exportMetadata{Version: 1, Format: "test", GeneratedAt: time.Unix(0, 0).UTC(), Artifact: "export.json", ArtifactSHA256: "digest"}

	require.NoError(t, writeExportMetadata(path, metadata))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), `"format": "test"`)
	require.Contains(t, string(content), `"artifact_sha256": "digest"`)

	require.Error(t, writeExportMetadata(path, exportMetadata{Version: 2}))
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, unchanged)
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".dawgs-export-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, matches)
}
