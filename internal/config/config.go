// Package config loads Orenda server configuration from a YAML file
// with overrides from environment variables.
//
// Configuration is layered:
//
//  1. Built-in defaults (DefaultConfig).
//  2. YAML file specified by --config flag (or defaultConfigPath).
//  3. Environment variables prefixed with ORENDA_, e.g. ORENDA_SERVER__PORT.
//
// Environment overrides use the double-underscore "__" as a section delimiter:
// ORENDA_SERVER__PORT=3000 overrides server.port.
// ORENDA_AUTH__JWT_SECRET=secret overrides auth.jwt_secret.
// ORENDA_AUTH__JWT_SECRET_FILE=/path/to/file overrides auth.jwt_secret_file:
// the named file holds the secret (trimmed), so the secret itself never
// appears in /proc/*/environ.
//
// The JWT secret resolution order in Load: direct auth.jwt_secret value,
// then auth.jwt_secret_file, then the systemd credential
// $CREDENTIALS_DIRECTORY/jwt (mounted by LoadCredential=jwt:...); empty
// remains legal — serve refuses to start with an explicit error.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// envPrefix is the prefix for environment variables that override config values.
const envPrefix = "ORENDA_"

// defaultConfigPath is the conventional location of the config file relative
// to the working directory when no --config flag is supplied.
const defaultConfigPath = "data/config.yaml"

// Config is the top-level Orenda server configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Auth      AuthConfig      `yaml:"auth"`
	Logging   LoggingConfig   `yaml:"logging"`
	Backup    BackupConfig    `yaml:"backup"`
	Bots      []BotConfig     `yaml:"bots"`
	Uploads   UploadsConfig   `yaml:"uploads"`
	RateLimit RateLimitConfig `yaml:"ratelimit"`
	// Phase 30.5: weekly digest scheduler config. DigestInterval
	// <= 0 disables the scheduler entirely.
	Notifier NotifierConfig `yaml:"notifier"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`

	// Phase 28.6: opt-in pprof listener for live debugging. Off by
	// default — the pprof endpoints expose memory and goroutine state
	// that are an information leak on any reachable port. When on,
	// a SECOND listener starts at PProfAddr (default 127.0.0.1:6060)
	// bound to http.DefaultServeMux (net/http/pprof registers itself
	// there on import). The listener is loopback-only by design; an
	// operator who wants remote profiling should set up an ssh
	// tunnel rather than bind 0.0.0.0.
	DebugPProf bool   `yaml:"debug_pprof"`
	PProfAddr  string `yaml:"pprof_addr"`
}

// StorageConfig controls the SQLite database.
type StorageConfig struct {
	DataDir       string `yaml:"data_dir"`
	DBPath        string `yaml:"db_path"`
	WALMode       bool   `yaml:"wal_mode"`
	BusyTimeoutMs int    `yaml:"busy_timeout_ms"`
	EnableForeign bool   `yaml:"enable_foreign_keys"`
}

// AuthConfig controls authentication parameters.
//
// JWTSecret comes from (in priority order) auth.jwt_secret
// (ORENDA_AUTH__JWT_SECRET), the file named by auth.jwt_secret_file
// (ORENDA_AUTH__JWT_SECRET_FILE), or the systemd credential
// $CREDENTIALS_DIRECTORY/jwt mounted by LoadCredential=jwt:... . Reading
// the secret from a file or credential keeps it out of /proc/*/environ;
// an earlier, more direct source always wins.
type AuthConfig struct {
	JWTSecret     string        `yaml:"jwt_secret"`
	JWTSecretFile string        `yaml:"jwt_secret_file"`
	JWTTTL        time.Duration `yaml:"jwt_ttl"`
	CookieName    string        `yaml:"cookie_name"`
	CookieSecure  bool          `yaml:"cookie_secure"`
	BcryptCost    int           `yaml:"bcrypt_cost"`
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Path   string `yaml:"path"`
}

// BackupConfig controls periodic backup behaviour.
//
// In Phase 0 only the file-system paths are wired; the scheduler and git client
// arrive in Phase 7.
type BackupConfig struct {
	Enabled              bool          `yaml:"enabled"`
	RemoteURL            string        `yaml:"remote_url"`
	RemoteAuth           string        `yaml:"remote_auth"`
	MirrorDir            string        `yaml:"mirror_dir"`
	SnapshotDir          string        `yaml:"snapshot_dir"`
	SnapshotRotationDays int           `yaml:"snapshot_rotation_days"`
	GitPushInterval      time.Duration `yaml:"git_push_interval"`
	SQLiteSnapshotCron   string        `yaml:"sqlite_snapshot_cron"`
	WALArchiveInterval   time.Duration `yaml:"wal_archive_interval"`
}

