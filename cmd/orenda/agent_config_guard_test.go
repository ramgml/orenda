package main

// Task 181: the project-local .orenda/agent.yaml carries a plaintext
// token, so the resolver guards how git sees the file. These tests
// build REAL git repositories in t.TempDir() (init + commit identity)
// because the guard shells out to git itself — mocked exits would
// only re-assert the mock.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// skipIfNoGit guards tests that need the git binary — CI images
// without it would otherwise fail for the wrong reason.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// gitRun executes a git command in dir (empty dir = process cwd) and
// requires success — the fixtures depend on the repo state they build.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	skipIfNoGit(t)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// initGuardRepo creates a real git repository with commit identity
// configured locally. Returns the repo path; the caller chdirs.
func initGuardRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "-c", "init.defaultBranch=main", "init")
	gitRun(t, dir, "config", "user.email", "guard@example.com")
	gitRun(t, dir, "config", "user.name", "Guard Test")
	return dir
}

// writeGuardConfig drops a populated .orenda/agent.yaml into the
// current working directory with an exact file mode (Chmod because
// WriteFile applies the process umask).
func writeGuardConfig(t *testing.T, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(".orenda", 0o755))
	raw, err := yaml.Marshal(agentConfig{URL: "http://guard-local", Token: "tok-guard"})
	require.NoError(t, err)
	path := filepath.Join(".orenda", "agent.yaml")
	require.NoError(t, os.WriteFile(path, raw, mode))
	require.NoError(t, os.Chmod(path, mode))
}

// resolveGuardSettings runs resolveAgentSettings against a fresh
// command tree in the caller's working directory with env cleared
// (so both fields must come from the local config) and returns the
// stderr text.
func resolveGuardSettings(t *testing.T) (*agentSettings, string) {
	t.Helper()
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	root := newAgentCmd()
	var errOut strings.Builder
	root.SetErr(&errOut)
	require.NoError(t, root.ParseFlags(nil))
	s, err := resolveAgentSettings(root, "orenda agent")
	require.NoError(t, err)
	return s, errOut.String()
}

// TestResolveAgentSettings_GitGuard walks the four git states of the
// project-local config: ignored → silence, tracked / not gitignored →
// a one-line warning, non-repo → silence (the guard must never break
// an arbitrary checkout). Each warning fires exactly once per run —
// the two return paths of resolveAgentSettings share one call site.
func TestResolveAgentSettings_GitGuard(t *testing.T) {
	tests := []struct {
		name       string
		needsGit   bool
		makeRepo   func(t *testing.T)
		wantSubstr string // empty = stderr must stay silent
	}{
		{
			name:     "ignored",
			needsGit: true,
			makeRepo: func(t *testing.T) {
				t.Chdir(initGuardRepo(t))
				require.NoError(t, os.WriteFile(".gitignore", []byte(".orenda/\n"), 0o644))
				writeGuardConfig(t, 0o600)
			},
		},
		{
			name:     "unignored",
			needsGit: true,
			makeRepo: func(t *testing.T) {
				t.Chdir(initGuardRepo(t))
				writeGuardConfig(t, 0o600)
			},
			wantSubstr: "warning: .orenda/agent.yaml is not gitignored — 'git add .' would commit the plaintext token; add '.orenda/' to .gitignore",
		},
		{
			name:     "tracked",
			needsGit: true,
			makeRepo: func(t *testing.T) {
				t.Chdir(initGuardRepo(t))
				writeGuardConfig(t, 0o600)
				gitRun(t, "", "add", ".orenda/agent.yaml")
				gitRun(t, "", "commit", "-m", "leak the token")
			},
			wantSubstr: "warning: .orenda/agent.yaml is tracked by git — the token will be committed to history; remove it from the index (git rm --cached) and rotate the token",
		},
		{
			name: "no-git",
			makeRepo: func(t *testing.T) {
				// Plain temp dir, no repository: git exits with a
				// fatal error and the guard stays silent.
				t.Chdir(t.TempDir())
				writeGuardConfig(t, 0o600)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsGit {
				skipIfNoGit(t)
			}
			tt.makeRepo(t)
			s, stderr := resolveGuardSettings(t)
			assert.Equal(t, sourceLocal, s.URL.Source)
			assert.Equal(t, sourceLocal, s.Token.Source)
			if tt.wantSubstr == "" {
				assert.Empty(t, stderr)
				return
			}
			assert.Contains(t, stderr, tt.wantSubstr)
			assert.Equal(t, 1, strings.Count(stderr, "warning:"),
				"exactly one warning per resolve run, got: %s", stderr)
		})
	}
}

// TestResolveAgentSettings_GitGuardPerms: the loose-mode warning is
// independent of the git state — a 0644 token file warns even when
// gitignored, 0600 stays quiet. Windows filesystems do not enforce
// POSIX mode bits, so the check degrades there.
func TestResolveAgentSettings_GitGuardPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not enforced on windows")
	}
	for _, tt := range []struct {
		name     string
		mode     os.FileMode
		wantPerm string // empty = stderr must stay silent
	}{
		{name: "perm 0600 silent", mode: 0o600},
		{name: "perm 0644 warns", mode: 0o644, wantPerm: "chmod 600"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			skipIfNoGit(t)
			t.Chdir(initGuardRepo(t))
			require.NoError(t, os.WriteFile(".gitignore", []byte(".orenda/\n"), 0o644))
			writeGuardConfig(t, tt.mode)
			_, stderr := resolveGuardSettings(t)
			if tt.wantPerm == "" {
				assert.Empty(t, stderr)
				return
			}
			assert.Contains(t, stderr, "warning: .orenda/agent.yaml is readable by other users (perm 644)")
			assert.Contains(t, stderr, tt.wantPerm)
			assert.Equal(t, 1, strings.Count(stderr, "warning:"),
				"exactly one warning per resolve run, got: %s", stderr)
		})
	}
}
