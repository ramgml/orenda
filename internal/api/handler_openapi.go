// Package api — Phase 24: serve docs/openapi.yaml at
// /api/v1/openapi.yaml so external agents can fetch the
// machine-readable contract without committing the file.
//
// The endpoint is public (no auth) — the spec is intentionally
// non-secret and matches docs/API.md. We mount it on the top-level
// router rather than the user/agent namespace because both kinds
// of consumers want it.
package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var openAPISpec []byte

// openAPIHandler streams the embedded OpenAPI document. We embed
// it at compile time so the binary doesn't need to read a file
// off disk at request time, and so the route-coverage test always
// sees the same bytes the wire sees.
func openAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_, _ = w.Write(openAPISpec)
	}
}