// RateLimitConfig controls the Phase 9.6 token-bucket rate limiter
// (internal/api/ratelimit.go). Both buckets live in-process and
// reset on restart — fine for a single-binary single-user
// install; cross-instance coordination would need Redis (out
// of scope today).
//
// Defaults applied by DefaultConfig() match what the router used
// to inline before this struct existed: anon 60 burst @ 20/s,
// authenticated 300 burst @ 100/s. E2E bumps these via env
// override (ORENDA_RATELIMIT_AUTH_BURST=1M, see
// web/e2e-setup/run-server.sh) — that env-override path is kept
// working by this struct; env > YAML > hard-coded default in
// the router. Operators who want to bake a setting into the
// install flip the YAML instead.
type RateLimitConfig struct {
	AnonBurst  int     `yaml:"anon_burst"`
	AnonPerSec float64 `yaml:"anon_per_sec"`
	AuthBurst  int     `yaml:"auth_burst"`
	AuthPerSec float64 `yaml:"auth_per_sec"`
}

// BotConfig is one entry of the `bots:` section (Phase 10 pluggable
// bots — console/telegram/vk/email/webhook, all shipped).
type BotConfig struct {
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"`
	Config  map[string]any `yaml:"config"`
	Enabled bool           `yaml:"enabled"`
}

// NotifierConfig tunes the Phase 30.5 weekly digest scheduler.
// DigestInterval <= 0 disables it entirely; default is 168h (7
// days). The scheduler fires once at startup then on each tick —
// no exponential backoff, no jitter; the workload is light (six
// scalar COUNT queries per active owner).
type NotifierConfig struct {
	DigestInterval time.Duration `yaml:"digest_interval"`
}

// UploadsConfig controls file attachments.
type UploadsConfig struct {
	Dir          string   `yaml:"dir"`
	MaxSizeMB    int      `yaml:"max_size_mb"`
	AllowedMimes []string `yaml:"allowed_mimes"`
}

// DefaultConfig returns safe built-in defaults.
//
// Tests and tooling that don't need a real config file can call this directly.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:            "127.0.0.1",
			Port:            2137,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			// Phase 28.6: pprof off by default. Loopback-only
			// address so a misconfigured operator can never
			// accidentally expose the heap profile to the
			// network.
			DebugPProf: false,
			PProfAddr:  "127.0.0.1:6060",
		},
		Storage: StorageConfig{
			DataDir:       "data",
			DBPath:        "data/orenda.db",
			WALMode:       true,
			BusyTimeoutMs: 5000,
			EnableForeign: true,
		},
		Auth: AuthConfig{
			JWTSecret: "",
			// Phase 28.4: OWASP-aligned default for a cookie-issued
			// session token. The previous 168h (7 days) survives
			// in any token already issued — JWT exp is baked into
			// the token — so existing sessions stay valid until
			// their original expiry. New logins get a 24h window.
			JWTTTL:       24 * time.Hour,
			CookieName:   "orenda_session",
			CookieSecure: false,
			BcryptCost:   12,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Path:   "data/logs/orenda.log",
		},
		Backup: BackupConfig{
			Enabled:              true,
			MirrorDir:            "data/mirror",
			SnapshotDir:          "data/snapshots",
			SnapshotRotationDays: 30,
			GitPushInterval:      5 * time.Minute,
			SQLiteSnapshotCron:   "0 3 * * *",
			WALArchiveInterval:   15 * time.Minute,
		},
		Uploads: UploadsConfig{
			Dir:       "data/uploads",
			MaxSizeMB: 50,
		},
		// Phase 30.5: weekly digest scheduler. 168h = 7 days is
		// the natural cadence; operators can dial it up (24h for
		// daily standup summaries) or set it to 0 to disable.
		Notifier: NotifierConfig{
			DigestInterval: 168 * time.Hour,
		},
		// Phase 28.8: rate-limit defaults moved here from the
		// router's hard-coded constants. Values match what the
		// router used before the refactor (anon 60/20, auth
		// 300/100) so production behaviour is unchanged. Override
		// via yaml rate_limit: section or the existing
		// ORENDA_RATELIMIT_* envs (router precedence is unchanged).
		RateLimit: RateLimitConfig{
			AnonBurst:  60,
			AnonPerSec: 20.0,
			AuthBurst:  300,
			AuthPerSec: 100.0,
		},
	}
}

