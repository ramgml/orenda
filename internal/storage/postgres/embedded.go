package postgres

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// DefaultEmbeddedLocale is the initdb locale for every fresh
	// embedded cluster (T365 review fix). Full-text search parity with
	// sqlite (T365) requires lower() to fold non-ASCII case: a C/POSIX
	// ctype — what initdb derives on a LANG-less host, e.g. the
	// shipped alpine image — silently keeps 'Поиск' and 'поиск'
	// distinct. C.UTF-8 is locale-independent UTF-8: present on
	// glibc ≥ 2.35 and musl ≥ 1.2.5 (alpine 3.22+) regardless of
	// the host LANG. lc_ctype and lc_collate come from it.
	DefaultEmbeddedLocale = "C.UTF-8"
	// FallbackEmbeddedLocale is tried when DefaultEmbeddedLocale is
	// rejected by initdb (older platforms without the C.UTF-8
	// alias); hosts where neither exists must pin EmbeddedOptions.
	// Locale explicitly.
	FallbackEmbeddedLocale = "en_US.UTF-8"
	// DefaultEmbeddedEncoding pins the cluster encoding; UTF-8 is the
	// only encoding the full-text search contract is defined for.
	DefaultEmbeddedEncoding = "UTF8"
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
	// Locale pins the initdb locale (and with it lc_ctype/lc_collate)
	// for a fresh cluster; empty selects DefaultEmbeddedLocale with
	// FallbackEmbeddedLocale as the fresh-init fallback. It only
	// takes effect when the data directory is initialized; an
	// existing cluster keeps its original locale and is guarded by
	// the post-start assert instead. Hosts where neither default
	// exists must set this field (cross-platform locale policy:
	// T368).
	Locale string
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
	stopped     bool
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
	// Incident-class guard (T370): classify an existing PGDATA before
	// starting. A live postmaster (preview or orphan) fails loudly
	// with the owning pid; a stale pid file from a killed previous run
	// warns and lets postgres clear the lock itself.
	if err := checkExistingCluster(opts); err != nil {
		return nil, err
	}
	// Fresh-init detection: initdb only runs on an empty data
	// directory, so only a fresh init may retry (and wipe) after a
	// failed locale attempt — a reuse path never touches PGDATA.
	fresh := dataDirEmpty(opts.DataPath)
	if err := os.MkdirAll(opts.DataPath, 0o755); err != nil {
		return nil, fmt.Errorf("postgres: embedded data path: %w", err)
	}
	locales := []string{opts.Locale}
	if opts.Locale == "" {
		locales = []string{DefaultEmbeddedLocale, FallbackEmbeddedLocale}
	}
	var (
		cluster *embeddedpostgres.EmbeddedPostgres
		lastErr error
	)
	for i, locale := range locales {
		if i > 0 {
			// The previous initdb attempt left a partial PGDATA in
			// this directory we created ourselves — reset it before
			// the next attempt (fresh inits only, see above).
			if err := os.RemoveAll(opts.DataPath); err != nil {
				return nil, fmt.Errorf("postgres: embedded data path reset: %w", err)
			}
			if err := os.MkdirAll(opts.DataPath, 0o755); err != nil {
				return nil, fmt.Errorf("postgres: embedded data path: %w", err)
			}
		}
		cfg := embeddedpostgres.DefaultConfig().
			Version(EmbeddedVersion).
			Port(uint32(opts.Port)). //nolint:gosec // port range validated above
			Username(opts.Username).
			Password(opts.Password).
			Database(opts.Database).
			DataPath(opts.DataPath).
			RuntimePath(opts.RuntimePath).
			Locale(locale).
			Encoding(DefaultEmbeddedEncoding).
			StartTimeout(startTimeout).
			Logger(opts.Logs)
		if opts.BinariesRepositoryURL != "" {
			cfg = cfg.BinaryRepositoryURL(opts.BinariesRepositoryURL)
		}
		cand := embeddedpostgres.NewDatabase(cfg)
		lastErr = cand.Start()
		if lastErr == nil {
			cluster = cand
			break
		}
		if !fresh {
			// An existing cluster failed to start — retrying with a
			// different initdb locale is meaningless, and wiping is
			// forbidden (PGDATA holds a real cluster).
			return nil, fmt.Errorf("postgres: embedded start: %w", lastErr)
		}
	}
	if cluster == nil {
		return nil, fmt.Errorf("postgres: embedded start: %w", lastErr)
	}
	emb := &Embedded{db: cluster, runtimePath: opts.RuntimePath}
	// Fail fast (T365 review fix): a C-like ctype/collation degrades
	// non-ASCII case folding silently — full-text search parity with
	// sqlite breaks without a single error raised. Loud beats silent.
	if err := assertClusterLocale(opts); err != nil {
		_ = emb.Stop()
		return nil, err
	}
	return emb, nil
}

