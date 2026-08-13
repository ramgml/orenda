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
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`
	Logging LoggingConfig `yaml:"logging"`
	Backup  BackupConfig  `yaml:"backup"`
	Bots    []BotConfig   `yaml:"bots"`
	Uploads UploadsConfig `yaml:"uploads"`
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
// In Phase 0 only the JWT secret and bcrypt cost are consumed; the cookie and
// token endpoints are wired in Phase 1.
type AuthConfig struct {
	JWTSecret    string        `yaml:"jwt_secret"`
	JWTTTL       time.Duration `yaml:"jwt_ttl"`
	CookieName   string        `yaml:"cookie_name"`
	CookieSecure bool          `yaml:"cookie_secure"`
	BcryptCost   int           `yaml:"bcrypt_cost"`
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

// BotConfig is a placeholder for Phase 10 pluggable bots.
//
// Phase 0 loads the section so the YAML schema is stable across phases.
type BotConfig struct {
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"`
	Config  map[string]any `yaml:"config"`
	Enabled bool           `yaml:"enabled"`
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
