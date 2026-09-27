package shim

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDatetimeNowStrictForm pins the datetime('now') rule: only the
// exact shape the storage layer writes (modulo internal whitespace) is
// rewritten; lookalike forms must survive untouched.
func TestDatetimeNowStrictForm(t *testing.T) {
	const replacement = `to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')`

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "INSERT stamp (activity_repo.Create shape)",
			query: "INSERT INTO task_activity (id, payload, created_at) VALUES (?, ?, datetime('now'))",
			want:  "INSERT INTO task_activity (id, payload, created_at) VALUES ($1, $2, " + replacement + ")",
		},
		{
			name:  "UPDATE stamp (agent_repo.TouchLastSeen shape)",
			query: "UPDATE agents SET last_seen_at = datetime('now'), status = ? WHERE id = ?",
			want:  "UPDATE agents SET last_seen_at = " + replacement + ", status = $1 WHERE id = $2",
		},
		{
			name:  "two stamps in one statement (task_repo.Create shape)",
			query: "INSERT INTO tasks (id, number, created_at, updated_at) VALUES (?, ?, datetime('now'), datetime('now'))",
			want:  "INSERT INTO tasks (id, number, created_at, updated_at) VALUES ($1, $2, " + replacement + ", " + replacement + ")",
		},
		{
			name:  "DEFAULT DDL form (schema_migrations) is rewritten too",
			query: "CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))",
			want:  "CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (" + replacement + "))",
		},
		{
			name:  "internal whitespace tolerated",
			query: "INSERT INTO t (c) VALUES ( datetime( 'now' ) )",
			want:  "INSERT INTO t (c) VALUES ( " + replacement + " )",
		},
		{
			name:  "other argument value is NOT rewritten",
			query: "SELECT datetime('utcnow')",
			want:  "SELECT datetime('utcnow')",
		},
		{
			name:  "extra argument is NOT rewritten",
			query: "SELECT datetime('now', 'localtime')",
			want:  "SELECT datetime('now', 'localtime')",
		},
		{
			name:  "uppercase NOW is a different SQLite value and is NOT rewritten",
			query: "SELECT datetime('NOW')",
			want:  "SELECT datetime('NOW')",
		},
		{
			name:  "non-now function arguments untouched",
			query: "SELECT substr(created_at, 1, 10) FROM t",
			want:  "SELECT substr(created_at, 1, 10) FROM t",
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

// TestDatetimeNowFormatStaysLexicographicallyOrdered documents WHY the
// replacement emits exactly 'YYYY-MM-DD HH24:MI:SS': SQLite stores
// timestamps as TEXT in that 19-character zero-padded UTC layout, and
// the schema compares them as strings. For this layout string order is
// chronological order (fixed width, zero padding, most-significant
// field first), so the shim must neither widen nor reformat it. The
// live-PostgreSQL smoke test (pg_test.go) additionally asserts the
// produced value's shape and ordering on a real server.
func TestDatetimeNowFormatStaysLexicographicallyOrdered(t *testing.T) {
	assert.Equal(t,
		`to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')`,
		pgNowUTC,
		"replacement layout is load-bearing for TEXT timestamp comparisons",
	)
	// Every Go call site uses the bare strict form; the rule must keep
	// matching all of them (36 SQL occurrences across 16 files today).
	assert.True(t, datetimeNowRe.MatchString(`datetime('now')`))
	// The sqlite identity never fires the rule at all.
	got, err := rewriteFor(DialectSQLite, `datetime('now')`)
	require.NoError(t, err)
	assert.Equal(t, `datetime('now')`, got)
}

// TestInsertOrIgnoreRewrite pins the rule against the real Go call
// sites: chat_actor.go ×3, sync_ops_repo.go:38, task_repo.go SetTaskTags.
func TestInsertOrIgnoreRewrite(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name: "chat_actor.go users seed (literal VALUES, multi-line)",
			query: `INSERT OR IGNORE INTO users (id, email, password_hash, display_name, role)
			VALUES ('u-chat', 'chat-agent@orenda.local', 'unusable', 'Dashboard chat agent', 'system')`,
			want: `INSERT INTO users (id, email, password_hash, display_name, role)
			VALUES ('u-chat', 'chat-agent@orenda.local', 'unusable', 'Dashboard chat agent', 'system') ON CONFLICT DO NOTHING`,
		},
		{
			name: "sync_ops_repo.go:38 (placeholders plus empty literals)",
			query: `INSERT OR IGNORE INTO sync_ops (client_id, server_id, op, target)
		VALUES (?, ?, '', '')
	`,
			want: `INSERT INTO sync_ops (client_id, server_id, op, target)
		VALUES ($1, $2, '', '') ON CONFLICT DO NOTHING`,
		},
		{
			name:  "task_repo.go SetTaskTags join row",
			query: "INSERT OR IGNORE INTO task_tags (task_id, tag_id) VALUES (?, ?)",
			want:  "INSERT INTO task_tags (task_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		},
		{
			name:  "trailing semicolon is dropped with the appended clause",
			query: "INSERT OR IGNORE INTO t (a) VALUES (?) ;",
			want:  "INSERT INTO t (a) VALUES ($1) ON CONFLICT DO NOTHING",
		},
		{
			name:  "lowercase hand-written form matches too (keyword replaced, rest preserved)",
			query: "insert or ignore into t (a) values (?)",
			want:  "INSERT into t (a) values ($1) ON CONFLICT DO NOTHING",
		},
		{
			name:  "plain INSERT untouched",
			query: "INSERT INTO t (a) VALUES (?)",
			want:  "INSERT INTO t (a) VALUES ($1)",
		},
		{
			name:  "MySQL-style INSERT IGNORE untouched",
			query: "INSERT IGNORE INTO t (a) VALUES (?)",
			want:  "INSERT IGNORE INTO t (a) VALUES ($1)",
		},
		{
			name:  "insert_or_ignore inside an identifier is not matched",
			query: "SELECT * FROM insert_or_ignore_log",
			want:  "SELECT * FROM insert_or_ignore_log",
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

// ON CONFLICT DO NOTHING is statement-final in PostgreSQL; a query
// batching several INSERT OR IGNORE statements cannot be rewritten
// textually, so the shim rejects it loudly instead of mangling it.
func TestInsertOrIgnoreMultipleStatementsRejected(t *testing.T) {
	_, err := rewriteFor(DialectPostgres,
		"INSERT OR IGNORE INTO a (x) VALUES (1); INSERT OR IGNORE INTO b (y) VALUES (2)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INSERT OR IGNORE")
}

// A statement whose tail lies inside a comment would silently swallow
// the appended ON CONFLICT DO NOTHING clause (`… -- seed ON CONFLICT
// DO NOTHING` is all comment), so the shim rejects it loudly. A
// comment that properly closes before the end keeps the statement
// rewritable only when nothing follows it — the clause is
// statement-final, so any closed comment at the very end is equally
// rejected by the trim; only comments strictly inside the statement
// are fine.
func TestInsertOrIgnoreTrailingCommentRejected(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{
			name:    "trailing line comment swallows the clause",
			query:   "INSERT OR IGNORE INTO sync_ops (client_id) VALUES (?) -- seed",
			wantErr: true,
		},
		{
			name:    "line comment closed by newline, then trimmed off",
			query:   "INSERT OR IGNORE INTO sync_ops (client_id) VALUES (?) -- seed\n",
			wantErr: true,
		},
		{
			name:    "unterminated block comment at the tail",
			query:   "INSERT OR IGNORE INTO sync_ops (client_id) VALUES (?) /* seed",
			wantErr: true,
		},
		{
			name:    "comment inside a literal is not a comment region",
			query:   "INSERT OR IGNORE INTO t (note) VALUES ('a -- b')",
			wantErr: false,
		},
		{
			name:    "no trailing comment rewrites normally",
			query:   "INSERT OR IGNORE INTO t (a) VALUES (?)",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteFor(DialectPostgres, tt.query)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "ends inside a comment")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, got, "ON CONFLICT DO NOTHING")
			assert.NotContains(t, got, "INSERT OR IGNORE")
		})
	}
}

