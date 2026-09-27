package shim

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
	"github.com/ramgml/orenda/internal/domain/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// This is the DoD integration smoke for the dialect shim (T362): real
// repository queries, unchanged, running through the shim on a live
// PostgreSQL server. Every passing call below doubles as syntax-level
// proof of the rewrite — PostgreSQL rejects a stray `?` placeholder or
// datetime('now') outright, so a successful prepare/exec means the
// rewritten SQL reached the server.
//
// The test is gated: set ORENDA_TEST_PG_DSN (e.g.
// postgres://postgres:test@127.0.0.1:21543/postgres?sslmode=disable)
// against a throwaway server:
//
//	docker run --rm -d --name orenda-t362-pg -e POSTGRES_PASSWORD=test \
//	  -p 127.0.0.1:21543:5432 postgres:16-alpine
//
// The schema below is a minimal stand-in (TEXT stamps, no indexes,
// triggers or defaults) — the real postgres baseline belongs to T361.
func TestPostgresSmoke_RepositoryQueriesThroughShim(t *testing.T) {
	dsn := os.Getenv("ORENDA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("integration smoke: set ORENDA_TEST_PG_DSN to a throwaway postgres (see test comment)")
	}

	pgCfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	// The shim sits between database/sql and the pgx stdlib connector —
	// this is the exact wiring T361's storage.Open postgres branch uses.
	base := stdlib.GetConnector(*pgCfg)
	db := sql.OpenDB(NewConnector(base, DialectPostgres))
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	require.NoError(t, db.PingContext(ctx))
	createMinimalSchema(t, ctx, db)

	// --- INSERT OR IGNORE (chat_actor.go ×3): idempotent seed --------
	require.NoError(t, sqlite.EnsureChatActor(ctx, db))
	require.NoError(t, sqlite.EnsureChatActor(ctx, db), "second run must stay a no-op (OR IGNORE)")
	var users int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE id = 'u-chat'").Scan(&users))
	assert.Equal(t, 1, users, "OR IGNORE must not duplicate the seed row")

	// --- INSERT OR IGNORE (sync_ops_repo.go:38) ----------------------
	syncOps := sqlite.NewSyncOpsRepository(db)
	require.NoError(t, syncOps.Record(ctx, "client-1", "server-1"))
	require.NoError(t, syncOps.Record(ctx, "client-1", "server-2"), "duplicate client must be ignored")
	seen, serverID, err := syncOps.Seen(ctx, "client-1")
	require.NoError(t, err)
	assert.True(t, seen)
	assert.Equal(t, "server-1", serverID, "the ignored duplicate must not overwrite")

	// --- upsert ON CONFLICT(key) DO UPDATE .. excluded (audit item) --
	settings := sqlite.NewBackupSettingsRepository(db)
	require.NoError(t, settings.SetKey(ctx, "backup.github", []byte(`{"v":1}`)))
	require.NoError(t, settings.SetKey(ctx, "backup.github", []byte(`{"v":2}`)))
	raw, found, err := settings.GetByKey(ctx, "backup.github")
	require.NoError(t, err)
	assert.True(t, found)
	assert.JSONEq(t, `{"v":2}`, string(raw), "excluded.value must win on conflict")
	require.NoError(t, settings.ClearByKey(ctx, "backup.github"))
	_, found, err = settings.GetByKey(ctx, "backup.github")
	require.NoError(t, err)
	assert.False(t, found)

	// --- ON CONFLICT(user_id, thread_id) DO NOTHING (audit item) -----
	threads := sqlite.NewChatThreadRepository(db)
	require.NoError(t, threads.Upsert(ctx, "user-1", "thread-1"))
	require.NoError(t, threads.Upsert(ctx, "user-1", "thread-1"))
	owned, err := threads.Owned(ctx, "user-1", "thread-1")
	require.NoError(t, err)
	assert.True(t, owned)

	// --- RETURNING number-seq + datetime stamps: project -------------
	projects := sqlite.NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(ctx, &project.Project{Name: "Orenda", OwnerID: "owner-1"})
	require.NoError(t, err)
	require.NotEmpty(t, cols)
	assert.Equal(t, 1, p.Number, "project_number_seq RETURNING must draw 1")
	assert.False(t, p.CreatedAt.IsZero(), "datetime('now') stamp must be rewritten and parsed")

	// --- tasks: 30-placeholder INSERT, RETURNING seq, dynamic UPDATE -
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{
		ProjectID:     p.ID,
		ColumnID:      cols[0].ID,
		Title:         "Implement login",
		Status:        task.StatusBacklog,
		CreatedByType: task.CreatorAgent,
		CreatedByID:   "agent-1",
	}
	require.NoError(t, tasks.Create(ctx, tr))
	assert.Equal(t, 1, tr.Number, "task_number_seq RETURNING must draw 1")

	got, err := tasks.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	assert.Equal(t, tr.Title, got.Title)
	assert.False(t, got.CreatedAt.IsZero())

	byNumber, err := tasks.GetByNumber(ctx, tr.Number)
	require.NoError(t, err)
	assert.Equal(t, tr.ID, byNumber.ID)

	// Conditional WHERE + IN list (task_repo ListByProject / IN builders).
	listed, err := tasks.ListByProject(ctx, task.Filter{
		ProjectID: p.ID,
		IDs:       []string{tr.ID, "missing-1", "missing-2"},
		Status:    task.StatusBacklog,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, tr.ID, listed[0].ID)

	// Dynamic SET assembly + updated_at = datetime('now').
	newTitle := "Implement login (scoped)"
	require.NoError(t, tasks.UpdateProposalFields(ctx, task.ProposalPatchParams{
		TaskID: tr.ID,
		Gate:   task.ProposalGate{CreatedByID: "agent-1"},
		Title:  &newTitle,
	}))
	patched, err := tasks.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	assert.Equal(t, newTitle, patched.Title)

	// --- activity: datetime INSERT + IN-list query -------------------
	acts := sqlite.NewActivityRepository(db)
	require.NoError(t, acts.Create(ctx, &activity.Activity{
		TaskID:  tr.ID,
		ActorID: "agent-1",
		Action:  activity.ActionStatusChanged,
		Payload: `{"from":"todo","to":"backlog"}`,
	}))
	changes, err := acts.StatusChangesByTasks(ctx, []string{tr.ID, "missing-1"})
	require.NoError(t, err)
	require.Len(t, changes[tr.ID], 1)

	// --- agent: UPDATE ... datetime('now') ---------------------------
	agents := sqlite.NewAgentRepository(db)
	touched, err := agents.TouchLastSeen(ctx, "chat")
	require.NoError(t, err)
	assert.Equal(t, agent.StatusOnline, touched.Status)
	assert.NotNil(t, touched.LastSeenAt)

	// --- courses: two more RETURNING seq tables + quiz position ------
	courses := sqlite.NewCourseRepository(db)
	c := &course.Course{
		Title:   "Dialects 101",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "casual",
		OwnerID: "owner-1",
	}
	require.NoError(t, courses.CreateCourse(ctx, c))
	assert.Equal(t, 1, c.Number)

	lesson := &course.Lesson{
		ModuleID: "module-1",
		Title:    "Placeholders",
		Status:   course.LessonOpen,
	}
	require.NoError(t, courses.CreateLesson(ctx, lesson))
	assert.Equal(t, 1, lesson.Number, "lesson_number_seq RETURNING must draw 1")

	quiz := &course.Quiz{
		LessonID:   lesson.ID,
		QuestionMD: "What is $1?",
		ExpectedMD: "a placeholder",
		Kind:       course.QuizOpen,
	}
	require.NoError(t, courses.CreateQuiz(ctx, quiz))
	assert.Equal(t, 1, quiz.Position, "INSERT..RETURNING position must append atomically")

	// --- wiki: RETURNING seq + datetime pair --------------------------
	wikis := sqlite.NewWikiRepository(db)
	page, err := wikis.Create(ctx, &wiki.Page{Slug: "home", Title: "Home"})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Number, "wiki_page_number_seq RETURNING must draw 1")
	assert.False(t, page.CreatedAt.IsZero())

	// --- time entries: IN-list lookup ---------------------------------
	entries := sqlite.NewTimeEntryRepository(db)
	entry, err := entries.Create(ctx, &timeentry.TimeEntry{
		TaskID:    tr.ID,
		AgentID:   "agent-1",
		StartedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.False(t, entry.ID == "")
	hasAny, err := entries.HasAnyEntriesByTasks(ctx, []string{tr.ID, "missing-1"})
	require.NoError(t, err)
	assert.True(t, hasAny[tr.ID])

	// --- trailing " LIMIT ?" appends on empty tables ------------------
	pa := sqlite.NewProjectActivityRepository(db)
	projRows, err := pa.ListByProject(ctx, p.ID, 5)
	require.NoError(t, err, "LIMIT ? append must prepare/exec cleanly")
	assert.Empty(t, projRows)
	ca := sqlite.NewCourseActivityRepository(db)
	courseRows, err := ca.ListByCourse(ctx, c.ID, 5)
	require.NoError(t, err, "LIMIT ? append must prepare/exec cleanly")
	assert.Empty(t, courseRows)

	// --- datetime('now') output shape and lexicographic semantics ----
	var createdAt string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT created_at FROM tasks WHERE id = ?", tr.ID).Scan(&createdAt))
	assert.Regexp(t, regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`),
		createdAt, "stamp must keep SQLite's 19-char UTC layout")

	// The layout is the comparability contract: TEXT order == time
	// order, exactly like SQLite's datetime('now') values.
	_, err = db.ExecContext(ctx,
		"INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?)",
		"act-old", tr.ID, "user", "owner-1", "probe", "", "2026-01-01 10:00:00")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		"INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?)",
		"act-new", tr.ID, "user", "owner-1", "probe", "", "2026-01-02 10:00:00")
	require.NoError(t, err)
	var ids []string
	rows, err := db.QueryContext(ctx,
		"SELECT id FROM task_activity WHERE action = ? AND created_at > ? ORDER BY created_at",
		"probe", "2026-01-01 10:00:00")
	require.NoError(t, err)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"act-new"}, ids,
		"lexicographic comparison on the to_char layout must equal chronological comparison")
}

// createMinimalSchema drops and recreates the smallest PostgreSQL
// schema the exercised repository methods touch. Statements are sent
// one per Exec: the extended protocol (pgx) rejects multi-statement
// batches. TEXT is used for every timestamp so the shim's stamp layout
// is preserved verbatim; DOUBLE PRECISION carries the SQLite REAL
// positions. The applied_at default is written in the shim's own
// rewritten form — valid PostgreSQL, same semantics as SQLite's
// DEFAULT (datetime('now')).
func createMinimalSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	now := `to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')`
	ddl := []string{
		`DROP TABLE IF EXISTS course_activity CASCADE`,
		`DROP TABLE IF EXISTS project_activity CASCADE`,
		`DROP TABLE IF EXISTS time_entries CASCADE`,
		`DROP TABLE IF EXISTS wiki_pages CASCADE`,
		`DROP TABLE IF EXISTS course_quizzes CASCADE`,
		`DROP TABLE IF EXISTS course_lessons CASCADE`,
		`DROP TABLE IF EXISTS courses CASCADE`,
		`DROP TABLE IF EXISTS task_activity CASCADE`,
		`DROP TABLE IF EXISTS tasks CASCADE`,
		`DROP TABLE IF EXISTS columns CASCADE`,
		`DROP TABLE IF EXISTS boards CASCADE`,
		`DROP TABLE IF EXISTS projects CASCADE`,
		`DROP TABLE IF EXISTS chat_threads CASCADE`,
		`DROP TABLE IF EXISTS backup_settings CASCADE`,
		`DROP TABLE IF EXISTS sync_ops CASCADE`,
		`DROP TABLE IF EXISTS agents CASCADE`,
		`DROP TABLE IF EXISTS api_tokens CASCADE`,
		`DROP TABLE IF EXISTS users CASCADE`,
		`DROP TABLE IF EXISTS task_number_seq CASCADE`,
		`DROP TABLE IF EXISTS project_number_seq CASCADE`,
		`DROP TABLE IF EXISTS course_number_seq CASCADE`,
		`DROP TABLE IF EXISTS lesson_number_seq CASCADE`,
		`DROP TABLE IF EXISTS wiki_page_number_seq CASCADE`,

		`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT, password_hash TEXT, display_name TEXT, role TEXT, created_at TEXT DEFAULT (` + now + `), updated_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE api_tokens (id TEXT PRIMARY KEY, user_id TEXT, name TEXT, hash TEXT, scopes TEXT, expires_at TEXT, created_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE agents (id TEXT PRIMARY KEY, name TEXT, type TEXT, description TEXT, token_id TEXT, last_seen_at TEXT, status TEXT, max_concurrent INTEGER, created_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE sync_ops (client_id TEXT, server_id TEXT, op TEXT, target TEXT, applied_at TEXT NOT NULL DEFAULT (` + now + `))`,
		`CREATE TABLE backup_settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE chat_threads (id TEXT, user_id TEXT, thread_id TEXT, UNIQUE (user_id, thread_id))`,
		`CREATE TABLE project_number_seq (id INTEGER PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, number INTEGER, name TEXT, color TEXT, description TEXT, wiki_slug TEXT, owner_id TEXT, archived INTEGER, agents_allowed INTEGER, created_at TEXT DEFAULT (` + now + `), updated_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE boards (id TEXT PRIMARY KEY, project_id TEXT, name TEXT, position DOUBLE PRECISION, created_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE columns (id TEXT PRIMARY KEY, board_id TEXT, name TEXT, position DOUBLE PRECISION, status TEXT)`,
		`CREATE TABLE task_number_seq (id INTEGER PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, number INTEGER, project_id TEXT, parent_task_id TEXT, column_id TEXT, title TEXT, description TEXT, status TEXT, priority TEXT, assignee_type TEXT, assignee_id TEXT, awaiting TEXT, context_md TEXT, agent_notes TEXT, due_at TEXT, started_at TEXT, claimed_at TEXT, completed_at TEXT, time_estimate_s INTEGER, time_spent_s INTEGER, position DOUBLE PRECISION, start_at TEXT, end_at TEXT, all_day INTEGER, color TEXT, recurrence TEXT, study_course_id TEXT, blocked_prev_status TEXT, created_by_type TEXT, created_by_id TEXT, created_at TEXT DEFAULT (` + now + `), updated_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE task_activity (id TEXT PRIMARY KEY, task_id TEXT, actor_type TEXT, actor_id TEXT, action TEXT, payload TEXT, created_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE course_number_seq (id INTEGER PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE courses (id TEXT PRIMARY KEY, number INTEGER, title TEXT, intent_md TEXT, level TEXT, pace TEXT, status TEXT, owner_id TEXT, generator_task_id TEXT, pace_notes_md TEXT, created_at TEXT DEFAULT (` + now + `), updated_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE lesson_number_seq (id INTEGER PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE course_lessons (id TEXT PRIMARY KEY, module_id TEXT, title TEXT, content_md TEXT, status TEXT, position INTEGER, task_id TEXT, number INTEGER)`,
		`CREATE TABLE course_quizzes (id TEXT PRIMARY KEY, lesson_id TEXT, position INTEGER, question_md TEXT, expected_md TEXT, kind TEXT)`,
		`CREATE TABLE wiki_page_number_seq (id INTEGER PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE wiki_pages (id TEXT PRIMARY KEY, parent_id TEXT, slug TEXT, title TEXT, content_md TEXT, content_format TEXT, position DOUBLE PRECISION, number INTEGER, created_at TEXT DEFAULT (` + now + `), updated_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE time_entries (id TEXT PRIMARY KEY, task_id TEXT, agent_id TEXT, started_at TEXT, ended_at TEXT, duration_s INTEGER, source TEXT)`,
		`CREATE TABLE project_activity (id TEXT, project_id TEXT, actor_type TEXT, actor_id TEXT, kind TEXT, payload TEXT, created_at TEXT DEFAULT (` + now + `))`,
		`CREATE TABLE course_activity (id TEXT, course_id TEXT, actor_type TEXT, actor_id TEXT, kind TEXT, payload TEXT, created_at TEXT DEFAULT (` + now + `))`,

		`INSERT INTO task_number_seq (id, next) VALUES (1, 1)`,
		`INSERT INTO project_number_seq (id, next) VALUES (1, 1)`,
		`INSERT INTO course_number_seq (id, next) VALUES (1, 1)`,
		`INSERT INTO lesson_number_seq (id, next) VALUES (1, 1)`,
		`INSERT INTO wiki_page_number_seq (id, next) VALUES (1, 1)`,
	}
	for _, stmt := range ddl {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err, "schema statement: %s", stmt)
	}
}