// Load reads configuration from the YAML file at path and applies environment
// overrides. If path is empty, defaultConfigPath is used.
//
// Returns the merged config and a non-nil error if loading or validation fails.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		path = defaultConfigPath
	}

	if err := loadYAML(path, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: load %q: %w", path, err)
	}

	applyEnvOverrides(cfg)

	// Task 138: resolve the JWT secret from a credential file when no
	// direct secret is configured. Priority: direct value (env/YAML)
	// wins; then auth.jwt_secret_file; then the standard systemd
	// CREDENTIALS_DIRECTORY (mounted by LoadCredential, named "jwt");
	// otherwise empty (serve will refuse to start with its existing
	// explicit error).
	if cfg.Auth.JWTSecret == "" && cfg.Auth.JWTSecretFile != "" {
		raw, err := os.ReadFile(cfg.Auth.JWTSecretFile)
		if err != nil {
			return nil, fmt.Errorf("config: auth.jwt_secret_file %q: %w", cfg.Auth.JWTSecretFile, err)
		}
		secret := strings.TrimSpace(string(raw))
		if secret == "" {
			return nil, fmt.Errorf("config: auth.jwt_secret_file %q is empty", cfg.Auth.JWTSecretFile)
		}
		cfg.Auth.JWTSecret = secret
	}
	// Last tier: systemd credentials (LoadCredential mounts the secret
	// as $CREDENTIALS_DIRECTORY/jwt). The name carries no "JWT"
	// substring, so the owner's DoD check on /proc/*/environ stays 0.
	if cfg.Auth.JWTSecret == "" {
		if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
			path := filepath.Join(dir, "jwt")
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("config: systemd credential jwt (%q): %w", path, err)
			}
			secret := strings.TrimSpace(string(raw))
			if secret == "" {
				return nil, fmt.Errorf("config: systemd credential jwt (%q) is empty", path)
			}
			cfg.Auth.JWTSecret = secret
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return cfg, nil
}

// loadYAML unmarshals the file at path into cfg. Missing files are not an
// error — the caller may rely on defaults — but malformed YAML is.
func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	return nil
}

// applyEnvOverrides walks every ORENDA_* variable and overrides the matching
// field in cfg.
//
// Supported types: string, int, bool, time.Duration (Go syntax: "30s", "5m").
// Nested sections use "__" as a separator, e.g. ORENDA_SERVER__PORT=3000.
// Unknown variables are ignored (forward-compat with future config keys).
func applyEnvOverrides(cfg *Config) {
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, envPrefix) {
			continue
		}
		path := strings.ToLower(strings.TrimPrefix(key, envPrefix))
		overrideField(cfg, path, value)
	}
}

// overrideField walks cfg by dot-separated path and assigns value to the
// matching leaf. Unknown paths are silently skipped.
func overrideField(cfg *Config, path, value string) {
	parts := strings.Split(path, "__")
	if len(parts) == 0 {
		return
	}

	// Map known top-level sections to their addressable structs.
	switch parts[0] {
	case "server":
		overrideServer(&cfg.Server, parts[1:], value)
	case "storage":
		overrideStorage(&cfg.Storage, parts[1:], value)
	case "auth":
		overrideAuth(&cfg.Auth, parts[1:], value)
	case "logging":
		overrideLogging(&cfg.Logging, parts[1:], value)
	case "backup":
		overrideBackup(&cfg.Backup, parts[1:], value)
	case "uploads":
		overrideUploads(&cfg.Uploads, parts[1:], value)
	// Phase 28.8: rate-limit env namespace. The YAML section
	// is named `ratelimit` (no underscore) so ORENDA_RATELIMIT__AUTH_BURST
	// splits cleanly into ["ratelimit", "auth", "burst"] under
	// our `__` tokenisation. Operators who still have
	// ORENDA_RATELIMIT_AUTH_BURST (no `__`) in their scripts need
	// to add the underscores — called out in the migration note
	// of the commit message and in PLAN.md.
	case "ratelimit":
		overrideRateLimit(&cfg.RateLimit, parts[1:], value)
	}
}

func overrideRateLimit(c *RateLimitConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	// Sub-keys come from `__` splitting, so e.g.
	// `ORENDA_RATELIMIT_AUTH_BURST` arrives as `["auth","burst"]`
	// — not `["auth_burst"]`. Re-join so we match the YAML key
	// shape and stay symmetric with overrideStorage et al.
	key := strings.Join(p, "_")
	switch key {
	case "anon_burst":
		if n, err := strconv.Atoi(v); err == nil {
			c.AnonBurst = n
		}
	case "anon_per_sec":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.AnonPerSec = f
		}
	case "auth_burst":
		if n, err := strconv.Atoi(v); err == nil {
			c.AuthBurst = n
		}
	case "auth_per_sec":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.AuthPerSec = f
		}
	}
}

