package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// writeConfig writes cfg to path in YAML form.
func writeConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, yaml.NewEncoder(f).Encode(cfg))
}

// stringReader returns an io.Reader for the given string.
func stringReader(s string) io.Reader { return &stringReaderImpl{s: s} }

type stringReaderImpl struct {
	s   string
	pos int
}

func (r *stringReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}

// runUserCreateCLI drives the user-create command directly with a custom
// stdin reader. We bypass the root command to keep --config local to the
// test (root's persistent flags aren't visible from the leaf without going
// through Execute).
func runUserCreateCLI(t *testing.T, cfgPath string, args []string, stdin string) error {
	t.Helper()
	cmd := newUserCreateCmd()
	// Register --config locally so the user-create command can read it
	// from cmd.Flags().GetString("config").
	cmd.Flags().String("config", "", "config path (test only)")
	cmd.SetArgs(append([]string{"--config", cfgPath, "--password-stdin"}, args...))
	cmd.SetIn(stringReader(stdin))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.ExecuteContext(context.Background())
}

func TestRunUserCreate_Success(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	err := runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"hunter2!\n")
	require.NoError(t, err)

	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	repo := sqlite.NewUserRepository(db)
	u, err := repo.GetByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Alice", u.DisplayName)
	assert.Equal(t, user.RoleOwner, u.Role)
	assert.False(t, u.CreatedAt.IsZero())
}

func TestRunUserCreate_DuplicateEmail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	args := []string{"--email=alice@example.com", "--display-name=Alice"}
	require.NoError(t, runUserCreateCLI(t, cfgPath, args, "hunter2!\n"))
	err := runUserCreateCLI(t, cfgPath, args, "hunter2!\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already")
}

func TestRunUserCreate_ShortPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	err := runUserCreateCLI(t, cfgPath,
		[]string{"--email=a@b.c", "--display-name=Alice"},
		"short\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8 characters")
}

func TestRunUserCreate_MissingEmail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	err := runUserCreateCLI(t, cfgPath,
		[]string{"--display-name=Alice"},
		"hunter2!\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}
