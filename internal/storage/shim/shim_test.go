package shim

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- recording fake driver -------------------------------------------------

// fakeDriver satisfies driver.Driver; the shim only reaches it through
// Connector.Driver(), never through Open-by-name.
type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("shim test: open by name is unused")
}

// fakeConnector hands out one recorded connection.
type fakeConnector struct {
	conn driver.Conn
}

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c *fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

// fakeConn records every SQL string the shim hands over, whichever
// driver path carried it.
type fakeConn struct {
	mu      sync.Mutex
	queries []string
	argsets [][]driver.Value

	closed bool
}

func (c *fakeConn) record(q string, args []driver.NamedValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	c.argsets = append(c.argsets, vals)
}

func (c *fakeConn) received() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	c.record(query, nil)
	return &fakeStmt{rec: func(args []driver.Value) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.argsets = append(c.argsets, args)
	}}, nil
}

func (c *fakeConn) Close() error {
	c.closed = true
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

// Fast paths: only exercised when the shim decides the base connection
// implements them.

func (c *fakeConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.record(query, nil)
	return &fakeStmt{rec: func(args []driver.Value) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.argsets = append(c.argsets, args)
	}}, nil
}

func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.record(query, args)
	return &fakeRows{cols: []string{"x"}}, nil
}

func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.record(query, args)
	return driver.RowsAffected(1), nil
}

func (c *fakeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return fakeTx{}, nil
}

// fakeStmt is prepared from already-rewritten SQL; it reports the
// arguments it receives through the rec callback.
type fakeStmt struct {
	rec func(args []driver.Value)
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.rec(args)
	return driver.RowsAffected(1), nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.rec(args)
	return &fakeRows{cols: []string{"x"}}, nil
}

// fakeTx is a do-nothing transaction.
type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

// fakeRows yields a single empty row.
type fakeRows struct {
	cols []string
	sent bool
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	for i := range dest {
		dest[i] = nil
	}
	return nil
}

// leanConn implements ONLY the mandatory driver.Conn surface, so the
// shim must answer database/sql's fast-path probes with ErrSkip and
// let the prepare fallback carry the rewrite.
type leanConn struct {
	mu      sync.Mutex
	queries []string
}

func (c *leanConn) Prepare(query string) (driver.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, query)
	return &fakeStmt{rec: func(args []driver.Value) {}}, nil
}

func (c *leanConn) Close() error { return nil }

func (c *leanConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

func (c *leanConn) received() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

// --- connector-level tests --------------------------------------------------

func TestConnectorRewritesThroughEveryDriverPath(t *testing.T) {
	conn := &fakeConn{}
	db := sql.OpenDB(NewConnector(&fakeConnector{conn: conn}, DialectPostgres))
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// Fast path: QueryerContext.
	_, err := db.QueryContext(ctx, "SELECT * FROM tasks WHERE id = ? AND status = ?", "a", "todo")
	require.NoError(t, err)

	// Fast path: ExecerContext.
	_, err = db.ExecContext(ctx, "INSERT INTO t (a, b) VALUES (?, ?)", "x", 7)
	require.NoError(t, err)

	// Prepared-statement path.
	st, err := db.PrepareContext(ctx, "UPDATE t SET a = ?, b = ? WHERE id = ?")
	require.NoError(t, err)
	_, err = st.ExecContext(ctx, "y", 1, "t1")
	require.NoError(t, err)
	require.NoError(t, st.Close())

	// Transactions ride the same connection and stay rewritten.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "DELETE FROM t WHERE id = ? AND done = ?", "t2", 0)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	got := conn.received()
	require.Len(t, got, 4)
	assert.Equal(t, "SELECT * FROM tasks WHERE id = $1 AND status = $2", got[0])
	assert.Equal(t, "INSERT INTO t (a, b) VALUES ($1, $2)", got[1])
	assert.Equal(t, "UPDATE t SET a = $1, b = $2 WHERE id = $3", got[2])
	assert.Equal(t, "DELETE FROM t WHERE id = $1 AND done = $2", got[3])
}

func TestConnectorArgumentsReachDriverUnchanged(t *testing.T) {
	conn := &fakeConn{}
	db := sql.OpenDB(NewConnector(&fakeConnector{conn: conn}, DialectPostgres))
	defer func() { _ = db.Close() }()

	_, err := db.ExecContext(context.Background(),
		"INSERT INTO t (a, b, c) VALUES (?, ?, ?)", "s", 42, nil)
	require.NoError(t, err)

	conn.mu.Lock()
	defer conn.mu.Unlock()
	require.Len(t, conn.argsets, 1)
	assert.Equal(t, []driver.Value{"s", int64(42), nil}, conn.argsets[0])
}

func TestConnectorRewritesWhenDriverLacksFastPaths(t *testing.T) {
	conn := &leanConn{}
	db := sql.OpenDB(NewConnector(&fakeConnector{conn: conn}, DialectPostgres))
	defer func() { _ = db.Close() }()

	var x any
	err := db.QueryRowContext(context.Background(),
		"SELECT a FROM t WHERE id = ? OR id = ?", "p", "q").Scan(&x)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), "UPDATE t SET a = ? WHERE id = ?", 1, "p")
	require.NoError(t, err)

	got := conn.received()
	require.Len(t, got, 2)
	assert.Equal(t, "SELECT a FROM t WHERE id = $1 OR id = $2", got[0])
	assert.Equal(t, "UPDATE t SET a = $1 WHERE id = $2", got[1])
}

