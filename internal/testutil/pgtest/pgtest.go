// Package pgtest is the postgres leg of the two-driver repository test
// matrix (T364, wiki:storage-adapters D7). It is the postgres
// counterpart of testutil's sqlite VACUUM INTO template:
//
//   - a postgres server is provisioned once per test binary — either an
//     embedded-postgres cluster (default; binaries are cached under
//     ~/.embedded-postgres-go, offline after the first run) or an
//     external/docker server via ORENDA_TEST_PG_DSN, which exercises
//     the same open path the production external mode uses;
//   - that server holds one migrated template database (the postgres
//     baseline applied once);
//   - every test clones the template via native CREATE DATABASE ...
//     TEMPLATE and gets a pristine, fully migrated database in tens of
//     milliseconds.
//
// The package deliberately imports internal/storage/postgres but never
// the storage seam: the matrix helpers are used from test files that
// live INSIDE package sqlite, and the seam imports sqlite — importing
// the seam here would make those imports illegal. Opening goes through
// postgres.OpenRuntime directly, which is exactly the connector + shim
// wiring the seam's postgres branch performs, and migrations run
// through postgres.Open's simple-protocol handle exactly like the
// seam's migration path.
//
// Driver selection is shared by every matrix participant:
//
//	ORENDA_TEST_DRIVERS   comma list; default "sqlite,postgres";
//	                      "sqlite" opts the suite out of postgres
//	ORENDA_TEST_PG_DSN    external postgres instead of the embedded
//	                      cluster (same code path as production DSN mode)
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ramgml/orenda/internal/storage/postgres"
)

// Matrix driver names as reported by ActiveDriver.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Connection defaults for the per-test pools. Tests stay short-lived,
// so the same busy timeout the sqlite fixtures use is mapped to
// lock_timeout by the runtime's after-connect hook.
const testBusyTimeoutMs = 5000

var (
	activeDriver atomic.Value // string
)

func init() {
	activeDriver.Store(DriverSQLite)
}

// ActiveDriver reports which dialect the current matrix run executes.
// Defaults to sqlite for binaries that never enter DriverMatrix, so a
// plain `go test -run X` on a non-matrix package keeps its historic
// sqlite-only behaviour.
func ActiveDriver() string {
	return activeDriver.Load().(string)
}

// Drivers returns the configured driver list. ORENDA_TEST_DRIVERS is a
// comma-separated subset of {sqlite, postgres}; empty or unset selects
// both. Unknown tokens make DriverMatrix exit loudly before running
// anything — a typo silently shrinking the matrix would defeat it.
func Drivers() []string {
	raw := strings.TrimSpace(os.Getenv("ORENDA_TEST_DRIVERS"))
	if raw == "" {
		return []string{DriverSQLite, DriverPostgres}
	}
	var drivers []string
	for _, part := range strings.Split(raw, ",") {
		d := strings.TrimSpace(part)
		if d == "" {
			continue
		}
		if d != DriverSQLite && d != DriverPostgres {
			fmt.Fprintf(os.Stderr, "pgtest: unknown driver %q in ORENDA_TEST_DRIVERS (want %q or %q)\n",
				d, DriverSQLite, DriverPostgres)
			os.Exit(2)
		}
		drivers = append(drivers, d)
	}
	if drivers == nil {
		return []string{DriverSQLite, DriverPostgres}
	}
	return drivers
}

// DriverMatrix runs m once per configured driver, pinning ActiveDriver
// for the duration of each run and prefixing the output of every run
// with a marker line (the DoD evidence for "both drivers green"). The
// exit code is the worst per-driver code. The shared postgres server,
// if one was ever started, is shut down when the last run finishes —
// packages that never touch postgres pay nothing.
func DriverMatrix(m *testing.M) int {
	defer shutdown()
	code := 0
	for _, d := range Drivers() {
		activeDriver.Store(d)
		fmt.Printf("=== matrix driver=%s ===\n", d)
		if c := m.Run(); c > code {
			code = c
		}
	}
	return code
}

