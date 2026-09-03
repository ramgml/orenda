// Package api — Task 122: test-visible accessor for the embedded spec.
//
// Go refuses //go:embed directives inside _test.go files, so the
// embedded-copy drift gate (openapi_sync_test.go) reads the spec
// through this accessor instead of embedding the file itself.
package api

// OpenAPIEmbeddedSpec returns the raw bytes of internal/api/openapi.yaml
// exactly as embedded into the binary (and served verbatim at
// /api/v1/openapi.yaml). Exported for the Task 122 drift gate.
func OpenAPIEmbeddedSpec() []byte {
	return openAPISpec
}