// TestAllRulesTogetherRewritesTaskCreate runs every rule over the
// biggest production statement (task_repo.Create): the 30 real
// placeholders renumber sequentially and both created_at/updated_at
// stamps rewrite.
func TestAllRulesTogetherRewritesTaskCreate(t *testing.T) {
	var b strings.Builder
	b.WriteString("INSERT INTO tasks (\n")
	b.WriteString("\t\tid, project_id, parent_task_id, column_id, title, description,\n")
	b.WriteString("\t\tstatus, priority, assignee_type, assignee_id, awaiting,\n")
	b.WriteString("\t\tcontext_md, agent_notes, due_at, started_at, claimed_at, completed_at,\n")
	b.WriteString("\t\ttime_estimate_s, time_spent_s, position,\n")
	b.WriteString("\t\tstart_at, end_at, all_day, color, recurrence,\n")
	b.WriteString("\t\tstudy_course_id,\n")
	b.WriteString("\t\tblocked_prev_status,\n")
	b.WriteString("\t\tnumber,\n")
	b.WriteString("\t\tcreated_by_type, created_by_id,\n")
	b.WriteString("\t\tcreated_at, updated_at\n")
	b.WriteString("\t) VALUES (\n")
	b.WriteString("\t\t?, ?, ?, ?, ?, ?,\n")
	b.WriteString("\t\t?, ?, ?, ?, ?,\n")
	b.WriteString("\t\t?, ?, ?, ?, ?, ?,\n")
	b.WriteString("\t\t?, ?, ?,\n")
	b.WriteString("\t\t?, ?, ?, ?, ?,\n")
	b.WriteString("\t\t?,\n")
	b.WriteString("\t\t?,\n")
	b.WriteString("\t\t?,\n")
	b.WriteString("\t\t?, ?,\n")
	b.WriteString("\t\tdatetime('now'), datetime('now')\n")
	b.WriteString("\t)")

	got, err := rewriteFor(DialectPostgres, b.String())
	require.NoError(t, err)

	assert.NotContains(t, got, "datetime('now')")
	assert.Equal(t, 2, strings.Count(got, "to_char(now() AT TIME ZONE 'UTC'"))
	assert.NotContains(t, got, "?")

	nums := placeholderNumbers(got)
	require.Len(t, nums, 30)
	for i, n := range nums {
		assert.Equal(t, i+1, n, "placeholders must stay sequential after the datetime rewrite")
	}
}