// cluster is the per-binary shared postgres server plus its migrated
// template database.
type cluster struct {
	// admin is a simple-protocol handle on the maintenance database —
	// the same dial flavour the migration runner uses; CREATE/DROP
	// DATABASE statements must not go through prepared statements.
	admin *sql.DB
	// open is the parts-form config per-test pools are dialed from;
	// TemplateDB only swaps the Database field.
	open postgres.RuntimeConfig
	// tmpl is the migrated template database name.
	tmpl string
	// pkg is the sanitized test-binary name used to namespace every
	// database this binary creates — parallel package binaries share
	// external (DSN-mode) servers without stepping on each other.
	pkg string
	// seq numbers per-test databases.
	seq atomic.Int64

	embedded    *postgres.Embedded
	dataPath    string
	runtimePath string
}

var (
	clusterOnce sync.Once
	theCluster  *cluster
	clusterErr  error
)

// freePort reserves an ephemeral loopback port and releases it — the
// postmaster binds it a moment later. Parallel binaries never collide.
func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pgtest: probe free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// packageName derives the database namespace from the test binary so
// that parallel package binaries are isolated even on one shared
// external server. "sqlite.test" → "sqlite".
func packageName() string {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(base, ".test")
	base = strings.TrimSuffix(base, ".exe")
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 'a' - 'A')
		default:
			b.WriteRune('_')
		}
	}
	if s := b.String(); s != "" {
		return s
	}
	return "pkg"
}

// startCluster provisions the shared server and the migrated template.
func startCluster(t testing.TB) (*cluster, error) {
	t.Helper()
	pkg := packageName()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := &cluster{pkg: pkg, tmpl: "orenda_tmpl_" + pkg}

	if dsn := strings.TrimSpace(os.Getenv("ORENDA_TEST_PG_DSN")); dsn != "" {
		// External server: reduce the DSN to parts so per-test opens
		// can swap the database name while host/auth/sslmode stay
		// exactly what production DSN mode dialed.
		cc, err := pgconn.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("pgtest: parse ORENDA_TEST_PG_DSN: %w", err)
		}
		sslmode := "require"
		if cc.TLSConfig == nil {
			sslmode = "disable"
		}
		c.open = postgres.RuntimeConfig{
			Host:     cc.Host,
			Port:     int(cc.Port),
			User:     cc.User,
			Password: cc.Password,
			SSLMode:  sslmode,
		}
		// Admin statements run on the DSN's own database — any database
		// on the server can host CREATE/DROP DATABASE.
		admin, err := postgres.Open(ctx, dsn)
		if err != nil {
			return nil, fmt.Errorf("pgtest: dial ORENDA_TEST_PG_DSN: %w", err)
		}
		c.admin = admin
	} else {
		// Embedded cluster: unique data dir and port per binary run, so
		// a crashed predecessor can never wedge the next run.
		port := freePort(t)
		dataPath, err := os.MkdirTemp("", "orenda-pgtest-data-*")
		if err != nil {
			return nil, fmt.Errorf("pgtest: data dir: %w", err)
		}
		runtimePath, err := postgres.ScratchRuntimePath()
		if err != nil {
			_ = os.RemoveAll(dataPath)
			return nil, fmt.Errorf("pgtest: runtime dir: %w", err)
		}
		bootstrap := "orenda_boot_" + pkg
		opts, err := postgres.BuildEmbeddedOptions(bootstrap, port, "", "")
		if err != nil {
			_ = os.RemoveAll(dataPath)
			_ = os.RemoveAll(runtimePath)
			return nil, fmt.Errorf("pgtest: embedded options: %w", err)
		}
		opts.DataPath = dataPath
		opts.RuntimePath = runtimePath
		// Postmaster output stays out of the test log; failures surface
		// through the dial error below.
		opts.Logs = io.Discard
		emb, err := postgres.StartEmbedded(opts)
		if err != nil {
			_ = os.RemoveAll(dataPath)
			_ = os.RemoveAll(runtimePath)
			return nil, fmt.Errorf("pgtest: start embedded postgres (opt out with ORENDA_TEST_DRIVERS=sqlite, or point ORENDA_TEST_PG_DSN at a server): %w", err)
		}
		c.embedded = emb
		c.dataPath = dataPath
		c.runtimePath = runtimePath
		c.open = opts.Connection()
		adminDSN, err := postgres.ResolveDSN(c.open)
		if err != nil {
			return nil, fmt.Errorf("pgtest: embedded dsn: %w", err)
		}
		admin, err := postgres.Open(ctx, adminDSN)
		if err != nil {
			return nil, fmt.Errorf("pgtest: dial embedded cluster: %w", err)
		}
		c.admin = admin
	}

	// Rebuild the template from scratch every binary run: cheaper to
	// drop + re-migrate (single baseline file) than to reason about a
	// stale schema after a migration edit.
	if err := c.execAdmin(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(c.tmpl)+" WITH (FORCE)"); err != nil {
		return nil, fmt.Errorf("pgtest: drop stale template: %w", err)
	}
	if err := c.execAdmin(ctx, "CREATE DATABASE "+quoteIdent(c.tmpl)); err != nil {
		return nil, fmt.Errorf("pgtest: create template: %w", err)
	}
	tmplCfg := c.open
	tmplCfg.Database = c.tmpl
	tmplDSN, err := postgres.ResolveDSN(tmplCfg)
	if err != nil {
		return nil, fmt.Errorf("pgtest: template dsn: %w", err)
	}
	tmplDB, err := postgres.Open(ctx, tmplDSN)
	if err != nil {
		return nil, fmt.Errorf("pgtest: dial template: %w", err)
	}
	if err := postgres.Migrate(ctx, tmplDB, postgres.MigrationsFS, "migrations"); err != nil {
		_ = tmplDB.Close()
		return nil, fmt.Errorf("pgtest: migrate template: %w", err)
	}
	// The template must be connection-free while it is cloned.
	if err := tmplDB.Close(); err != nil {
		return nil, fmt.Errorf("pgtest: close template: %w", err)
	}
	return c, nil
}