// The sqlite path is the calibrated no-op: NewConnector hands back the
// base connector itself, and the fake driver below database/sql must
// receive the queries byte-exact — including SQLite-only syntax the
// postgres shim would rewrite.
func TestConnectorSqlitePathIsByteExactPassthrough(t *testing.T) {
	base := &fakeConnector{conn: &fakeConn{}}
	wrapped := NewConnector(base, DialectSQLite)
	assert.Equal(t, driver.Connector(base), wrapped, "sqlite target must return the base connector itself")

	queries := []string{
		"SELECT * FROM tasks WHERE id = ? AND status = ?",
		"INSERT INTO task_activity (id, payload, created_at) VALUES (?, ?, datetime('now'))",
		"INSERT OR IGNORE INTO task_tags (task_id, tag_id) VALUES (?, ?)",
		"UPDATE agents SET last_seen_at = datetime('now'), status = ? WHERE id = ?",
		"SELECT note FROM t WHERE note = 'trailing ? mark'",
	}
	for _, q := range queries {
		db := sql.OpenDB(wrapped)
		_, err := db.ExecContext(context.Background(), q, argsFor(q)...)
		require.NoError(t, err)
		require.NoError(t, db.Close())
	}
	conn := base.conn.(*fakeConn)
	assert.Equal(t, queries, conn.received(), "sqlite driver must receive byte-identical SQL")
}

// argsFor synthesises enough arguments for the passthrough queries.
func argsFor(q string) []any {
	n := strings.Count(q, "?")
	args := make([]any, n)
	for i := range args {
		args[i] = i
	}
	return args
}

// --- rebind numbering -------------------------------------------------------

// TestRebindNumbering pins the $1..$n numbering on the dynamic query
// shapes the repositories actually build: IN-lists via
// strings.Repeat("?, ", n-1)+"?" (task_repo ×8, time_entry_repo,
// activity_repo), conditionally assembled WHERE/SET clauses and the
// trailing q += " LIMIT ?" appends.
func TestRebindNumbering(t *testing.T) {
	inList := func(n int) string {
		placeholders := strings.Repeat("?, ", n-1) + "?"
		return "SELECT id FROM tasks WHERE id IN (" + placeholders + ")"
	}
	inWant := func(n int) string {
		ps := make([]string, n)
		for i := range ps {
			ps[i] = fmt.Sprintf("$%d", i+1)
		}
		return "SELECT id FROM tasks WHERE id IN (" + strings.Join(ps, ", ") + ")"
	}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "IN builder, single element (len-1==0 → bare ?)",
			query: inList(1),
			want:  inWant(1),
		},
		{
			name:  "IN builder ×10 (ListByProject IDs, aggregateCounters, BlockersForTasks, TagsForTasks, HasAnyEntriesByTasks, StatusChangesByTasks, task_repo:193/292/437/472/1199/1367/1859/1893 family)",
			query: inList(10),
			want:  inWant(10),
		},
		{
			name: "conditional WHERE assembly (task_repo.ListByProject: clauses joined with AND)",
			query: "SELECT id FROM tasks" +
				" WHERE project_id = ?" +
				" AND column_id = ?" +
				" AND (assignee_type = ? OR assignee_type IS NULL)" +
				" AND id IN (?, ?, ?)" +
				" ORDER BY position ASC",
			want: "SELECT id FROM tasks" +
				" WHERE project_id = $1" +
				" AND column_id = $2" +
				" AND (assignee_type = $3 OR assignee_type IS NULL)" +
				" AND id IN ($4, $5, $6)" +
				" ORDER BY position ASC",
		},
		{
			name: "conditional SET assembly (checklist_repo.UpdateItem, task_repo.UpdateProposalFields)",
			query: "UPDATE checklist_items SET id = id" +
				", done = ?" +
				", title = ?" +
				" WHERE id = ?",
			want: "UPDATE checklist_items SET id = id" +
				", done = $1" +
				", title = $2" +
				" WHERE id = $3",
		},
		{
			name: "trailing LIMIT append (project_activity_repo.ListByProject, course_activity_repo.ListByCourse)",
			query: "SELECT id FROM project_activity WHERE project_id = ?" +
				" ORDER BY created_at DESC, id DESC" +
				" LIMIT ?",
			want: "SELECT id FROM project_activity WHERE project_id = $1" +
				" ORDER BY created_at DESC, id DESC" +
				" LIMIT $2",
		},
		{
			name: "conditional range + LIMIT append (task_repo ListByDueBetween family)",
			query: "SELECT id FROM tasks" +
				" WHERE start_at IS NOT NULL AND start_at < ? AND end_at > ?" +
				" AND project_id = ?" +
				" ORDER BY start_at ASC" +
				" LIMIT ?",
			want: "SELECT id FROM tasks" +
				" WHERE start_at IS NOT NULL AND start_at < $1 AND end_at > $2" +
				" AND project_id = $3" +
				" ORDER BY start_at ASC" +
				" LIMIT $4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteFor(DialectPostgres, tt.query)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRebindNumberingSequentialOnLargeInsert(t *testing.T) {
	// task.Create: 30 anonymous placeholders across a multi-line INSERT.
	var b strings.Builder
	b.WriteString("INSERT INTO tasks (id, project_id, title, status, position,\n")
	b.WriteString("\tcontext_md, agent_notes, due_at, started_at, claimed_at, completed_at,\n")
	b.WriteString("\ttime_estimate_s, time_spent_s, start_at, end_at, all_day, color,\n")
	b.WriteString("\trecurrence, study_course_id, blocked_prev_status, number,\n")
	b.WriteString("\tcreated_by_type, created_by_id) VALUES (\n")
	for range 30 {
		b.WriteString("?, ")
	}
	b.WriteString("\n)")
	got, err := rewriteFor(DialectPostgres, b.String())
	require.NoError(t, err)

	assert.NotContains(t, got, "?")
	nums := placeholderNumbers(got)
	require.Len(t, nums, 30)
	for i, n := range nums {
		assert.Equal(t, i+1, n, "placeholders must be sequential $1..$30")
	}
}

