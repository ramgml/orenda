// Package sqlite — tutor_messages persistence (T16).
//
// One row per student question or agent reply; a thread is keyed by
// (lesson_id, user_id) and pending iff its last row has
// role='user'. Delete cascades from course_lessons (migration 045).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/service/tutor"
)

type tutorMessageRepo struct {
	db *sql.DB
}

// NewTutorMessageRepository returns the sqlite-backed tutor.Repository.
func NewTutorMessageRepository(db *sql.DB) tutor.Repository {
	return &tutorMessageRepo{db: db}
}

func (r *tutorMessageRepo) Create(ctx context.Context, m *tutor.Message) error {
	if m.ID == "" {
		m.ID = newUUID()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tutor_messages (id, lesson_id, user_id, role, body_md, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		m.ID, m.LessonID, m.UserID, string(m.Role), m.BodyMD,
		m.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("tutor.Create: %w", err)
	}
	return nil
}

func (r *tutorMessageRepo) ListThread(ctx context.Context, lessonID, userID string) ([]*tutor.Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, lesson_id, user_id, role, body_md, created_at
		FROM tutor_messages
		WHERE lesson_id = ? AND user_id = ?
		ORDER BY created_at ASC, id ASC`, lessonID, userID)
	if err != nil {
		return nil, fmt.Errorf("tutor.ListThread: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]*tutor.Message, 0)
	for rows.Next() {
		m, err := scanTutorMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *tutorMessageRepo) Last(ctx context.Context, lessonID, userID string) (*tutor.Message, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, lesson_id, user_id, role, body_md, created_at
		FROM tutor_messages
		WHERE lesson_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, lessonID, userID)
	m, err := scanTutorMessage(row)
	if err == sql.ErrNoRows {
		// No thread yet — (nil, nil) is the documented "empty
		// thread" result; the service decides what that means.
		return nil, nil //nolint:nilnil // empty thread is a valid state, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("tutor.Last: %w", err)
	}
	return m, nil
}

// PendingThreads returns the distinct thread keys whose last message
// has role='user' — the agent's queue, derived from the rows (no
// awaiting column to drift). The MAX(created_at || id) trick pins
// each thread's newest row; id breaks created_at ties
// deterministically.
func (r *tutorMessageRepo) PendingThreads(ctx context.Context) ([]tutor.PendingThreadKey, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.lesson_id, t.user_id
		FROM tutor_messages t
		JOIN (
			SELECT lesson_id, user_id, MAX(created_at || id) AS tip
			FROM tutor_messages
			GROUP BY lesson_id, user_id
		) tip ON tip.lesson_id = t.lesson_id
		       AND tip.user_id = t.user_id
		       AND tip.tip = t.created_at || t.id
		WHERE t.role = 'user'`)
	if err != nil {
		return nil, fmt.Errorf("tutor.PendingThreads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]tutor.PendingThreadKey, 0)
	for rows.Next() {
		var k tutor.PendingThreadKey
		if err := rows.Scan(&k.LessonID, &k.UserID); err != nil {
			return nil, fmt.Errorf("tutor.PendingThreads: scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// scanner abstracts *sql.Row / *sql.Rows for the shared column scan.
// Named locally: study_proposal_repo.go declares its own `scanner`.
type tutorRowScanner interface {
	Scan(dest ...any) error
}

func scanTutorMessage(s tutorRowScanner) (*tutor.Message, error) {
	m := &tutor.Message{}
	var role, createdAt string
	if err := s.Scan(&m.ID, &m.LessonID, &m.UserID, &role, &m.BodyMD, &createdAt); err != nil {
		return nil, err
	}
	m.Role = tutor.Role(role)
	m.CreatedAt = parseTutorTime(createdAt)
	return m, nil
}

// parseTutorTime parses the RFC3339 stamps Create writes; the plain
// datetime('now') DEFAULT (used only by rows inserted outside the
// repo) parses as the SQLite layout.
func parseTutorTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}
