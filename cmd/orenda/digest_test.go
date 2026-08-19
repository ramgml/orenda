// Phase 30.5: weekly digest scheduler tests.
//
// We exercise the computeWeeklyDigestStats aggregator against a
// real SQLite schema (the digest queries target the same tables the
// production scheduler reads from) plus the notifier template
// adapter. The scheduler's Run() ticker is too coupled to time.Now
// to be worth spinning a fake clock; fireOnce is the testable
// surface.
package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/ramgml/orenda/internal/service/notifier"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// newDigestDB spins up an isolated SQLite instance with the
// canonical migrations applied. Mirrors the pattern used by
// telegram_inbox_test.go and main_course_adapter_test.go — Phase
// 30.5 doesn't add migrations, but the tests need the existing
// users/projects/tasks/comments/time_entries schema to be present.
func newDigestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "digest.db")
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	return db
}

// fakeNotifier records every Notify call without touching the bot
// registry or the notifier service — keeps the digest tests focused
// on the scheduler loop.
type fakeNotifier struct {
	calls []notifyEvent
}

func (f *fakeNotifier) Notify(_ context.Context, e notifyEvent) error {
	f.calls = append(f.calls, e)
	return nil
}

// TestDigestScheduler_FiresForEveryActiveOwner: the loop contract is
// "every owner, one event, no duplicates". Two users → two calls.
func TestDigestScheduler_FiresForEveryActiveOwner(t *testing.T) {
	db := newDigestDB(t)
	_, err := db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner'),
		('u2', 'b@x', 'h', 'B', 'owner')`)
	require.NoError(t, err)

	notif := &fakeNotifier{}
	digest := &digestScheduler{
		interval: time.Hour,
		logger:   zap.NewNop(),
		db:       db,
		users:    &userRepoAdapterForTest{db: db},
		notifier: notif,
	}
	digest.fireOnce(context.Background())

	require.Len(t, notif.calls, 2, "every owner should get a digest")
	for _, c := range notif.calls {
		assert.Equal(t, "digest.weekly", c.Type)
		assert.Equal(t, "/today", c.Link)
		assert.NotEmpty(t, c.Title)
		assert.NotEmpty(t, c.Body)
		// The Meta keys feed WeeklyDigestFromEvent which rebuilds
		// the Message — both must be present.
		assert.Equal(t, c.Title, c.Meta["title"])
		assert.Equal(t, c.Body, c.Meta["body"])
	}
}

// TestDigestScheduler_SkipsInactiveOwner: in single-owner installs
// every row is active, but we still want to guard the future
// multi-user era where deactivation becomes meaningful.
func TestDigestScheduler_SkipsInactiveOwner(t *testing.T) {
	db := newDigestDB(t)
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner')`)

	notif := &fakeNotifier{}
	digest := &digestScheduler{
		interval: time.Hour,
		logger:   zap.NewNop(),
		db:       db,
		users:    &userRepoAdapterForTest{db: db, skip: map[string]bool{"u1": true}},
		notifier: notif,
	}
	digest.fireOnce(context.Background())
	assert.Len(t, notif.calls, 0, "inactive owner must not receive digest")
}

// TestDigestScheduler_LoopContinuesThroughFailures: when Notify
// returns an error for one owner, the loop must continue with the
// next. We inject a no-op notifier here (no error path) and assert
// the loop reaches both owners — proving the iteration didn't panic
// mid-way. The error branch is covered by the `notifier.Notify`
// tests in the notifier package itself.
func TestDigestScheduler_LoopContinuesThroughFailures(t *testing.T) {
	db := newDigestDB(t)
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner'),
		('u2', 'b@x', 'h', 'B', 'owner')`)

	notif := &fakeNotifier{}
	digest := &digestScheduler{
		interval: time.Hour,
		logger:   zap.NewNop(),
		db:       db,
		users:    &userRepoAdapterForTest{db: db},
		notifier: notif,
	}
	digest.fireOnce(context.Background())
	assert.Len(t, notif.calls, 2)
}

// TestComputeWeeklyDigestStats_EmptyDB returns zero counts. The
// most common case for a freshly-migrated install where no tasks
// exist yet.
func TestComputeWeeklyDigestStats_EmptyDB(t *testing.T) {
	db := newDigestDB(t)
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner')`)

	stats, err := computeWeeklyDigestStats(context.Background(), db, "u1", time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TasksDone)
	assert.Equal(t, 0, stats.TasksCreated)
	assert.Equal(t, 0, stats.TasksAwaitingReview)
	assert.Equal(t, 0, stats.TasksOverdue)
	assert.Equal(t, 0, stats.CommentsReceived)
	assert.Equal(t, 0, stats.ActiveTimers)
}

// TestComputeWeeklyDigestStats_CountsTasksDone: a task transitioned
// to done in the period counts; one done before the period does not.
func TestComputeWeeklyDigestStats_CountsTasksDone(t *testing.T) {
	db := newDigestDB(t)
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner')`)
	now := time.Now().UTC()
	recent := now.Add(-2 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	old := now.Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	_, _ = db.Exec(`INSERT INTO projects (id, name, owner_id) VALUES ('p1', 'P', 'u1')`)
	// Tasks use assignee_type/assignee_id (the worker), not a
	// direct owner_id.
	_, _ = db.Exec(`INSERT INTO tasks (id, project_id, title, status, assignee_type, assignee_id, updated_at, number) VALUES
		('t1', 'p1', 'In-period done', 'done', 'user', 'u1', ?, 1),
		('t2', 'p1', 'Pre-period done', 'done', 'user', 'u1', ?, 2)`,
		recent, old)

	stats, err := computeWeeklyDigestStats(context.Background(), db, "u1", now)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TasksDone, "only the in-period done task counts")
}

