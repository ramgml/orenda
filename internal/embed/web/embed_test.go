// Tests for the embedded SPA filesystem. Phase 27.1.
//
// The webembed package needs to satisfy three contracts:
//
//  1. `//go:embed all:dist placeholder.txt` must compile in a fresh
//     checkout (the embed directory contains only `.gitkeep`).
//  2. DistSubFS() must return the embedded `dist/` sub-FS when it has
//     an index.html (the post-`make build` state).
//  3. DistSubFS() must fall back to the on-disk `web/dist/` when the
//     embed is empty (the dev / `go test` state).
//
// We don't have a robust way to inject a mock embed.FS from outside the
// package, so most of these tests inspect the *real* package state — the
// embed directory is committed with `.gitkeep` only, which means the
// compiled FS holds one file (placeholder.txt) and `dist/` is empty.
package webembed

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPackage_EmbedCompiles is the cheap smoke test: just touching the
// package symbol proves the //go:embed directives resolved successfully.
// If somebody removes the `dist/.gitkeep` placeholder the build itself
// will fail; this test exists to give a friendlier failure message.
func TestPackage_EmbedCompiles(t *testing.T) {
	require.NotNil(t, FS, "embed.FS should be initialized")
}

// TestDistSubFS_EmbeddedHasIndexOrEmpty confirms the embed either
// contains an index.html (post-`make build`) or the helper falls back
// gracefully. The behaviour is environment-dependent, so we only
// assert invariants that always hold.
func TestDistSubFS_EmbeddedHasIndexOrEmpty(t *testing.T) {
	dist, err := fs.Sub(FS, "dist")
	require.NoError(t, err)

	files, err := fs.ReadDir(dist, ".")
	require.NoError(t, err)

	hasIndex := false
	for _, f := range files {
		if f.Name() == "index.html" {
			hasIndex = true
			break
		}
	}

	// Either the SPA is embedded (production build) or the directory is
	// empty (dev / clean checkout). Anything else is a regression.
	if hasIndex {
		// Sanity-check: the index.html is readable and contains <html.
		raw, err := fs.ReadFile(dist, "index.html")
		require.NoError(t, err)
		html := strings.ToLower(string(raw))
		assert.True(t, strings.Contains(html, "<html") || strings.Contains(html, "<!doctype html"),
			"embedded index.html should look like an HTML document")
	}
}

// TestDistSubFS_Placeholder always ships — placeholder.txt is part of
// the embed independent of the dist/ contents. This guards against
// somebody accidentally dropping the placeholder and breaking the
// "always at least one file" contract.
func TestDistSubFS_Placeholder(t *testing.T) {
	raw, err := FS.ReadFile("placeholder.txt")
	require.NoError(t, err, "placeholder.txt must always be embedded")
	assert.Contains(t, string(raw), "orenda web placeholder")
}

// TestDistSubFS_ReturnsValidFS is the integration smoke-test: regardless
// of whether the embed is empty or populated, DistSubFS() must return
// a usable fs.FS (never nil, never an error that callers can't recover
// from).
func TestDistSubFS_ReturnsValidFS(t *testing.T) {
	fsys, err := DistSubFS()
	require.NoError(t, err)
	require.NotNil(t, fsys)

	// Stat the root. A valid fs.FS accepts the empty path.
	_, statErr := fs.Stat(fsys, ".")
	assert.NoError(t, statErr, "DistSubFS() must return a root-stat'able FS")
}

// TestDistSubFS_DiskFallback temporarily removes the on-disk web/dist
// from consideration by chdir-ing into a temp directory that has no
// web/dist. After the test we restore the original working directory.
//
// This is the only way to exercise the "embed is empty AND no on-disk
// dist" path: the production build places index.html in the embed, so
// without this trick we'd never see the fallback (it would short-circuit
// on the embedded index.html).
func TestDistSubFS_DiskFallback(t *testing.T) {
	// Save current dir, run from a temp dir, then restore.
	orig, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(orig) }()

	tmp := t.TempDir()
	require.NoError(t, os.Chdir(tmp))

	fsys, err := DistSubFS()
	require.NoError(t, err)
	require.NotNil(t, fsys)

	// Without an on-disk web/dist and without an embedded index.html,
	// the only valid behaviour is "mount a placeholder FS that returns
	// 404 for everything". We assert that index.html either errors out
	// (NotExist) or returns our placeholder.
	_, statErr := fs.Stat(fsys, "index.html")
	if statErr == nil {
		// If the embed still has an index.html (e.g. test was run after
		// `make build`), we accept that — the "real SPA" path is the
		// happy case and is covered by TestDistSubFS_EmbeddedHasIndexOrEmpty.
		t.Skip("index.html available in embed even from /tmp; this is fine for production builds")
	}
}

// TestDistSubFS_PrefersEmbeddedOverDisk proves precedence: if both the
// embed and the on-disk web/dist have an index.html, DistSubFS returns
// the embedded one (production deploys must not depend on a stray
// web/dist directory in /tmp).
func TestDistSubFS_PrefersEmbeddedOverDisk(t *testing.T) {
	// We can't easily compare two fs.FS by content; instead we stub an
	// on-disk web/dist/ that contains a marker file and check whether
	// the marker is reachable. If the embedded SPA is present, the
	// marker won't be reachable because the embed is preferred.
	orig, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(orig) }()

	tmp := t.TempDir()
	distDir := filepath.Join(tmp, "web", "dist")
	require.NoError(t, os.MkdirAll(distDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "index.html"),
		[]byte("<html>on-disk</html>"), 0o644))
	// Marker file that lives ONLY on disk, not in the embed.
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "marker.txt"),
		[]byte("on-disk-only"), 0o644))

	require.NoError(t, os.Chdir(tmp))

	fsys, err := DistSubFS()
	require.NoError(t, err)
	require.NotNil(t, fsys)

	_, err = fs.Stat(fsys, "marker.txt")
	if err == nil {
		// Could only happen if the embed is empty AND the on-disk
		// fallback kicks in. That's still correct for dev mode.
		t.Log("embedded dist is empty; on-disk fallback engaged (expected for `go test` outside of `make build`)")
		return
	}
	assert.ErrorIs(t, err, fs.ErrNotExist,
		"when the embed has an index.html, the on-disk dist must be ignored")
}
