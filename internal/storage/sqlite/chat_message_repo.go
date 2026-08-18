package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/chat"
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
		INSERT INTO chat_messages (id, thread_id, sender_type, body_md, command, result_ref, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ThreadID, string(m.SenderType), m.BodyMD,
		nullString(m.Command), nullString(m.ResultRef), m.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("chat.Create: %w", err)
	}
	return nil
}

func (r *chatMessageRepo) ListByThread(ctx context.Context, threadID string, limit int) ([]*chat.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, thread_id, sender_type, body_md, COALESCE(command, ''), COALESCE(result_ref, ''), created_at
		FROM chat_messages
		WHERE thread_id = ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("chat.ListByThread: %w", err)
	}
	defer rows.Close()
	out := make([]*chat.Message, 0)
	for rows.Next() {
		m := &chat.Message{}
		var sender, createdAt string
		if err := rows.Scan(&m.ID, &m.ThreadID, &sender, &m.BodyMD, &m.Command, &m.ResultRef, &createdAt); err != nil {
			return nil, err
		}
		m.SenderType = chat.SenderType(sender)
		m.CreatedAt = parseTimeLite(createdAt)
		out = append(out, m)
	}
	return out, rows.Err()
}