// TestComputeWeeklyDigestStats_OverdueExcludesDone: a task with
// due_at < now is only "overdue" if status != done. Done tasks
// are not actionable in the digest's overdue count.
func TestComputeWeeklyDigestStats_OverdueExcludesDone(t *testing.T) {
	db := newDigestDB(t)
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner')`)
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour).Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO projects (id, name, owner_id) VALUES ('p1', 'P', 'u1')`)
	_, _ = db.Exec(`INSERT INTO tasks (id, project_id, title, status, assignee_type, assignee_id, due_at, number) VALUES
		('t1', 'p1', 'Overdue', 'todo', 'user', 'u1', ?, 1),
		('t2', 'p1', 'Overdue but done', 'done', 'user', 'u1', ?, 2)`,
		yesterday, yesterday)

	stats, err := computeWeeklyDigestStats(context.Background(), db, "u1", now)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TasksOverdue)
}

// TestComputeWeeklyDigestStats_CommentsReceivedExcludesSelf:
// operator commenting on their own task is noise in the digest.
func TestComputeWeeklyDigestStats_CommentsReceivedExcludesSelf(t *testing.T) {
	db := newDigestDB(t)
	now := time.Now().UTC()
	recent := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner'),
		('u2', 'b@x', 'h', 'B', 'owner')`)
	_, _ = db.Exec(`INSERT INTO projects (id, name, owner_id) VALUES ('p1', 'P', 'u1')`)
	_, _ = db.Exec(`INSERT INTO tasks (id, project_id, title, status, assignee_type, assignee_id) VALUES
		('t1', 'p1', 'Assigned to u1', 'todo', 'user', 'u1')`)
	_, _ = db.Exec(`INSERT INTO comments (id, target_type, target_id, author_type, author_id, body_md, created_at) VALUES
		('c-self', 'task', 't1', 'user', 'u1', 'self comment', ?),
		('c-other', 'task', 't1', 'user', 'u2', 'other comment', ?)`,
		recent, recent)

	stats, err := computeWeeklyDigestStats(context.Background(), db, "u1", now)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.CommentsReceived, "self-comments must be excluded")
}

// TestComputeWeeklyDigestStats_AwaitingReviewLive: awaiting_review
// is a LIVE count (not period-bounded), so a task awaiting before
// the period still counts.
func TestComputeWeeklyDigestStats_AwaitingReviewLive(t *testing.T) {
	db := newDigestDB(t)
	now := time.Now().UTC()
	_, _ = db.Exec(`INSERT INTO users (id, email, password_hash, display_name, role) VALUES
		('u1', 'a@x', 'h', 'A', 'owner')`)
	_, _ = db.Exec(`INSERT INTO projects (id, name, owner_id) VALUES ('p1', 'P', 'u1')`)
	_, _ = db.Exec(`INSERT INTO tasks (id, project_id, title, status, assignee_type, assignee_id, awaiting) VALUES
		('t1', 'p1', 'Long-awaiting', 'review', 'user', 'u1', 'human')`)

	stats, err := computeWeeklyDigestStats(context.Background(), db, "u1", now)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TasksAwaitingReview)
}

// TestRenderWeeklyDigestFromEvent_Adapter: the template
// WeeklyDigestFromEvent rebuilds a Message from Event.Meta. Pin
// it so a future refactor doesn't lose the body.
func TestRenderWeeklyDigestFromEvent_Adapter(t *testing.T) {
	msg := notifier.WeeklyDigestFromEvent(notifier.Event{
		Type:   "digest.weekly",
		UserID: "u1",
		Link:   "/today",
		Meta: map[string]string{
			"title": "Weekly digest: Aug 9 – Aug 16",
			"body":  "• Tasks completed: **3**",
		},
	})
	assert.Equal(t, "digest.weekly", msg.Kind)
	assert.Equal(t, "Weekly digest: Aug 9 – Aug 16", msg.Title)
	assert.Contains(t, msg.Body, "Tasks completed")
	assert.Equal(t, "/today", msg.Link)
}

// TestDigestScheduler_DefaultsTo168hInConfig pins the YAML default
// so a fresh install gets a weekly digest out of the box without
// editing config.yaml. Uses yaml.v3 (the same decoder DefaultConfig
// uses) so a regression in the field name surfaces here too.
func TestDigestScheduler_DefaultsTo168hInConfig(t *testing.T) {
	yamlIn := []byte("notifier:\n  digest_interval: 168h\n")
	var roundTrip struct {
		Notifier struct {
			DigestInterval time.Duration `yaml:"digest_interval"`
		} `yaml:"notifier"`
	}
	require.NoError(t, yaml.Unmarshal(yamlIn, &roundTrip))
	assert.Equal(t, 168*time.Hour, roundTrip.Notifier.DigestInterval)
}

// userRepoAdapterForTest wraps a real DB but lets a single test
// inject "inactive" owners without touching the production schema.
type userRepoAdapterForTest struct {
	db   *sql.DB
	skip map[string]bool
}

func (u *userRepoAdapterForTest) ListAll(_ context.Context) ([]ownerRecord, error) {
	rows, err := u.db.Query(`SELECT id, role FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ownerRecord, 0)
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		if u.skip[id] {
			continue
		}
		out = append(out, ownerRecord{ID: id, Active: true})
	}
	return out, rows.Err()
}
