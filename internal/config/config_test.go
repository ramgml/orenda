package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()

	assert.Equal(t, "127.0.0.1", c.Server.Host)
	assert.Equal(t, 2137, c.Server.Port)
	assert.Equal(t, "data/orenda.db", c.Storage.DBPath)
	assert.True(t, c.Storage.WALMode)
	assert.Equal(t, 12, c.Auth.BcryptCost)
	assert.Equal(t, "info", c.Logging.Level)
	require.NoError(t, c.Validate())
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 2137, c.Server.Port)
}

func TestLoad_FromYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  host: "0.0.0.0"
  port: 9000
  read_timeout: "5s"
auth:
  jwt_secret: "from-file"
  bcrypt_cost: 8
logging:
  level: "debug"
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0", c.Server.Host)
	assert.Equal(t, 9000, c.Server.Port)
	assert.Equal(t, 5*time.Second, c.Server.ReadTimeout)
	assert.Equal(t, "from-file", c.Auth.JWTSecret)
	assert.Equal(t, 8, c.Auth.BcryptCost)
	assert.Equal(t, "debug", c.Logging.Level)
	// Defaults preserved for unspecified fields.
	assert.True(t, c.Storage.WALMode)
}

func TestLoad_MalformedYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server: : :"), 0o600))

	_, err := Load(path)
	require.Error(t, err)
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 9000\n"), 0o600))

	t.Setenv("ORENDA_SERVER__PORT", "1234")
	t.Setenv("ORENDA_AUTH__JWT_SECRET", "from-env")
	t.Setenv("ORENDA_LOGGING__LEVEL", "warn")
	t.Setenv("ORENDA_STORAGE__WAL_MODE", "false")

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 1234, c.Server.Port)
	assert.Equal(t, "from-env", c.Auth.JWTSecret)
	assert.Equal(t, "warn", c.Logging.Level)
	assert.False(t, c.Storage.WALMode)
}

func TestLoad_EnvOnly_NoFile(t *testing.T) {
	t.Setenv("ORENDA_SERVER__PORT", "4321")

	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 4321, c.Server.Port)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{
			name:    "valid defaults",
			mutate:  func(c *Config) {},
			wantErr: "",
		},
		{
			name:    "port too high",
			mutate:  func(c *Config) { c.Server.Port = 100_000 },
			wantErr: "server.port out of range",
		},
		{
			name:    "port zero",
			mutate:  func(c *Config) { c.Server.Port = 0 },
			wantErr: "server.port out of range",
		},
		{
			name:    "empty db path",
			mutate:  func(c *Config) { c.Storage.DBPath = "" },
			wantErr: "storage.db_path is required",
		},
		{
			name:    "bcrypt cost too low",
			mutate:  func(c *Config) { c.Auth.BcryptCost = 2 },
			wantErr: "auth.bcrypt_cost out of range",
		},
		{
			name:    "bcrypt cost too high",
			mutate:  func(c *Config) { c.Auth.BcryptCost = 40 },
			wantErr: "auth.bcrypt_cost out of range",
		},
		{
			name:    "invalid log level",
			mutate:  func(c *Config) { c.Logging.Level = "verbose" },
			wantErr: "logging.level invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestResolveDataDir(t *testing.T) {
	c := DefaultConfig()
	c.Storage.DataDir = "data"
	assert.Equal(t, filepath.Join("/tmp", "data"), c.ResolveDataDir("/tmp"))

	c.Storage.DataDir = "/var/lib/orenda"
	assert.Equal(t, "/var/lib/orenda", c.ResolveDataDir("/tmp"))
}
