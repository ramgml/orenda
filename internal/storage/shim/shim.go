// Package shim is the SQLite→PostgreSQL dialect shim for the storage
// layer (T362, epic wiki:storage-adapters).
//
// Every repository in internal/storage/sqlite holds a *sql.DB and
// speaks the SQLite dialect: anonymous ? placeholders,
// datetime('now') stamps and INSERT OR IGNORE. The shim sits BELOW
// database/sql — it wraps a driver.Connector and rewrites each query
// on its way into the driver — so the repository files and their
// constructors stay untouched while the same SQL runs on PostgreSQL.
//
// Rules applied for DialectPostgres (see rewrite.go):
//   - ? → $1..$n, renumbered outside string literals, quoted
//     identifiers and comments;
//   - datetime('now') → to_char(now() AT TIME ZONE 'UTC',
//     'YYYY-MM-DD HH24:MI:SS') — the same 19-character zero-padded
//     UTC layout SQLite emits, so TEXT-timestamp comparisons keep
//     their lexicographic = chronological meaning;
//   - INSERT OR IGNORE INTO → INSERT INTO … ON CONFLICT DO NOTHING.
//
// DialectSQLite is the calibrated no-op: the base connector is
// returned as-is and the rewrite is the identity function, pinned by
// string-equality tests. The sqlite driver must keep receiving
// byte-exact SQL.
//
// Wiring (T361): build the postgres connector (e.g. pgx
// stdlib.GetConnector), hand it to NewConnector with DialectPostgres
// and sql.OpenDB the result — the returned *sql.DB drops into every
// New*Repository constructor unchanged.
package shim

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// Dialect selects which SQL dialect the shim speaks towards.
type Dialect string

// Supported shim targets.
const (
	// DialectSQLite keeps SQL byte-exact (identity passthrough).
	DialectSQLite Dialect = "sqlite"
	// DialectPostgres enables the SQLite→PostgreSQL rewrite rules.
	DialectPostgres Dialect = "postgres"
)

// NewConnector wraps base so every query is rewritten from the SQLite
// dialect to the target dialect before it reaches the driver.
//
// DialectPostgres enables the full rule set; DialectSQLite returns
// base unchanged — the sqlite path must stay a byte-exact no-op, so no
// wrapper layer is inserted at all. base must speak the target
// dialect's wire protocol itself (for postgres: a driver.Connector
// over a PostgreSQL server, e.g. the pgx stdlib connector).
func NewConnector(base driver.Connector, target Dialect) driver.Connector {
	if target != DialectPostgres {
		return base
	}
	return &postgresConnector{base: base}
}

// postgresConnector produces shim-wrapped connections over a
// PostgreSQL-speaking base connector.
type postgresConnector struct {
	base driver.Connector
}

// Driver returns the base driver; database/sql only uses it for
// legacy Open paths the shim never takes.
func (c *postgresConnector) Driver() driver.Driver { return c.base.Driver() }

// Connect dials through the base connector and wraps the connection.
func (c *postgresConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("shim: connect: %w", err)
	}
	return &postgresConn{Conn: conn}, nil
}

// postgresConn rewrites every query and delegates to the wrapped
// PostgreSQL connection. Optional driver interfaces are forwarded
// explicitly so database/sql keeps the fast paths the underlying
// driver offers.
type postgresConn struct {
	driver.Conn
}

// Prepare rewrites the query, prepares it on the base connection and
// hands back a shim-wrapped statement.
func (c *postgresConn) Prepare(query string) (driver.Stmt, error) {
	rq, err := rewrite(query)
	if err != nil {
		return nil, fmt.Errorf("shim: rewrite: %w", err)
	}
	stmt, err := c.Conn.Prepare(rq)
	if err != nil {
		return nil, err
	}
	return &postgresStmt{Stmt: stmt}, nil
}

