package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/ramgml/orenda/internal/auth"
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

// runUserListCLI drives `orenda user list` and returns the rendered
// tabwriter output.
func runUserListCLI(t *testing.T, cfgPath string) (string, error) {
	t.Helper()
	cmd := newUserListCmd()
	cmd.Flags().String("config", "", "config path (test only)")
	cmd.SetArgs([]string{"--config", cfgPath})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

func TestRunUserList_Empty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	out, err := runUserListCLI(t, cfgPath)
	require.NoError(t, err)
	// Fresh migrations always seed the system-inbox placeholder, so the
	// list can never be literally empty. We assert the seed row is
	// shown instead.
	assert.Contains(t, out, "system-inbox@orenda.local")
	assert.Contains(t, out, "system")
}

func TestRunUserList_OneUser(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"hunter2!\n"))

	out, err := runUserListCLI(t, cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "alice@example.com")
	assert.Contains(t, out, "Alice")
	assert.Contains(t, out, "owner")
}

// runUserResetPasswordCLI drives `orenda user reset-password` with a
// known stdin so we can prove "wrong password then right password"
// actually moves the bcrypt hash.
func runUserResetPasswordCLI(t *testing.T, cfgPath string, args []string, stdin string) error {
	t.Helper()
	cmd := newUserResetPasswordCmd()
	cmd.Flags().String("config", "", "config path (test only)")
	cmd.SetArgs(append([]string{"--config", cfgPath, "--password-stdin"}, args...))
	cmd.SetIn(stringReader(stdin))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.ExecuteContext(context.Background())
}

func TestRunUserResetPassword_Success(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"original-pw\n"))

	// Confirm the old password works.
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	repo := sqlite.NewUserRepository(db)
	before, err := repo.GetByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, auth.VerifyPassword(before.PasswordHash, "original-pw"),
		"original password must verify before reset")

	// Reset via the CLI.
	require.NoError(t, runUserResetPasswordCLI(t, cfgPath,
		[]string{"--email=alice@example.com"}, "rotated-pw!!\n"))

	// Old password must now fail; new one must succeed.
	after, err := repo.GetByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, before.PasswordHash, after.PasswordHash, "hash must rotate")
	assert.Error(t, auth.VerifyPassword(after.PasswordHash, "original-pw"),
		"old password must no longer verify")
	assert.NoError(t, auth.VerifyPassword(after.PasswordHash, "rotated-pw!!"),
		"new password must verify")
}

func TestRunUserResetPassword_ShortPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"original-pw\n"))

	err := runUserResetPasswordCLI(t, cfgPath,
		[]string{"--email=alice@example.com"}, "short\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8 characters")
}

func TestRunUserResetPassword_UnknownEmail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"original-pw\n"))

	err := runUserResetPasswordCLI(t, cfgPath,
		[]string{"--email=ghost@example.com"}, "any-pass-w\n")
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no user"),
		"expected not-found error, got %q", err.Error(),
	)
}

func TestRunUserResetPassword_AutoPickSingleUser(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=solo@example.com", "--display-name=Solo"},
		"original-pw\n"))

	// Omit --email; the CLI must auto-pick the single user.
	require.NoError(t, runUserResetPasswordCLI(t, cfgPath, nil, "rotated-pw!!\n"))

	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	repo := sqlite.NewUserRepository(db)
	u, err := repo.GetByEmail(context.Background(), "solo@example.com")
	require.NoError(t, err)
	assert.NoError(t, auth.VerifyPassword(u.PasswordHash, "rotated-pw!!"))
}
