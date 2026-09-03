// Package api — Task 122: embedded-copy gate.
//
// internal/api/openapi.yaml (served at /api/v1/openapi.yaml via
// //go:embed) and docs/openapi.yaml (the canonical spec the
// route-coverage tests validate) are two committed copies of one
// document. Task 115 (PR #150) synced them by hand and the copies
// drifted again; this test makes the drift fail loudly instead,
// pointing at the one-command resync (make openapi-sync).
//
// Go refuses //go:embed directives inside _test.go files, so the
// embedded bytes are re-exported by openapi.go (OpenAPIEmbeddedSpec)
// next to the embed in handler_openapi.go.
package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ramgml/orenda/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoDocsOpenAPIPath resolves docs/openapi.yaml relative to this
// file's location (internal/api/), independent of the go test cwd.
func repoDocsOpenAPIPath(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	// thisFile = <repo>/internal/api/openapi_sync_test.go
	// docs copy = <repo>/docs/openapi.yaml
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "docs", "openapi.yaml")
}

// TestOpenAPI_EmbeddedCopyMatchesDocs is the drift gate (Task 122).
//
// The binary serves the embedded copy verbatim; docs/openapi.yaml is
// canonical (the route-coverage tests keep validating it). The two
// files must stay byte-identical: edit docs/openapi.yaml, then run
// make openapi-sync.
func TestOpenAPI_EmbeddedCopyMatchesDocs(t *testing.T) {
	t.Parallel()

	docsPath := repoDocsOpenAPIPath(t)
	docsRaw, err := os.ReadFile(docsPath)
	require.NoError(t, err, "docs/openapi.yaml must exist next to the repo")

	if !assert.Equal(t, string(docsRaw), string(api.OpenAPIEmbeddedSpec()),
		"internal/api/openapi.yaml drifted from docs/openapi.yaml") {
		t.Logf(`embedded openapi.yaml (%s) differs from docs/openapi.yaml (%s).

The binary serves the embedded copy, so it is serving a stale contract.

Resync in one command:

    make openapi-sync

(docs/openapi.yaml is canonical: edit it, then run the make target.)`,
			filepath.Join("internal", "api", "openapi.yaml"), docsPath)
	}
}