// execAdmin runs one statement on the admin handle.
func (c *cluster) execAdmin(ctx context.Context, q string) error {
	if _, err := c.admin.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("admin %q: %w", q, err)
	}
	return nil
}

// quoteIdent double-quotes a generated identifier. All names are
// produced by this package (no user input), quoting is belt and braces.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// theClusterFor lazily provisions the shared cluster.
func theClusterFor(t testing.TB) *cluster {
	t.Helper()
	clusterOnce.Do(func() {
		theCluster, clusterErr = startCluster(t)
	})
	if clusterErr != nil {
		t.Fatalf("pgtest: postgres accelerator unavailable: %v", clusterErr)
	}
	return theCluster
}

// TemplateDB clones the migrated template into a fresh database and
// opens a shim-wrapped pool on it — the identical open path the
// production postgres mode takes (postgres.OpenRuntime: pgx stdlib
// connector + dialect shim). Cleanup closes the pool and drops the
// database. The returned *sql.DB drops into every sqlite.New*Repository
// constructor unchanged. Budgets match startCluster's two minutes:
// under `go test ./...` a dozen matrix binaries start their clusters
// simultaneously and the first calls queue behind that storm.
func TemplateDB(t testing.TB) *sql.DB {
	t.Helper()
	c := theClusterFor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	name := fmt.Sprintf("orenda_%s_t%d", c.pkg, c.seq.Add(1))
	// Crash leftovers from a previous run with the same pid would make
	// CREATE ... TEMPLATE fail; dropping first is one cheap round trip.
	if err := c.execAdmin(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
		t.Fatalf("pgtest: drop stale %s: %v", name, err)
	}
	if err := c.execAdmin(ctx, "CREATE DATABASE "+quoteIdent(name)+" TEMPLATE "+quoteIdent(c.tmpl)); err != nil {
		t.Fatalf("pgtest: clone template into %s: %v", name, err)
	}

	cfg := c.open
	cfg.Database = name
	cfg.BusyTimeoutMs = testBusyTimeoutMs
	_, pool, err := postgres.OpenRuntime(ctx, cfg)
	if err != nil {
		t.Fatalf("pgtest: open %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer dropCancel()
		_ = c.execAdmin(dropCtx, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)")
	})
	return pool
}

// shutdown terminates the embedded cluster, if one was started. It is
// wired into DriverMatrix; external (DSN-mode) servers are never owned
// and never touched.
func shutdown() {
	if theCluster == nil {
		return
	}
	if theCluster.admin != nil {
		_ = theCluster.admin.Close()
	}
	if theCluster.embedded != nil {
		_ = theCluster.embedded.Stop()
	}
	if theCluster.dataPath != "" {
		_ = os.RemoveAll(theCluster.dataPath)
	}
	if theCluster.runtimePath != "" {
		_ = os.RemoveAll(theCluster.runtimePath)
	}
}
