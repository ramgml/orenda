package main

import (
	"os"
	"testing"
)

// TestT97GitignoreAnchor guards the regression behind task T97: the bare
// `orenda` .gitignore pattern silently ignored every new untracked file
// under cmd/orenda/, so plain `git add -A` skipped new sources here.
// The ignore rule is now root-anchored (/orenda, for the build binary
// that `bin/` already covers); this file exists to prove a new source
// under cmd/orenda/ lands in commits via plain git add -A.
func TestT97GitignoreAnchor(t *testing.T) {
	if os.Getenv("T97") == "" {
		t.Skip("smoke file: new files under cmd/orenda/ are staged by plain git add -A (task T97)")
	}
}
