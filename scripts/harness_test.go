//go:build pg_harness

package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requirePostgresHarness(t *testing.T) {
	t.Helper()
	for _, name := range []string{"postgres", "initdb", "pg_ctl", "createdb", "psql", "flock", "python3"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is unavailable", name)
		}
	}
}

func harnessCommand(t *testing.T, root string, args ...string) *exec.Cmd {
	t.Helper()
	command := exec.Command("./test-postgres", args...)
	command.Env = append(os.Environ(), "DAWGS_TEST_POSTGRES_ROOT="+root)
	return command
}

func TestPGHarnessPreservesChildFailureAndCleansOwnedInstance(t *testing.T) {
	requirePostgresHarness(t)
	root := filepath.Join(t.TempDir(), "clusters")
	command := harnessCommand(t, root, "run", "failure", "--", "sh", "-c", "exit 37")
	err := command.Run()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, 37, exitError.ExitCode())
	require.NoDirExists(t, filepath.Join(root, "failure"))
}

func TestPGHarnessSignalCleansOwnedInstance(t *testing.T) {
	requirePostgresHarness(t)
	root := filepath.Join(t.TempDir(), "clusters")
	command := harnessCommand(t, root, "run", "signal", "--", "sh", "-c", "while :; do :; done")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, command.Start())
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(filepath.Join(root, "signal", "state"))
		return err == nil && strings.Contains(string(content), "status=running")
	}, 20*time.Second, 50*time.Millisecond)
	require.NoError(t, syscall.Kill(-command.Process.Pid, syscall.SIGTERM))
	err := command.Wait()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, 143, exitError.ExitCode())
	require.NoDirExists(t, filepath.Join(root, "signal"))
}

func TestPGHarnessPreservesPreexistingNamedInstanceAndRecordsState(t *testing.T) {
	requirePostgresHarness(t)
	root := filepath.Join(t.TempDir(), "clusters")
	up := harnessCommand(t, root, "up", "existing")
	require.NoError(t, up.Run())
	t.Cleanup(func() { _ = harnessCommand(t, root, "down", "existing").Run() })
	statePath := filepath.Join(root, "existing", "state")
	info, err := os.Stat(statePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	state, err := os.ReadFile(statePath)
	require.NoError(t, err)
	for _, key := range []string{"nonce=", "data_directory=", "port=", "server_version_num=", "profile=fast-read", "fsync=off", "synchronous_commit=off", "full_page_writes=off", "wal_level=", "wal_compression=", "checkpoint_timeout=", "checkpoint_completion_target=", "min_wal_size=", "max_wal_size=", "status=running"} {
		require.Contains(t, string(state), key)
	}

	run := harnessCommand(t, root, "run", "existing", "--", "sh", "-c", "test -n \"$CONNECTION_STRING\"")
	require.NoError(t, run.Run())
	require.DirExists(t, filepath.Join(root, "existing", "data"))
	require.NoError(t, harnessCommand(t, root, "connection", "existing").Run())
}

func TestPGHarnessDurableProfileRecordsAndExportsLiveSettings(t *testing.T) {
	requirePostgresHarness(t)
	root := filepath.Join(t.TempDir(), "clusters")
	command := harnessCommand(t, root, "--profile", "durable", "run", "durable", "--", "sh", "-c", `
test "$DAWGS_TEST_POSTGRES_PROFILE" = durable
test "$(psql "$CONNECTION_STRING" -XAtqc 'show fsync')" = on
test "$(psql "$CONNECTION_STRING" -XAtqc 'show synchronous_commit')" = on
test "$(psql "$CONNECTION_STRING" -XAtqc 'show full_page_writes')" = on
`)
	require.NoError(t, command.Run())
	require.NoDirExists(t, filepath.Join(root, "durable"))
}

func TestPGHarnessRejectsUnknownProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "clusters")
	err := harnessCommand(t, root, "--profile", "unsafe", "help").Run()
	require.Error(t, err)
}

func TestPGHarnessExportsServerLogBeforeTeardown(t *testing.T) {
	requirePostgresHarness(t)
	base := t.TempDir()
	root := filepath.Join(base, "clusters")
	logPath := filepath.Join(base, "postgres.sanitized.log")
	command := harnessCommand(t, root, "--log-output", logPath, "run", "log-export", "--", "sh", "-c", `psql "$CONNECTION_STRING" -XAtqc 'select 1' >/dev/null`)
	require.NoError(t, command.Run())
	require.FileExists(t, logPath)
	info, err := os.Stat(logPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoDirExists(t, filepath.Join(root, "log-export"))
}

func TestPGHarnessRejectsSymlinkRuntimeRoot(t *testing.T) {
	requirePostgresHarness(t)
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(realRoot, 0o700))
	symlinkRoot := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realRoot, symlinkRoot))
	err := harnessCommand(t, symlinkRoot, "help").Run()
	require.Error(t, err)
}

func TestPGHarnessRoleCoordinatorCleansDatabaseAndRoles(t *testing.T) {
	requirePostgresHarness(t)
	root := filepath.Join(t.TempDir(), "clusters")
	command := harnessCommand(t, root, "run", "roles", "--", "./prepare-test-pg", "--", "sh", "-c", `
test "$PG_TEST_OPERATOR_ROLE" != "$PG_TEST_TARGET_ROLE"
test "$PG_TEST_TARGET_ROLE" != "$PG_TEST_OLD_WRITER_ROLE"
psql "$CONNECTION_STRING" -XAtqc 'select session_user=current_user and not r.rolsuper from pg_catalog.pg_roles r where r.rolname=session_user' | grep -qx t
psql "$PG_TEST_TARGET_CONNECTION_STRING" -XAtqc 'select session_user=current_user and not r.rolsuper from pg_catalog.pg_roles r where r.rolname=session_user' | grep -qx t
psql "$PG_TEST_ADMIN_CONNECTION_STRING" -XAtqc "select not rolcanlogin and rolpassword is null from pg_catalog.pg_authid where rolname='$PG_TEST_PREPARATION_ROLE'" | grep -qx t
`)
	require.NoError(t, command.Run())
	require.NoDirExists(t, filepath.Join(root, "roles"))
}
