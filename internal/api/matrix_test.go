package api

import (
	"os"
	"testing"

	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

// TestMain runs the api package's suite (white-box and black-box test
// files share one test binary) under the two-driver matrix (T364):
// once per driver from ORENDA_TEST_DRIVERS (default sqlite+postgres).
// Fixture databases opened via testutil.MatrixDB follow the active
// driver.
func TestMain(m *testing.M) {
	os.Exit(pgtest.DriverMatrix(m))
}