// Documented behaviour: a '?' inside a single-quoted SQL literal is SQL
// text, not a parameter — the scanner leaves it untouched and keeps the
// numbering of the real placeholders intact. The static audit test
// guarantees the storage corpus contains no such literals, so this
// only guards the rewriter's own contract.
func TestRebindLeavesQuestionMarkInsideLiteral(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "literal with ? keeps it, real placeholder renumbers",
			query: "SELECT * FROM t WHERE note = 'best ? ever' AND id = ?",
			want:  "SELECT * FROM t WHERE note = 'best ? ever' AND id = $1",
		},
		{
			name:  "doubled quote inside literal",
			query: "SELECT * FROM t WHERE note = 'it''s a ? test' AND id = ? AND b = ?",
			want:  "SELECT * FROM t WHERE note = 'it''s a ? test' AND id = $1 AND b = $2",
		},
		{
			name:  "double-quoted identifier with ?",
			query: `SELECT "weird ? col" FROM t WHERE id = ?`,
			want:  `SELECT "weird ? col" FROM t WHERE id = $1`,
		},
		{
			name:  "line comment with ?",
			query: "SELECT 1 -- uses ? inside comment\nWHERE a = ?",
			want:  "SELECT 1 -- uses ? inside comment\nWHERE a = $1",
		},
		{
			name:  "block comment with ?",
			query: "SELECT /* keeps ? here */ 1 WHERE a = ?",
			want:  "SELECT /* keeps ? here */ 1 WHERE a = $1",
		},
		{
			name:  "no placeholders at all",
			query: "SELECT 1",
			want:  "SELECT 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteFor(DialectPostgres, tt.query)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The sqlite target rewrite is the identity by contract: pass-through
// string equality over representative real query shapes.
func TestRewriteSqliteTargetIsIdentity(t *testing.T) {
	queries := []string{
		"SELECT * FROM tasks WHERE id = ?",
		strings.Repeat("SELECT ?, ", 9) + "?",
		"UPDATE agents SET last_seen_at = datetime('now') WHERE id = ?",
		"INSERT OR IGNORE INTO sync_ops (client_id, server_id, op, target) VALUES (?, ?, '', '')",
		"INSERT INTO course_quizzes (id, lesson_id, position) VALUES (?, ?, COALESCE((SELECT MAX(position)+1 FROM course_quizzes WHERE lesson_id = ?), 1)) RETURNING position",
		"UPDATE course_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1",
		"INSERT INTO backup_settings (key, value) VALUES (?, ?)\n\t\tON CONFLICT(key) DO UPDATE SET value = excluded.value",
	}
	for _, q := range queries {
		got, err := rewriteFor(DialectSQLite, q)
		require.NoError(t, err)
		assert.Equal(t, q, got, "sqlite rewrite must be the identity (string equality)")
	}
}
