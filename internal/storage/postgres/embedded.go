package postgres

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Embedded runtime defaults (wiki:storage-adapters D7). The port and
// the binary repository can be overridden via config
// (storage.postgres.embedded_port / storage.postgres.binaries_url).
const (
	// DefaultEmbeddedPort keeps the embedded cluster clear of the
	// 5432 an operator's development PostgreSQL usually occupies.
	DefaultEmbeddedPort = 5433
	// DefaultEmbeddedUser / DefaultEmbeddedPassword bootstrap the
	// throwaway cluster; initdb creates them and the runtime pool
	// dials them back. Loopback-only by construction.
	DefaultEmbeddedUser     = "postgres"
	DefaultEmbeddedPassword = "postgres"
	// EmbeddedVersion pins the zonky binary series to the same major
	// the external smoke runs (postgres:16-alpine).
	EmbeddedVersion = embeddedpostgres.V16
	// DefaultStartTimeout bounds initdb + first start; the library
	// default (15s) is tight on cold binary extraction.
	DefaultStartTimeout = 30 * time.Second
)

// EmbeddedOptions configures one embedded-postgres cluster lifecycle.
//
// DataPath is the PGDATA directory and survives restarts (initdb runs
// once; later starts reuse the cluster). RuntimePath is scratch — the
// library wipes it at every Start (it extracts the postgres binaries
// there) and this package removes it after Stop — so it must live
// OUTSIDE DataPath, or the first initdb would find a non-empty target
// and the next Start would delete the freshly extracted binaries under
// a running cluster's sibling paths.
type EmbeddedOptions struct {
	DataPath    string
	RuntimePath string
	Port        int
	Username    string
	Password    string
	Database    string
	// BinariesRepositoryURL overrides the Maven repository binaries are
	// fetched from (offline mirrors); empty uses the library default.
	BinariesRepositoryURL string
	StartTimeout          time.Duration
	// Logs receives the postmaster output. The library defaults to
	// os.Stdout; production wiring must pass a zap-backed writer so
	// postgres logs land in the structured log, not the terminal.
	Logs io.Writer
}

// BuildEmbeddedOptions fills the libpq defaults of a bootstrap cluster:
// the caller supplies the validated database name plus whatever
// overrides the config carried, everything else falls back to the
// embedded defaults above.
func BuildEmbeddedOptions(database string, port int, user, password string) (EmbeddedOptions, error) {
	if database == "" {
		return EmbeddedOptions{}, fmt.Errorf("postgres: embedded runtime requires a database name")
	}
	if port == 0 {
		port = DefaultEmbeddedPort
	}
	if user == "" {
		user = DefaultEmbeddedUser
	}
	if password == "" {
		password = DefaultEmbeddedPassword
	}
	return EmbeddedOptions{
		Port:     port,
		Username: user,
		Password: password,
		Database: database,
	}, nil
}

// Connection returns the RuntimeConfig that dials the embedded cluster
// described by o: loopback, the cluster's port and bootstrap
// credentials, sslmode disabled (initdb creates no certificates).
func (o EmbeddedOptions) Connection() RuntimeConfig {
	return RuntimeConfig{
		Host:     "127.0.0.1",
		Port:     o.Port,
		User:     o.Username,
		Password: o.Password,
		Database: o.Database,
		SSLMode:  "disable",
	}
}

// Embedded is a started embedded-postgres cluster. Stop terminates the
// postmaster and removes the scratch runtime directory.
type Embedded struct {
	db          *embeddedpostgres.EmbeddedPostgres
	runtimePath string
}

// StartEmbedded starts the cluster described by opts, creating the data
// directory on first use and reusing it on later runs. DataPath and
// RuntimePath are created when missing; RuntimePath should be empty or
// absent — the library removes it before extracting binaries.
func StartEmbedded(opts EmbeddedOptions) (*Embedded, error) {
	if opts.DataPath == "" {
		return nil, fmt.Errorf("postgres: embedded runtime requires dataPath")
	}
	if opts.RuntimePath == "" {
		return nil, fmt.Errorf("postgres: embedded runtime requires runtimePath (scratch outside dataPath)")
	}
	if opts.Logs == nil {
		return nil, fmt.Errorf("postgres: embedded runtime requires a log sink (postmaster output must not reach stdout)")
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return nil, fmt.Errorf("postgres: embedded port out of range: %d", opts.Port)
	}
	startTimeout := opts.StartTimeout
	if startTimeout == 0 {
		startTimeout = DefaultStartTimeout
	}
	if err := os.MkdirAll(opts.DataPath, 0o755); err != nil {
		return nil, fmt.Errorf("postgres: embedded data path: %w", err)
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(EmbeddedVersion).
		Port(uint32(opts.Port)). //nolint:gosec // port range validated above
		Username(opts.Username).
		Password(opts.Password).
		Database(opts.Database).
		DataPath(opts.DataPath).
		RuntimePath(opts.RuntimePath).
		StartTimeout(startTimeout).
		Logger(opts.Logs)
	if opts.BinariesRepositoryURL != "" {
		cfg = cfg.BinaryRepositoryURL(opts.BinariesRepositoryURL)
	}
	cluster := embeddedpostgres.NewDatabase(cfg)
	if err := cluster.Start(); err != nil {
		return nil, fmt.Errorf("postgres: embedded start: %w", err)
	}
	return &Embedded{db: cluster, runtimePath: opts.RuntimePath}, nil
}

// Stop terminates the postmaster (pg_ctl stop -w — a graceful fast
// shutdown with no orphaned processes) and removes the scratch runtime
// directory. The data directory survives for the next start.
func (e *Embedded) Stop() error {
	if err := e.db.Stop(); err != nil {
		return fmt.Errorf("postgres: embedded stop: %w", err)
	}
	// Scratch removal is best-effort: the library wipes the directory
	// at the next Start anyway; a leaked tmp dir on a crashed run is
	// acceptable scratch loss, not state corruption.
	_ = os.RemoveAll(e.runtimePath)
	return nil
}

// ScratchRuntimePath returns a fresh scratch directory for the runtime
// (binaries extraction) under os.TempDir. Callers pass it as
// EmbeddedOptions.RuntimePath; Stop removes it.
func ScratchRuntimePath() (string, error) {
	dir, err := os.MkdirTemp("", "orenda-pg-runtime-")
	if err != nil {
		return "", fmt.Errorf("postgres: embedded scratch dir: %w", err)
	}
	return filepath.Clean(dir), nil
}
