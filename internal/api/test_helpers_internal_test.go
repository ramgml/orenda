package api

// Shared test infrastructure for the api package (white-box tests).
//
// Fixture databases come from testutil.MatrixDB: the active matrix
// driver decides between the sqlite template copy and the postgres
// template clone (T364).

import (
	"database/sql"
	"testing"

	"github.com/ramgml/orenda/internal/testutil"
)

// copyInternalTemplateDB returns the fixture database for the active
// matrix driver (sqlite template copy or postgres template clone —
// T364). The caller must not close it; cleanup is registered.
func copyInternalTemplateDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.MatrixDB(t)
}