// PrepareContext is the context-aware prepare path.
func (c *postgresConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		rq, err := rewrite(query)
		if err != nil {
			return nil, fmt.Errorf("shim: rewrite: %w", err)
		}
		stmt, err := p.PrepareContext(ctx, rq)
		if err != nil {
			return nil, err
		}
		return &postgresStmt{Stmt: stmt}, nil
	}
	// Mirror database/sql's fallback: prepare without a context. The
	// query is rewritten once, in Prepare.
	return c.Prepare(query)
}

// QueryContext rewrites and runs the query directly when the base
// connection supports the fast path, otherwise skips so database/sql
// falls back to Prepare (which rewrites too).
func (c *postgresConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rq, err := rewrite(query)
	if err != nil {
		return nil, fmt.Errorf("shim: rewrite: %w", err)
	}
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, rq, args)
	}
	//nolint:staticcheck // legacy interface: fallback mirrors database/sql for drivers without QueryerContext
	if q, ok := c.Conn.(driver.Queryer); ok {
		return q.Query(rq, namedValuesToValues(args))
	}
	return nil, driver.ErrSkip
}

// ExecContext mirrors QueryContext for statements that return no rows.
func (c *postgresConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	rq, err := rewrite(query)
	if err != nil {
		return nil, fmt.Errorf("shim: rewrite: %w", err)
	}
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, rq, args)
	}
	//nolint:staticcheck // legacy interface: fallback mirrors database/sql for drivers without ExecerContext
	if e, ok := c.Conn.(driver.Execer); ok {
		return e.Exec(rq, namedValuesToValues(args))
	}
	return nil, driver.ErrSkip
}

// BeginTx forwards to the base connection's context-aware begin when
// available, mirroring database/sql's own fallback otherwise.
func (c *postgresConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if opts.Isolation != driver.IsolationLevel(sql.LevelDefault) {
		return nil, fmt.Errorf("shim: driver does not support non-default isolation level: %v", opts.Isolation)
	}
	if opts.ReadOnly {
		return nil, fmt.Errorf("shim: driver does not support read-only transactions")
	}
	return c.Begin()
}

// Ping forwards the liveness probe to the base connection; without a
// Pinger underneath the connection is freshly dialed, so a nil answer
// matches what database/sql would conclude on its own.
func (c *postgresConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// IsValid forwards connection health so database/sql can pool the
// shim-wrapped connection exactly like a raw one.
func (c *postgresConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// ResetSession forwards per-session cleanup to the base connection.
func (c *postgresConn) ResetSession(ctx context.Context) error {
	if s, ok := c.Conn.(driver.SessionResetter); ok {
		return s.ResetSession(ctx)
	}
	return nil
}

// CheckNamedValue forwards argument validation; ErrSkip tells
// database/sql to apply its default converter instead.
func (c *postgresConn) CheckNamedValue(nv *driver.NamedValue) error {
	if chk, ok := c.Conn.(driver.NamedValueChecker); ok {
		return chk.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// postgresStmt delegates execution to a statement that was prepared
// from already-rewritten SQL; arguments need no rewriting at all.
type postgresStmt struct {
	driver.Stmt
}

// ExecContext forwards the context-aware path when the base statement
// has one, mirroring database/sql's args conversion otherwise.
func (s *postgresStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	//nolint:staticcheck // legacy interface: fallback mirrors database/sql for statements without StmtExecContext
	return s.Stmt.Exec(namedValuesToValues(args))
}

// QueryContext mirrors ExecContext for row-returning statements.
func (s *postgresStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return q.QueryContext(ctx, args)
	}
	//nolint:staticcheck // legacy interface: fallback mirrors database/sql for statements without StmtQueryContext
	return s.Stmt.Query(namedValuesToValues(args))
}

// namedValuesToValues strips ordinals for legacy driver interfaces
// that take plain []driver.Value, exactly like database/sql does.
func namedValuesToValues(args []driver.NamedValue) []driver.Value {
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	return vals
}
