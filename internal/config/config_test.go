package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearORENDAEnv unsets every ORENDA_* variable for the duration of t,
// restoring originals on cleanup. Tests that verify YAML/default values
// (without intending to test env overrides) must call this to stay
// hermetic against the calling shell's environment.
func clearORENDAEnv(t *testing.T) {
	t.Helper()
	saved := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "ORENDA_") {
			saved[k] = v
			t.Setenv(k, "") // t.Setenv sets but can't unset; we override then unset below
			os.Unsetenv(k)
		}
	}
	t.Cleanup(func() {
		for k := range saved {
			os.Unsetenv(k)
		}
		for k, v := range saved {
			os.Setenv(k, v)
		}
	})
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()

	assert.Equal(t, "127.0.0.1", c.Server.Host)
	assert.Equal(t, 2137, c.Server.Port)
	assert.Equal(t, "data/orenda.db", c.Storage.DBPath)
	assert.True(t, c.Storage.WALMode)
	assert.Equal(t, 12, c.Auth.BcryptCost)
	// Phase 28.4: OWASP-aligned default for cookie session TTL.
	// Older 168h lives on any token already issued; new logins
	// get this 24h window. The cookie's Expires in handlers_auth.go
	// is derived from this same value (deps.JWTTTL).
	assert.Equal(t, 24*time.Hour, c.Auth.JWTTTL)
	// Phase 28.6: pprof is off by default — exposing heap /
	// goroutine state on any reachable port is an information
	// leak. Operators flip it on with `server.debug_pprof: true`
	// in config.yaml or `ORENDA_SERVER__DEBUG_PPROF=true`.
	assert.False(t, c.Server.DebugPProf, "pprof must be off by default")
	assert.Equal(t, "127.0.0.1:6060", c.Server.PProfAddr, "pprof defaults to loopback-only")
	assert.Equal(t, "info", c.Logging.Level)
	// Phase 28.8: rate-limit defaults must match the values the
	// router used to inline before this struct existed
	// (anon 60 burst @ 20/s, auth 300 burst @ 100/s). Anything
	// else would be a silent behaviour change for operators
	// who never wrote a rate_limit: section.
	assert.Equal(t, 60, c.RateLimit.AnonBurst)
	assert.Equal(t, 20.0, c.RateLimit.AnonPerSec)
	assert.Equal(t, 300, c.RateLimit.AuthBurst)
	assert.Equal(t, 100.0, c.RateLimit.AuthPerSec)
	require.NoError(t, c.Validate())
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	clearORENDAEnv(t)
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 2137, c.Server.Port)
}

func TestLoad_FromYAMLFile(t *testing.T) {
	clearORENDAEnv(t)
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
	// Phase 28.4: jwt_ttl wasn't in the file → falls back to the
	// 24h default set in DefaultConfig().
	assert.Equal(t, 24*time.Hour, c.Auth.JWTTTL)
}

// Phase 28.4: explicit jwt_ttl in YAML wins over the default.
// Operators who still want a longer cookie session can opt in
// (the spec mentions 168h/7d historically) by setting the value.
func TestLoad_JWTTTLFromYAML(t *testing.T) {
	clearORENDAEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
auth:
  jwt_secret: "x"
  jwt_ttl: "168h"
  cookie_secure: true
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 168*time.Hour, c.Auth.JWTTTL)
	assert.True(t, c.Auth.CookieSecure)
}

// Phase 28.8: rate_limit: section in YAML takes precedence over the
// hard-coded default. Operators who want a strict prod setting
// ("tighten auth to 60/10 for the cluster") bake it here rather
// than relying on env vars at every boot.
func TestLoad_RateLimitFromYAML(t *testing.T) {
	clearORENDAEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
ratelimit:
  anon_burst: 30
  anon_per_sec: 5
  auth_burst: 600
  auth_per_sec: 200
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 30, c.RateLimit.AnonBurst)
	assert.Equal(t, 5.0, c.RateLimit.AnonPerSec)
	assert.Equal(t, 600, c.RateLimit.AuthBurst)
	assert.Equal(t, 200.0, c.RateLimit.AuthPerSec)
}

// Phase 28.8: env override (ORENDA_RATELIMIT__AUTH_BURST) wins over
// yaml. Same env keys the E2E setup uses to crank the limiter so
// Playwright doesn't flake on the rapid /api/v1/board-style
// GETs fired on every page mount.
func TestLoad_RateLimitEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
ratelimit:
  auth_burst: 600
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	t.Setenv("ORENDA_RATELIMIT__AUTH_BURST", "9999")
	t.Setenv("ORENDA_RATELIMIT__ANON_PER_SEC", "1.5")

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 9999, c.RateLimit.AuthBurst, "env wins over yaml")
	assert.Equal(t, 1.5, c.RateLimit.AnonPerSec)
}

