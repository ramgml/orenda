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
	const q = `INSERT INTO courses
		(id, title, intent_md, level, pace, status, owner_id, generator_task_id, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`
	_, err := r.db.ExecContext(ctx, q,
		c.ID, c.Title, c.IntentMD, c.Level, c.Pace, string(c.Status),
		c.OwnerID, nullString(c.GeneratorTaskID),
	)
	if err != nil {
		return fmt.Errorf("course.Create: %w", err)
	}
	got, err := r.GetCourse(ctx, c.ID)
	if err != nil {
		return err
	}
	*c = *got
	return nil
}

func (r *courseRepo) GetCourse(ctx context.Context, id string) (*course.Course, error) {
	const q = `SELECT id, title, intent_md, level, pace, status, owner_id,
		COALESCE(generator_task_id, ''), created_at, updated_at
		FROM courses WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var c course.Course
	var status, created, updated string
	if err := row.Scan(&c.ID, &c.Title, &c.IntentMD, &c.Level, &c.Pace,
		&status, &c.OwnerID, &c.GeneratorTaskID, &created, &updated); err != nil {
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

func (r *courseRepo) ListCourses(ctx context.Context, ownerID string) ([]*course.Course, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, title, intent_md, level, pace, status, owner_id,
		        COALESCE(generator_task_id, ''), created_at, updated_at
		 FROM courses WHERE owner_id = ? ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("course.List: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Course, 0)
	for rows.Next() {
		var c course.Course
		var status, created, updated string
		if err := rows.Scan(&c.ID, &c.Title, &c.IntentMD, &c.Level, &c.Pace,
			&status, &c.OwnerID, &c.GeneratorTaskID, &created, &updated); err != nil {
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
		    owner_id=?, generator_task_id=?, updated_at=datetime('now')
		 WHERE id = ?`,
		c.Title, c.IntentMD, c.Level, c.Pace, string(c.Status),
		c.OwnerID, nullString(c.GeneratorTaskID), c.ID,
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
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, content_md, status, position, task_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.ModuleID, l.Title, l.ContentMD, string(l.Status),
		l.Position, nullString(l.TaskID),
	)
	if err != nil {
		return fmt.Errorf("course.CreateLesson: %w", err)
	}
	return nil
}

func (r *courseRepo) ListLessons(ctx context.Context, moduleID string) ([]*course.Lesson, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, module_id, title, content_md, status, position, COALESCE(task_id, '')
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
			&status, &l.Position, &l.TaskID); err != nil {
			return nil, err
		}
		l.Status = course.LessonStatus(status)
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *courseRepo) ListLessonsInCourse(ctx context.Context, courseID string) ([]*course.Lesson, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT l.id, l.module_id, l.title, l.content_md, l.status, l.position, COALESCE(l.task_id, '')
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
			&status, &l.Position, &l.TaskID); err != nil {
			return nil, err
		}
		l.Status = course.LessonStatus(status)
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *courseRepo) GetLesson(ctx context.Context, id string) (*course.Lesson, error) {
	const q = `SELECT id, module_id, title, content_md, status, position, COALESCE(task_id, '')
		FROM course_lessons WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	var l course.Lesson
	var status string
	if err := row.Scan(&l.ID, &l.ModuleID, &l.Title, &l.ContentMD,
		&status, &l.Position, &l.TaskID); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, fmt.Errorf("course.GetLesson: %w", err)
	}
	l.Status = course.LessonStatus(status)
	return &l, nil
}

func (r *courseRepo) UpdateLesson(ctx context.Context, l *course.Lesson) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE course_lessons SET title=?, content_md=?, status=?, position=?, task_id=?
		 WHERE id = ?`,
		l.Title, l.ContentMD, string(l.Status), l.Position, nullString(l.TaskID), l.ID,
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

func (r *courseRepo) CreateQuiz(ctx context.Context, q *course.Quiz) error {
	if q.ID == "" {
		q.ID = newUUID()
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

// SubmitCurriculum replaces the modules + lessons for the course
// atomically. Quizzes are not replaced within this call — we'd
// accept the curriculum first, then the tutor fills in quizzes.
func (r *courseRepo) SubmitCurriculum(
	ctx context.Context,
	courseID string,
	modules []*course.Module,
	lessons []*course.Lesson,
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

	// Wipe existing modules + lessons (cascade kills lessons).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM course_modules WHERE course_id = ?`, courseID); err != nil {
		return fmt.Errorf("course.SubmitCurriculum: clear: %w", err)
	}
	for _, m := range modules {
		if m.ID == "" {
			m.ID = newUUID()
		}
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
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO course_lessons (id, module_id, title, content_md, status, position, task_id)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				l.ID, m.ID, l.Title, l.ContentMD, string(l.Status),
				l.Position, nullString(l.TaskID),
			); err != nil {
				return fmt.Errorf("course.SubmitCurriculum: lesson: %w", err)
			}
		}
	}
	return tx.Commit()
}