func overrideServer(c *ServerConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "host":
		c.Host = v
	case "port":
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	case "read_timeout":
		if d, err := time.ParseDuration(v); err == nil {
			c.ReadTimeout = d
		}
	case "write_timeout":
		if d, err := time.ParseDuration(v); err == nil {
			c.WriteTimeout = d
		}
	case "shutdown_timeout":
		if d, err := time.ParseDuration(v); err == nil {
			c.ShutdownTimeout = d
		}
	case "debug_pprof":
		if b, err := strconv.ParseBool(v); err == nil {
			c.DebugPProf = b
		}
	case "pprof_addr":
		c.PProfAddr = v
	}
}

func overrideStorage(c *StorageConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "data_dir":
		c.DataDir = v
	case "db_path":
		c.DBPath = v
	case "wal_mode":
		if b, err := strconv.ParseBool(v); err == nil {
			c.WALMode = b
		}
	case "busy_timeout_ms":
		if n, err := strconv.Atoi(v); err == nil {
			c.BusyTimeoutMs = n
		}
	case "enable_foreign_keys":
		if b, err := strconv.ParseBool(v); err == nil {
			c.EnableForeign = b
		}
	}
}

func overrideAuth(c *AuthConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "jwt_secret":
		c.JWTSecret = v
	case "jwt_secret_file":
		c.JWTSecretFile = v
	case "jwt_ttl":
		if d, err := time.ParseDuration(v); err == nil {
			c.JWTTTL = d
		}
	case "cookie_name":
		c.CookieName = v
	case "cookie_secure":
		if b, err := strconv.ParseBool(v); err == nil {
			c.CookieSecure = b
		}
	case "bcrypt_cost":
		if n, err := strconv.Atoi(v); err == nil {
			c.BcryptCost = n
		}
	}
}

func overrideLogging(c *LoggingConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "level":
		c.Level = v
	case "format":
		c.Format = v
	case "path":
		c.Path = v
	}
}

func overrideBackup(c *BackupConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "enabled":
		if b, err := strconv.ParseBool(v); err == nil {
			c.Enabled = b
		}
	case "remote_url":
		c.RemoteURL = v
	case "remote_auth":
		c.RemoteAuth = v
	case "mirror_dir":
		c.MirrorDir = v
	case "snapshot_dir":
		c.SnapshotDir = v
	case "snapshot_rotation_days":
		if n, err := strconv.Atoi(v); err == nil {
			c.SnapshotRotationDays = n
		}
	case "git_push_interval":
		if d, err := time.ParseDuration(v); err == nil {
			c.GitPushInterval = d
		}
	case "sqlite_snapshot_cron":
		c.SQLiteSnapshotCron = v
	case "wal_archive_interval":
		if d, err := time.ParseDuration(v); err == nil {
			c.WALArchiveInterval = d
		}
	}
}

func overrideUploads(c *UploadsConfig, p []string, v string) {
	if len(p) == 0 {
		return
	}
	switch p[0] {
	case "dir":
		c.Dir = v
	case "max_size_mb":
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxSizeMB = n
		}
	}
}

// Validate enforces invariants that can't be expressed by yaml defaults.
//
// jwt_secret is intentionally optional in Phase 0 — endpoints that need it
// (Phase 1) will return an explicit error. This keeps `make dev` runnable
// before the operator has generated a secret.
func (c *Config) Validate() error {
	var errs []string

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Sprintf("server.port out of range: %d", c.Server.Port))
	}
	if c.Storage.DBPath == "" {
		errs = append(errs, "storage.db_path is required")
	}
	if c.Storage.BusyTimeoutMs < 0 {
		errs = append(errs, "storage.busy_timeout_ms must be >= 0")
	}
	if c.Auth.BcryptCost < 4 || c.Auth.BcryptCost > 31 {
		errs = append(errs, fmt.Sprintf("auth.bcrypt_cost out of range: %d", c.Auth.BcryptCost))
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("logging.level invalid: %q", c.Logging.Level))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ResolveDataDir returns an absolute path for cfg.Storage.DataDir, evaluated
// relative to baseDir (typically the executable's working directory).
//
// This helper exists so callers can normalize config paths consistently across
// the binary.
func (c *Config) ResolveDataDir(baseDir string) string {
	return resolveRelative(baseDir, c.Storage.DataDir)
}

// ResolveDBPath returns an absolute path for cfg.Storage.DBPath.
func (c *Config) ResolveDBPath(baseDir string) string {
	return resolveRelative(baseDir, c.Storage.DBPath)
}

// ResolveUploadsDir returns an absolute path for cfg.Uploads.Dir. The
// attachment service reads/writes files relative to this path; without
// resolution the working directory of the serve process becomes
// load-bearing and uploads appear to vanish after a restart.
func (c *Config) ResolveUploadsDir(baseDir string) string {
	return resolveRelative(baseDir, c.Uploads.Dir)
}

func resolveRelative(base, p string) string {
	if p == "" {
		return base
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}