func TestLoad_MalformedYAML_ReturnsError(t *testing.T) {
	clearORENDAEnv(t)
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
	t.Setenv("ORENDA_AUTH__JWT_TTL", "12h")
	t.Setenv("ORENDA_AUTH__COOKIE_SECURE", "true")
	t.Setenv("ORENDA_SERVER__DEBUG_PPROF", "true")
	t.Setenv("ORENDA_SERVER__PPROF_ADDR", "127.0.0.1:6061")

	c, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 1234, c.Server.Port)
	assert.Equal(t, "from-env", c.Auth.JWTSecret)
	assert.Equal(t, "warn", c.Logging.Level)
	assert.False(t, c.Storage.WALMode)
	// Phase 28.4: env wins over YAML wins over default.
	assert.Equal(t, 12*time.Hour, c.Auth.JWTTTL)
	assert.True(t, c.Auth.CookieSecure)
	// Phase 28.6: pprof env override (bool + addr).
	assert.True(t, c.Server.DebugPProf)
	assert.Equal(t, "127.0.0.1:6061", c.Server.PProfAddr)
}

func TestLoad_EnvOnly_NoFile(t *testing.T) {
	t.Setenv("ORENDA_SERVER__PORT", "4321")

	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 4321, c.Server.Port)
}

// TestLoad_JWTSecretFile covers the Task 138 credential-file path: the
// secret may come from a file (ORENDA_AUTH__JWT_SECRET_FILE /
// auth.jwt_secret_file) when no direct secret is configured. A direct
// value always wins; a missing or empty file is a hard error from Load.
func TestLoad_JWTSecretFile(t *testing.T) {
	writeSecret := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "jwt")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}
	writeSecretTo := func(t *testing.T, path, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	tests := []struct {
		name       string
		secret     string // ORENDA_AUTH__JWT_SECRET value ("" = unset)
		filePath   string // ORENDA_AUTH__JWT_SECRET_FILE value ("" = unset, "SET" = create file below)
		fileData   string // content for the created file (when filePath == "SET")
		credDir    string // CREDENTIALS_DIRECTORY value ("" = unset, "SET" = fresh temp dir)
		credFile   string // "SET" = create jwt inside the credentials dir
		credData   string // content for the credentials jwt file (when credFile == "SET")
		wantSecret string
		wantErr    string
	}{
		{
			name:       "secret read from file",
			filePath:   "SET",
			fileData:   "file-secret\n",
			wantSecret: "file-secret",
		},
		{
			name:       "direct secret wins, file never read",
			secret:     "direct-secret",
			filePath:   "/nonexistent/t138/jwt-does-not-exist",
			wantSecret: "direct-secret",
		},
		{
			name:     "missing file is an error",
			filePath: "/nonexistent/t138/jwt-does-not-exist",
			wantErr:  "auth.jwt_secret_file",
		},
		{
			name:     "whitespace-only file is an error",
			filePath: "SET",
			fileData: "   \n\t ",
			wantErr:  "is empty",
		},
		{
			name:       "systemd credentials directory supplies the secret",
			credFile:   "SET",
			credData:   "cred-secret\n",
			wantSecret: "cred-secret",
		},
		{
			name:    "systemd credential jwt missing is an error",
			credDir: "SET", // empty dir: no jwt file inside
			wantErr: "systemd credential jwt",
		},
		{
			name:     "systemd credential jwt empty is an error",
			credFile: "SET",
			credData: " \n\t ",
			wantErr:  "is empty",
		},
		{
			name:       "jwt_secret_file wins over systemd credentials directory",
			filePath:   "SET",
			fileData:   "file-secret\n",
			credFile:   "SET",
			credData:   "cred-secret\n",
			wantSecret: "file-secret",
		},
		{
			name:       "nothing set keeps legacy contract",
			wantSecret: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearORENDAEnv(t)
			if tc.secret != "" {
				t.Setenv("ORENDA_AUTH__JWT_SECRET", tc.secret)
			}
			if tc.filePath != "" {
				path := tc.filePath
				if path == "SET" {
					path = writeSecret(t, tc.fileData)
				}
				t.Setenv("ORENDA_AUTH__JWT_SECRET_FILE", path)
			}
			// CREDENTIALS_DIRECTORY is not ORENDA_-prefixed, so
			// clearORENDAEnv leaves it alone — set/unset explicitly
			// in every case via t.Setenv.
			if tc.credDir != "" || tc.credFile != "" {
				dir := tc.credDir
				if dir == "SET" || tc.credFile == "SET" {
					dir = t.TempDir()
				}
				t.Setenv("CREDENTIALS_DIRECTORY", dir)
				if tc.credFile == "SET" {
					writeSecretTo(t, filepath.Join(dir, "jwt"), tc.credData)
				}
			} else {
				t.Setenv("CREDENTIALS_DIRECTORY", "")
			}

			c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSecret, c.Auth.JWTSecret)
		})
	}
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
