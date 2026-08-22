// Package sqlite — Phase 18 course repository.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

type courseRepo struct {
	db *sql.DB
}

// parseTimeLite parses an SQLite timestamp string (UTC) into time.Time.
// Tolerates an empty string by returning the zero time.
func parseTimeLite(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// NewCourseRepository returns a course.Repository backed by db.
func NewCourseRepository(db *sql.DB) course.Repository {
	return &courseRepo{db: db}
}

func (r *courseRepo) CreateCourse(ctx context.Context, c *course.Course) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ID == "" {
		c.ID = newUUID()
	}

	// number comes from the course_number_seq high-watermark, not from
	// MAX(courses.number): a MAX+1 would re-issue the newest course's
	// number after that course is deleted, and a "C7" reference in a
	// commit message, branch name or PR title must keep pointing at
	// the same course forever. The watermark UPDATE...RETURNING and the
	// INSERT share one transaction, so the draw is atomic and the
	// sequence can never run backwards.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("course.CreateCourse: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var number int
	if err := tx.QueryRowContext(ctx,
		`UPDATE course_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&number); err != nil {
		return fmt.Errorf("course.CreateCourse: draw number: %w", err)
	}

	const q = `INSERT INTO courses
		(id, number, title, intent_md, level, pace, status, owner_id, generator_task_id,
		 pace_notes_md, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?,
	        ?, datetime('now'), datetime('now'))`
	_, err = tx.ExecContext(ctx, q,
		c.ID, number, c.Title, c.IntentMD, c.Level, c.Pace, string(c.Status),
		c.OwnerID, nullString(c.GeneratorTaskID),
		c.PaceNotesMD,
	)
	if err != nil {
		return fmt.Errorf("course.CreateCourse: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("course.CreateCourse: commit: %w", err)
	}
	got, err := r.GetCourse(ctx, c.ID)
	if err != nil {
		return err
	}
	*c = *got
	return nil
}

func (r *courseRepo) GetCourse(ctx context.Context, id string) (*course.Course, error) {
	const q = `SELECT id, number, title, intent_md, level, pace, status, owner_id,
		COALESCE(generator_task_id, ''), COALESCE(pace_notes_md, ''),
		created_at, updated_at
		FROM courses WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var c course.Course
	var status, created, updated string
	if err := row.Scan(&c.ID, &c.Number, &c.Title, &c.IntentMD, &c.Level, &c.Pace,
		&status, &c.OwnerID, &c.GeneratorTaskID, &c.PaceNotesMD, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.Get: %w", err)
	}
	c.Status = course.Status(status)
	c.CreatedAt = parseTimeLite(created)
	c.UpdatedAt = parseTimeLite(updated)
	return &c, nil
}

// GetCourseByNumber resolves the human-readable "C<N>" reference to a course.
// The UNIQUE index idx_courses_number (migration 038) makes this an
// index point lookup.
func (r *courseRepo) GetCourseByNumber(ctx context.Context, number int) (*course.Course, error) {
	const q = `SELECT id, number, title, intent_md, level, pace, status, owner_id,
		COALESCE(generator_task_id, ''), COALESCE(pace_notes_md, ''),
		created_at, updated_at
		FROM courses WHERE number = ?`
	row := r.db.QueryRowContext(ctx, q, number)
	var c course.Course
	var status, created, updated string
	if err := row.Scan(&c.ID, &c.Number, &c.Title, &c.IntentMD, &c.Level, &c.Pace,
		&status, &c.OwnerID, &c.GeneratorTaskID, &c.PaceNotesMD, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetCourseByNumber: %w", err)
	}
	c.Status = course.Status(status)
	c.CreatedAt = parseTimeLite(created)
	c.UpdatedAt = parseTimeLite(updated)
	return &c, nil
}

func (r *courseRepo) ListCourses(ctx context.Context, ownerID string) ([]*course.Course, error) {
	// Empty ownerID means "list all courses". The single-owner
	// architecture (Phase 32.12 follow-on) intentionally relaxes
	// the filter when the caller has no usable scope — the agent
	// middleware authenticates the bearer token but the token's
	// user_id is a synthetic "agent-owner" user, not the human
	// owner who created the course. The /agent/* handlers are
	// tutor-side; the human owner's id is the only meaningful
	// scope, and we don't track created_by on agents. A multi-user
	// future would reintroduce the filter.
	var query string
	var args []any
	if ownerID == "" {
		query = `SELECT id, number, title, intent_md, level, pace, status, owner_id,
		        COALESCE(generator_task_id, ''), COALESCE(pace_notes_md, ''),
		        created_at, updated_at
		 FROM courses ORDER BY updated_at DESC`
	} else {
		query = `SELECT id, number, title, intent_md, level, pace, status, owner_id,
		        COALESCE(generator_task_id, ''), COALESCE(pace_notes_md, ''),
		        created_at, updated_at
		 FROM courses WHERE owner_id = ? ORDER BY updated_at DESC`
		args = []any{ownerID}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("course.List: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Course, 0)
	for rows.Next() {
		var c course.Course
		var status, created, updated string
		if err := rows.Scan(&c.ID, &c.Number, &c.Title, &c.IntentMD, &c.Level, &c.Pace,
			&status, &c.OwnerID, &c.GeneratorTaskID, &c.PaceNotesMD, &created, &updated); err != nil {
			return nil, err
		}
		c.Status = course.Status(status)
		c.CreatedAt = parseTimeLite(created)
		c.UpdatedAt = parseTimeLite(updated)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *courseRepo) UpdateCourse(ctx context.Context, c *course.Course) error {
	if err := c.Validate(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE courses SET title=?, intent_md=?, level=?, pace=?, status=?,
		    owner_id=?, generator_task_id=?, pace_notes_md=?,
		    updated_at=datetime('now')
		 WHERE id = ?`,
		c.Title, c.IntentMD, c.Level, c.Pace, string(c.Status),
		c.OwnerID, nullString(c.GeneratorTaskID), c.PaceNotesMD, c.ID,
	)
	if err != nil {
		return fmt.Errorf("course.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// UpdatePaceNotesMD is the narrow PATCH endpoint the agent-planner
// uses to write pace_notes_md on an existing course (Phase 31). It
// doesn't touch title/status/etc — those flow through UpdateCourse
// when the human edits the course via the UI. Validate runs through
// the Course struct so the cap and trim normalisation apply.
func (r *courseRepo) UpdatePaceNotesMD(ctx context.Context, id, notes string) error {
	tmp := &course.Course{ID: id, Title: "x", Status: course.StatusDraft, PaceNotesMD: notes}
	if err := tmp.Validate(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE courses SET pace_notes_md = ?, updated_at = datetime('now') WHERE id = ?`,
		tmp.PaceNotesMD, id)
	if err != nil {
		return fmt.Errorf("course.UpdatePaceNotesMD: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

func (r *courseRepo) DeleteCourse(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM courses WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("course.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return course.ErrNotFound
	}
	return nil
}

func (r *courseRepo) CreateModule(ctx context.Context, m *course.Module) error {
	if m.ID == "" {
		m.ID = newUUID()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO course_modules (id, course_id, title, description, position)
		 VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.CourseID, m.Title, m.Description, m.Position,
	)
	if err != nil {
		return fmt.Errorf("course.CreateModule: %w", err)
	}
	return nil
}

// GetModule loads a single module by id. Phase 30.13 uses it to
// walk module → course when gating granular edits on course status.
func (r *courseRepo) GetModule(ctx context.Context, id string) (*course.Module, error) {
	const q = `SELECT id, course_id, title, description, position
		FROM course_modules WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var m course.Module
	if err := row.Scan(&m.ID, &m.CourseID, &m.Title, &m.Description, &m.Position); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetModule: %w", err)
	}
	return &m, nil
}

// UpdateModule writes title and description in place. Position is
// deliberately not written here — ApplyStructure owns ordering so a
// rename can never clobber a concurrent reorder's positions.
func (r *courseRepo) UpdateModule(ctx context.Context, m *course.Module) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE course_modules SET title=?, description=? WHERE id = ?`,
		m.Title, m.Description, m.ID,
	)
	if err != nil {
		return fmt.Errorf("course.UpdateModule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// DeleteModule removes the module; lessons and quizzes cascade
// (migration 019 FK ON DELETE CASCADE).
func (r *courseRepo) DeleteModule(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM course_modules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("course.DeleteModule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

func (r *courseRepo) ListModules(ctx context.Context, courseID string) ([]*course.Module, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, course_id, title, description, position FROM course_modules
		 WHERE course_id = ? ORDER BY position ASC, id ASC`,
		courseID)
	if err != nil {
		return nil, fmt.Errorf("course.ListModules: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Module, 0)
	for rows.Next() {
		var m course.Module
		if err := rows.Scan(&m.ID, &m.CourseID, &m.Title, &m.Description, &m.Position); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (r *courseRepo) CreateLesson(ctx context.Context, l *course.Lesson) error {
	if l.ID == "" {
		l.ID = newUUID()
	}

	// number comes from the lesson_number_seq high-watermark (same
	// pattern as course_number_seq and task_number_seq — never reuse
	// after delete).
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("course.CreateLesson: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var number int
	if err := tx.QueryRowContext(ctx,
		`UPDATE lesson_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&number); err != nil {
		return fmt.Errorf("course.CreateLesson: draw number: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, content_md, status, position, task_id, number)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.ModuleID, l.Title, l.ContentMD, string(l.Status),
		l.Position, nullString(l.TaskID), number,
	)
	if err != nil {
		return fmt.Errorf("course.CreateLesson: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("course.CreateLesson: commit: %w", err)
	}
	l.Number = number
	return nil
}

func (r *courseRepo) ListLessons(ctx context.Context, moduleID string) ([]*course.Lesson, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, module_id, title, content_md, status, position, COALESCE(task_id, ''), number
		 FROM course_lessons WHERE module_id = ? ORDER BY position ASC, id ASC`,
		moduleID)
	if err != nil {
		return nil, fmt.Errorf("course.ListLessons: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Lesson, 0)
	for rows.Next() {
		var l course.Lesson
		var status string
		if err := rows.Scan(&l.ID, &l.ModuleID, &l.Title, &l.ContentMD,
			&status, &l.Position, &l.TaskID, &l.Number); err != nil {
			return nil, err
		}
		l.Status = course.LessonStatus(status)
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *courseRepo) ListLessonsInCourse(ctx context.Context, courseID string) ([]*course.Lesson, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT l.id, l.module_id, l.title, l.content_md, l.status, l.position, COALESCE(l.task_id, ''), l.number
		 FROM course_lessons l
		 JOIN course_modules m ON m.id = l.module_id
		 WHERE m.course_id = ?
		 ORDER BY m.position ASC, l.position ASC, l.id ASC`,
		courseID)
	if err != nil {
		return nil, fmt.Errorf("course.ListLessonsInCourse: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Lesson, 0)
	for rows.Next() {
		var l course.Lesson
		var status string
		if err := rows.Scan(&l.ID, &l.ModuleID, &l.Title, &l.ContentMD,
			&status, &l.Position, &l.TaskID, &l.Number); err != nil {
			return nil, err
		}
		l.Status = course.LessonStatus(status)
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *courseRepo) GetLesson(ctx context.Context, id string) (*course.Lesson, error) {
	const q = `SELECT id, module_id, title, content_md, status, position, COALESCE(task_id, ''), number
		FROM course_lessons WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var l course.Lesson
	var status string
	if err := row.Scan(&l.ID, &l.ModuleID, &l.Title, &l.ContentMD,
		&status, &l.Position, &l.TaskID, &l.Number); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetLesson: %w", err)
	}
	l.Status = course.LessonStatus(status)
	return &l, nil
}

// GetLessonByNumber resolves the human-readable "L<N>" reference to a lesson.
// The UNIQUE index idx_lessons_number (migration 039) makes this an
// index point lookup.
func (r *courseRepo) GetLessonByNumber(ctx context.Context, number int) (*course.Lesson, error) {
	const q = `SELECT id, module_id, title, content_md, status, position, COALESCE(task_id, ''), number
		FROM course_lessons WHERE number = ?`
	row := r.db.QueryRowContext(ctx, q, number)
	var l course.Lesson
	var status string
	if err := row.Scan(&l.ID, &l.ModuleID, &l.Title, &l.ContentMD,
		&status, &l.Position, &l.TaskID, &l.Number); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetLessonByNumber: %w", err)
	}
	l.Status = course.LessonStatus(status)
	return &l, nil
}

func (r *courseRepo) UpdateLesson(ctx context.Context, l *course.Lesson) error {
	// Phase 32.12: completed_at is stamped when the lesson transitions
	// to Done. UpdateLesson is the single write path for lesson state,
	// so we keep the timestamp logic here rather than introducing a
	// second method (smaller blast radius — see CompleteLesson in
	// service/course/course.go for the open → done transition).
	var completedArg interface{}
	if l.CompletedAt != nil {
		completedArg = l.CompletedAt.UTC().Format(time.RFC3339)
	} else {
		completedArg = nil
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE course_lessons SET title=?, content_md=?, status=?, position=?, task_id=?, completed_at=?
		 WHERE id = ?`,
		l.Title, l.ContentMD, string(l.Status), l.Position, nullString(l.TaskID), completedArg, l.ID,
	)
	if err != nil {
		return fmt.Errorf("course.UpdateLesson: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// MarkLessonDone atomically transitions a lesson to status='done' and
// stamps completed_at. Phase 32.12: the lesson completion timestamp
// is what feeds the rolling velocity classifier for the agent-side
// course list. Pre-migration rows have NULL completed_at; the
// classifier treats those as "before the window" and excludes them
// from the rolling count, which is conservative (planner sees slower
// pace than reality rather than faster).
//
// Idempotent: re-running on an already-done lesson is a no-op on
// status but updates completed_at. That's intentional — letting the
// service clock overwrite an old completion timestamp lets a workflow
// "refresh" the timestamp (rare; documented for the operationally
// curious). The repo also tolerates a status that wasn't 'open' as
// long as the lesson exists; the service layer's CompleteLesson
// gates the transition (status must be 'open' before flip).
func (r *courseRepo) MarkLessonDone(ctx context.Context, lessonID string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE course_lessons
		 SET status = ?, completed_at = ?
		 WHERE id = ?`,
		string(course.LessonDone), at.UTC().Format(time.RFC3339), lessonID,
	)
	if err != nil {
		return fmt.Errorf("course.MarkLessonDone: %w", err)
	}
	return nil
}

// VelocityStatsByCourse returns the rolling velocity for a course
// (Phase 32.12). "Done" rows with NULL completed_at are skipped
// (pre-migration 025 data) — counted as zero contribution to the
// rolling window. Done rows whose completed_at is older than `since`
// are also skipped.
//
// Returns domain.VelocityStats so callers (handlers / drift
// classifier / SKILL.md-described planner) consume a single named
// type across the codebase; the storage layer is the only place
// that knows about SQL.
func (r *courseRepo) VelocityStatsByCourse(ctx context.Context, courseID string, since time.Time) (course.VelocityStats, error) {
	out := course.VelocityStats{
		Since:  since,
		Window: 14 * 24 * time.Hour,
	}
	var (
		count   int
		lastStr sql.NullString
	)
	row := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(completed_at)
		 FROM course_lessons l
		 JOIN course_modules m ON m.id = l.module_id
		 WHERE m.course_id = ?
		   AND l.status = ?
		   AND l.completed_at IS NOT NULL
		   AND l.completed_at >= ?`,
		courseID, string(course.LessonDone), since.UTC().Format(time.RFC3339),
	)
	if err := row.Scan(&count, &lastStr); err != nil {
		return out, fmt.Errorf("course.VelocityStats: scan: %w", err)
	}
	out.LessonsDoneInWindow = count
	if lastStr.Valid && lastStr.String != "" {
		ts, perr := time.Parse(time.RFC3339, lastStr.String)
		if perr != nil {
			return out, fmt.Errorf("course.VelocityStats: parse last_completed_at %q: %w", lastStr.String, perr)
		}
		out.LastCompletedAt = &ts
	}
	return out, nil
}

// DeleteLesson removes the lesson; its quizzes cascade. The row is
// gone, so Progress counts shrink accordingly — deleting content is
// an explicit act by the owner/tutor.
func (r *courseRepo) DeleteLesson(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM course_lessons WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("course.DeleteLesson: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// UpdateLessonContent writes content_md / status / task_id without
// touching the immutable fields (title, position). Phase 27.4 uses
// this from MaterializeLesson so the tutor agent can patch a lesson
// in place without re-submitting the whole curriculum. task_id="" is
// interpreted as NULL (clears the link).
func (r *courseRepo) UpdateLessonContent(ctx context.Context, lessonID, contentMD string, status course.LessonStatus, taskID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE course_lessons SET content_md=?, status=?, task_id=?
		 WHERE id = ?`,
		contentMD, string(status), nullString(taskID), lessonID,
	)
	if err != nil {
		return fmt.Errorf("course.UpdateLessonContent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// GetQuiz loads a single quiz by id. Phase 27.4 uses it from
// AnswerQuiz so the service can read the expected_md and kind without
// a list traversal.
func (r *courseRepo) GetQuiz(ctx context.Context, id string) (*course.Quiz, error) {
	const q = `SELECT id, lesson_id, position, question_md, expected_md, kind
		FROM course_quizzes WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var z course.Quiz
	var kind string
	if err := row.Scan(&z.ID, &z.LessonID, &z.Position, &z.QuestionMD, &z.ExpectedMD, &kind); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetQuiz: %w", err)
	}
	z.Kind = course.QuizKind(kind)
	return &z, nil
}

// ModuleCourseOwner walks the module → course → owner chain in a
// single SQL hop. Phase 27.4 uses it from AnswerQuiz to find the
// course owner for the open-quiz review task without expanding the
// repo interface with a dedicated CourseByModule lookup.
func (r *courseRepo) ModuleCourseOwner(ctx context.Context, moduleID string) (string, error) {
	const q = `SELECT c.owner_id FROM course_modules m
		JOIN courses c ON c.id = m.course_id
		WHERE m.id = ?`
	var owner string
	if err := r.db.QueryRowContext(ctx, q, moduleID).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return "", course.ErrNotFound
		}
		return "", fmt.Errorf("course.ModuleCourseOwner: %w", err)
	}
	return owner, nil
}

func (r *courseRepo) CreateQuiz(ctx context.Context, q *course.Quiz) error {
	if q.ID == "" {
		q.ID = newUUID()
	}
	// Position 0 means "append at the end" — the repo picks the
	// next slot atomically via a sub-query and writes it back
	// into q so the caller knows where the quiz landed. Phase 27.6
	// is what makes the UI's "add another question" affordance
	// work without the client having to know the current count.
	if q.Position <= 0 {
		var newPos int
		err := r.db.QueryRowContext(ctx,
			`INSERT INTO course_quizzes (id, lesson_id, position, question_md, expected_md, kind)
			 VALUES (?, ?,
			   COALESCE((SELECT MAX(position)+1 FROM course_quizzes WHERE lesson_id = ?), 1),
			   ?, ?, ?)
			 RETURNING position`,
			q.ID, q.LessonID, q.LessonID, q.QuestionMD, q.ExpectedMD, string(q.Kind),
		).Scan(&newPos)
		if err != nil {
			return fmt.Errorf("course.CreateQuiz: %w", err)
		}
		q.Position = newPos
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO course_quizzes (id, lesson_id, position, question_md, expected_md, kind)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		q.ID, q.LessonID, q.Position, q.QuestionMD, q.ExpectedMD, string(q.Kind),
	)
	if err != nil {
		return fmt.Errorf("course.CreateQuiz: %w", err)
	}
	return nil
}

// UpdateQuiz writes question_md/expected_md/kind in place (Phase
// 30.13). Position and lesson_id are untouched — quiz order inside
// a lesson is stable in the MVP.
func (r *courseRepo) UpdateQuiz(ctx context.Context, q *course.Quiz) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE course_quizzes SET question_md=?, expected_md=?, kind=? WHERE id = ?`,
		q.QuestionMD, q.ExpectedMD, string(q.Kind), q.ID,
	)
	if err != nil {
		return fmt.Errorf("course.UpdateQuiz: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

// DeleteQuiz removes the quiz row (Phase 30.13).
func (r *courseRepo) DeleteQuiz(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM course_quizzes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("course.DeleteQuiz: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

func (r *courseRepo) ListQuizzesInCourse(ctx context.Context, courseID string) ([]*course.Quiz, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT q.id, q.lesson_id, q.position, q.question_md, q.expected_md, q.kind
		 FROM course_quizzes q
		 JOIN course_lessons l ON l.id = q.lesson_id
		 JOIN course_modules m ON m.id = l.module_id
		 WHERE m.course_id = ?
		 ORDER BY m.position ASC, l.position ASC, q.position ASC, q.id ASC`,
		courseID)
	if err != nil {
		return nil, fmt.Errorf("course.ListQuizzesInCourse: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Quiz, 0)
	for rows.Next() {
		var q course.Quiz
		var kind string
		if err := rows.Scan(&q.ID, &q.LessonID, &q.Position,
			&q.QuestionMD, &q.ExpectedMD, &kind); err != nil {
			return nil, err
		}
		q.Kind = course.QuizKind(kind)
		out = append(out, &q)
	}
	return out, rows.Err()
}

func (r *courseRepo) Progress(ctx context.Context, courseID string) (course.Progress, error) {
	var p course.Progress
	row := r.db.QueryRowContext(ctx,
		`SELECT
		    COUNT(*),
		    COALESCE(SUM(CASE WHEN l.status = 'done' THEN 1 ELSE 0 END), 0)
		 FROM course_lessons l
		 JOIN course_modules m ON m.id = l.module_id
		 WHERE m.course_id = ?`, courseID)
	if err := row.Scan(&p.LessonsTotal, &p.LessonsDone); err != nil {
		return course.Progress{}, fmt.Errorf("course.Progress: %w", err)
	}
	return p, nil
}

// SubmitCurriculum replaces the modules + lessons + quizzes for the
// course atomically. Quizzes are matched to lessons via LessonID;
// the service fills that in from the parent module during the
// request decode so the payload is one flat list.
//
// Phase 27.6: previously quizzes were a follow-up step the tutor
// did after approval. They're now part of the swap, which means
// the owner can ship a fully-formed program in one request and
// the tutor's job collapses to "approve or send back".
func (r *courseRepo) SubmitCurriculum(
	ctx context.Context,
	courseID string,
	modules []*course.Module,
	lessons []*course.Lesson,
	quizzes []*course.Quiz,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("course.SubmitCurriculum: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Validate the course exists.
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM courses WHERE id = ?`, courseID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return course.ErrNotFound
		}
		return err
	}

	// Wipe existing modules + lessons + quizzes (the cascade on
	// modules drops lessons, the cascade on lessons drops quizzes;
	// explicit delete for quizzes keeps it independent of the
	// schema's exact cascade direction).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM course_modules WHERE course_id = ?`, courseID); err != nil {
		return fmt.Errorf("course.SubmitCurriculum: clear: %w", err)
	}
	moduleIDs := make(map[string]struct{}, len(modules))
	for _, m := range modules {
		if m.ID == "" {
			m.ID = newUUID()
		}
		moduleIDs[m.ID] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO course_modules (id, course_id, title, description, position)
			 VALUES (?, ?, ?, ?, ?)`,
			m.ID, courseID, m.Title, m.Description, m.Position,
		); err != nil {
			return fmt.Errorf("course.SubmitCurriculum: module: %w", err)
		}
		for _, l := range lessons {
			if l.ModuleID != m.ID {
				continue
			}
			if l.ID == "" {
				l.ID = newUUID()
			}
			var lessonNum int
			if err := tx.QueryRowContext(ctx,
				`UPDATE lesson_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
			).Scan(&lessonNum); err != nil {
				return fmt.Errorf("course.SubmitCurriculum: draw lesson number: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO course_lessons (id, module_id, title, content_md, status, position, task_id, number)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				l.ID, m.ID, l.Title, l.ContentMD, string(l.Status),
				l.Position, nullString(l.TaskID), lessonNum,
			); err != nil {
				return fmt.Errorf("course.SubmitCurriculum: lesson: %w", err)
			}
			l.Number = lessonNum
		}
	}
	// Insert quizzes in the same tx. Each quiz's LessonID is
	// expected to match a lesson that was just inserted; we
	// silently skip ones that don't (defence-in-depth against
	// hand-crafted payloads). When the caller passes an empty ID
	// we mint one.
	for _, q := range quizzes {
		if _, ok := moduleIDs[lessonModuleID(lessons, q.LessonID)]; !ok {
			continue
		}
		if q.ID == "" {
			q.ID = newUUID()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO course_quizzes (id, lesson_id, position, question_md, expected_md, kind)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			q.ID, q.LessonID, q.Position, q.QuestionMD, q.ExpectedMD, string(q.Kind),
		); err != nil {
			return fmt.Errorf("course.SubmitCurriculum: quiz: %w", err)
		}
	}
	return tx.Commit()
}

// lessonModuleID returns the module_id of the lesson with the given
// id, or "" if not found. SubmitCurriculum uses this to filter
// quizzes to lessons that are actually part of the new curriculum.
func lessonModuleID(lessons []*course.Lesson, lessonID string) string {
	for _, l := range lessons {
		if l.ID == lessonID {
			return l.ModuleID
		}
	}
	return ""
}

// ApplyStructure rewrites the course's module positions and lesson
// (module_id, position) pairs in one transaction. The payload must
// name every module and every lesson of the course exactly once —
// partial payloads are rejected so a client bug can never orphan
// rows. No rows are created or deleted, which is what preserves
// student progress (lesson status) across a drag-and-drop reorder
// of an active course (Phase 30.13).
func (r *courseRepo) ApplyStructure(ctx context.Context, courseID string, modules []course.ModuleOrder) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("course.ApplyStructure: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Load the current module and lesson id sets of the course.
	existingModules := map[string]struct{}{}
	mrows, err := tx.QueryContext(ctx,
		`SELECT id FROM course_modules WHERE course_id = ?`, courseID)
	if err != nil {
		return fmt.Errorf("course.ApplyStructure: load modules: %w", err)
	}
	for mrows.Next() {
		var id string
		if err := mrows.Scan(&id); err != nil {
			mrows.Close()
			return err
		}
		existingModules[id] = struct{}{}
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return err
	}

	existingLessons := map[string]struct{}{}
	lrows, err := tx.QueryContext(ctx,
		`SELECT l.id FROM course_lessons l
		 JOIN course_modules m ON m.id = l.module_id
		 WHERE m.course_id = ?`, courseID)
	if err != nil {
		return fmt.Errorf("course.ApplyStructure: load lessons: %w", err)
	}
	for lrows.Next() {
		var id string
		if err := lrows.Scan(&id); err != nil {
			lrows.Close()
			return err
		}
		existingLessons[id] = struct{}{}
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return err
	}

	// Validate exact coverage: no unknown, duplicate, or missing ids.
	if len(modules) != len(existingModules) {
		return course.ErrInvalidInput
	}
	seenModules := map[string]struct{}{}
	seenLessons := map[string]struct{}{}
	lessonCount := 0
	for _, mo := range modules {
		if _, ok := existingModules[mo.ModuleID]; !ok {
			return course.ErrInvalidInput
		}
		if _, dup := seenModules[mo.ModuleID]; dup {
			return course.ErrInvalidInput
		}
		seenModules[mo.ModuleID] = struct{}{}
		for _, lid := range mo.LessonIDs {
			if _, ok := existingLessons[lid]; !ok {
				return course.ErrInvalidInput
			}
			if _, dup := seenLessons[lid]; dup {
				return course.ErrInvalidInput
			}
			seenLessons[lid] = struct{}{}
			lessonCount++
		}
	}
	if lessonCount != len(existingLessons) {
		return course.ErrInvalidInput
	}

	// Rewrite positions 1..n in payload order; lessons may move
	// across modules freely (module_id is rewritten alongside).
	for i, mo := range modules {
		res, err := tx.ExecContext(ctx,
			`UPDATE course_modules SET position = ? WHERE id = ?`,
			i+1, mo.ModuleID)
		if err != nil {
			return fmt.Errorf("course.ApplyStructure: module position: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return course.ErrInvalidInput
		}
		for j, lid := range mo.LessonIDs {
			res, err := tx.ExecContext(ctx,
				`UPDATE course_lessons SET module_id = ?, position = ? WHERE id = ?`,
				mo.ModuleID, j+1, lid)
			if err != nil {
				return fmt.Errorf("course.ApplyStructure: lesson position: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return course.ErrInvalidInput
			}
		}
	}
	return tx.Commit()
}
