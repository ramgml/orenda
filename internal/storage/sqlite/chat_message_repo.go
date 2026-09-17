package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/chat"
	chatdialog "github.com/ramgml/orenda/internal/service/chatdialog"
)

type chatMessageRepo struct {
	db *sql.DB
}

func NewChatMessageRepository(db *sql.DB) chat.MessageRepository {
	return &chatMessageRepo{db: db}
}

func (r *chatMessageRepo) Create(ctx context.Context, m *chat.Message) error {
	if m.ID == "" {
		m.ID = newUUID()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO chat_messages (id, thread_id, sender_type, body_md, command, result_ref, user_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ThreadID, string(m.SenderType), m.BodyMD,
		nullString(m.Command), nullString(m.ResultRef), m.UserID, m.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("chat.Create: %w", err)
	}
	return nil
}

// chatMessageColumns is the SELECT list shared by the reader
// methods. user_id arrived with migration 048; COALESCE keeps the
// scan null-safe on legacy rows.
const chatMessageColumns = `
	id, thread_id, sender_type, body_md, COALESCE(command, ''), COALESCE(result_ref, ''), COALESCE(user_id, ''), created_at`

// scanChatMessage reads one chat_messages row from a row-like
// scanner (works for both *sql.Rows and *sql.Row).
func scanChatMessage(row interface{ Scan(...any) error }) (*chat.Message, error) {
	m := &chat.Message{}
	var sender, createdAt string
	if err := row.Scan(&m.ID, &m.ThreadID, &sender, &m.BodyMD, &m.Command, &m.ResultRef, &m.UserID, &createdAt); err != nil {
		return nil, err
	}
	m.SenderType = chat.SenderType(sender)
	m.CreatedAt = parseTimeLite(createdAt)
	return m, nil
}

// ByID fetches one message; chat.ErrNotFound when absent. The
// agent reply flow derives thread + owner from the pending
// question it pulled.
func (r *chatMessageRepo) ByID(ctx context.Context, id string) (*chat.Message, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+chatMessageColumns+` FROM chat_messages WHERE id = ?`, id)
	m, err := scanChatMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, chat.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat.ByID: %w", err)
	}
	return m, nil
}

// Last returns the user's newest message on a thread (nil when the
// thread has none). MAX(created_at || id) pins the newest row; id
// breaks created_at ties deterministically (same trick the tutor
// repo uses for PendingThreads).
func (r *chatMessageRepo) Last(ctx context.Context, userID, threadID string) (*chat.Message, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+chatMessageColumns+`
		FROM chat_messages
		WHERE user_id = ? AND thread_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, userID, threadID)
	m, err := scanChatMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat.Last: %w", err)
	}
	return m, nil
}

// ListByUserThread returns one user's messages on one thread
// oldest-first, capped at limit (50 if limit <= 0).
func (r *chatMessageRepo) ListByUserThread(ctx context.Context, userID, threadID string, limit int) ([]*chat.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+chatMessageColumns+`
		FROM chat_messages
		WHERE user_id = ? AND thread_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?`, userID, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("chat.ListByUserThread: %w", err)
	}
	defer rows.Close()
	out := make([]*chat.Message, 0)
	for rows.Next() {
		m, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PendingThreads returns the distinct (user_id, thread_id) keys
// whose last message has sender_type='user' — the dashboard
// agent's queue, derived from the rows. MAX(created_at || id)
// pins each thread's newest row; id breaks created_at ties
// deterministically (same trick the tutor repo uses).
func (r *chatMessageRepo) PendingThreads(ctx context.Context) ([]chatdialog.PendingThreadKey, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.user_id, c.thread_id
		FROM chat_messages c
		JOIN (
			SELECT user_id, thread_id, MAX(created_at || id) AS tip
			FROM chat_messages
			GROUP BY user_id, thread_id
		) tip ON tip.user_id = c.user_id
		       AND tip.thread_id = c.thread_id
		       AND tip.tip = c.created_at || c.id
		WHERE c.sender_type = 'user' AND c.user_id != ''`)
	if err != nil {
		return nil, fmt.Errorf("chat.PendingThreads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]chatdialog.PendingThreadKey, 0)
	for rows.Next() {
		var k chatdialog.PendingThreadKey
		if err := rows.Scan(&k.UserID, &k.ThreadID); err != nil {
			return nil, fmt.Errorf("chat.PendingThreads: scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// chatThreadRepo implements chat.ThreadRepository over chat_threads
// (migration 047): who may replay which dashboard thread.
type chatThreadRepo struct {
	db *sql.DB
}

// NewChatThreadRepository returns the sqlite-backed chat.ThreadRepository.
func NewChatThreadRepository(db *sql.DB) chat.ThreadRepository {
	return &chatThreadRepo{db: db}
}

func (r *chatThreadRepo) Upsert(ctx context.Context, userID, threadID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO chat_threads (id, user_id, thread_id) VALUES (?, ?, ?)
		ON CONFLICT(user_id, thread_id) DO NOTHING`,
		newUUID(), userID, threadID)
	if err != nil {
		return fmt.Errorf("chat.UpsertThread: %w", err)
	}
	return nil
}

func (r *chatThreadRepo) Owned(ctx context.Context, userID, threadID string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM chat_threads WHERE user_id = ? AND thread_id = ? LIMIT 1`, userID, threadID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("chat.OwnedThread: %w", err)
	}
	return true, nil
}
