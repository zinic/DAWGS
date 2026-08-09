package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteReportAtomicallyCreatesMode0600JSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := Report{Version: 1, Action: "status", State: "v1", CreatedAt: time.Unix(1, 0).UTC()}
	require.NoError(t, WriteReport(path, report))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(content, &decoded))
	require.Equal(t, report, decoded)
}

func TestWriteReportReplacesExistingReportWithoutWeakeningMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	require.NoError(t, WriteReport(path, Report{Version: 1, Action: "health", State: "drifted", CreatedAt: time.Unix(2, 0).UTC()}))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