// dataDirEmpty reports whether the data directory is absent or empty —
// i.e. the next start will run initdb (fresh init) rather than reuse an
// existing cluster.
func dataDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err != nil || len(entries) == 0
}

// assertClusterLocale dials the freshly started cluster and rejects a
// C-like lc_ctype/lc_collate with an actionable error (T365 review
// fix). The check runs on EVERY start, so a pre-existing cluster
// initialized under a broken locale fails here too — loudly, with the
// remediation — instead of degrading Cyrillic search silently.
func assertClusterLocale(opts EmbeddedOptions) error {
	dsn, err := ResolveDSN(opts.Connection())
	if err != nil {
		return fmt.Errorf("postgres: embedded locale assert: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("postgres: embedded locale assert: dial cluster: %w", err)
	}
	defer func() { _ = db.Close() }()
	var ctype, collate string
	if err := db.QueryRowContext(ctx,
		`SELECT datctype, datcollate FROM pg_database WHERE datname = current_database()`,
	).Scan(&ctype, &collate); err != nil {
		return fmt.Errorf("postgres: embedded locale assert: query: %w", err)
	}
	if issue := localeIssue(ctype, collate); issue != "" {
		return fmt.Errorf(
			"postgres: embedded cluster locale is not usable for full-text search: %s — "+
				"a C-like ctype/collation cannot fold non-ASCII case, so Cyrillic "+
				"(and any non-Latin) search silently misses matches; "+
				"reinitialize: stop the cluster, remove the data directory %s, "+
				"and start again under a *.UTF-8 locale (pin EmbeddedOptions.Locale "+
				"if the host has neither %s nor %s)",
			issue, opts.DataPath, DefaultEmbeddedLocale, FallbackEmbeddedLocale)
	}
	return nil
}

// localeIssue names the first C-like locale among the cluster's ctype
// and collation, or the empty string when both fold non-ASCII case.
func localeIssue(datctype, datcollate string) string {
	if cLikeLocale(datctype) {
		return fmt.Sprintf("datctype %q is C-like", datctype)
	}
	if cLikeLocale(datcollate) {
		return fmt.Sprintf("datcollate %q is C-like", datcollate)
	}
	return ""
}

// cLikeLocale reports whether a locale is the C/POSIX collation family
// (byte-order only, ASCII-only case folding).
func cLikeLocale(locale string) bool {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "c", "posix":
		return true
	default:
		return false
	}
}

// Stop terminates the postmaster (pg_ctl stop -w — a graceful fast
// shutdown with no orphaned processes) and removes the scratch runtime
// directory. The data directory survives for the next start. Stopping
// an already-stopped cluster is a no-op.
func (e *Embedded) Stop() error {
	if e.stopped {
		return nil
	}
	if err := e.db.Stop(); err != nil {
		return fmt.Errorf("postgres: embedded stop: %w", err)
	}
	e.stopped = true
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
