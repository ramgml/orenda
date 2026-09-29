// Internal-package tests for the pg tooling helpers (T366 review round):
// they need access to unexported functions and shell out to real
// pg_restore, so they live inside the package.
package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGRestoreList_SurfacesStderr pins the review fix: a failing
// pg_restore --list must carry the tool's stderr (e.g. the "did not
// find magic string" parser complaint), not an empty "exit status 1:"
// reason — that's what the CLI prints and the HTTP 422 detail shows.
func TestPGRestoreList_SurfacesStderr(t *testing.T) {
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skipf("pg_restore not on PATH (embedded bundle ships server binaries only): %v", err)
	}
	bad := filepath.Join(t.TempDir(), "garbage.dump")
	require.NoError(t, os.WriteFile(bad, []byte("definitely not a pg_dump archive"), 0o644))

	_, err := pgRestoreList(context.Background(), "pg_restore", bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_restore --list")
	assert.Contains(t, err.Error(), "valid archive",
		"stderr must be surfaced so the operator sees WHY the archive is unreadable")
}
