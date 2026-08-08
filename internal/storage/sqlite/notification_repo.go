// Package sqlite — notifications + bot_subscriptions repos.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/service/notifier"
)

// notificationRepo implements notifier.InboxRepository.
type notificationRepo struct{ db *sql.DB }

// NewNotificationRepository returns the notifier inbox repo.
func NewNotificationRepository(db *sql.DB) notifier.InboxRepository {
	return &notificationRepo{db: db}
}

func (r *notificationRepo) Upsert(ctx context.Context, n *notifier.Notification) error {
	if n.ID == "" {
		n.ID = newUUID()
	}
	// Delete any unread row with the same (user_id, dedup_key) so the
	// new insert wins; this is the dedup semantic.
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE user_id = ? AND dedup_key = ? AND read_at IS NULL`,
		n.UserID, n.DedupKey); err != nil {
		return fmt.Errorf("notif.Upsert: delete: %w", err)
	}
	const q = `
		INSERT INTO notifications (id, user_id, type, target_type, target_id, payload, dedup_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		n.ID, n.UserID, n.Type, nullString(n.TargetType), nullString(n.TargetID), n.Payload, n.DedupKey,
	)
	if err != nil {
		if isUniqueViolation(err) {
			// Another concurrent writer inserted the same dedup_key.
			return nil
		}
		return fmt.Errorf("notif.Upsert: insert: %w", err)
	}
	return nil
}

func (r *notificationRepo) ListByUser(ctx context.Context, userID string, limit int) ([]*notifier.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		SELECT id, user_id, type, target_type, target_id, payload, read_at, dedup_key, created_at
		FROM notifications
		WHERE user_id = ?
		ORDER BY read_at IS NOT NULL ASC, created_at DESC
		LIMIT ?
	`
	rows, err := r.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("notif.List: %w", err)
	}
	defer rows.Close()
	var out []*notifier.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *notificationRepo) MarkRead(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = datetime('now') WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("notif.MarkRead: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("notification: not found")
	}
	return nil
}

func (r *notificationRepo) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

func scanNotification(rows *sql.Rows) (*notifier.Notification, error) {
	var (
		n      notifier.Notification
		target sql.NullString
		tType  sql.NullString
		readAt sql.NullString
		cAt    string
	)
	if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &tType, &target, &n.Payload, &readAt, &n.DedupKey, &cAt); err != nil {
		return nil, fmt.Errorf("notif.Scan: %w", err)
	}
	n.TargetType = tType.String
	n.TargetID = target.String
	if readAt.Valid {
		t := parseTime(readAt.String)
		n.ReadAt = &t
	}
	n.CreatedAt = parseTime(cAt)
	return &n, nil
}

// --- bot_subscriptions ---

// botSubRepo implements notifier.SubscriptionRepository.
type botSubRepo struct{ db *sql.DB }

// NewBotSubscriptionRepository returns the subs repo.
func NewBotSubscriptionRepository(db *sql.DB) notifier.SubscriptionRepository {
	return &botSubRepo{db: db}
}

func (r *botSubRepo) ListForUserEvent(ctx context.Context, userID, eventType string) ([]*notifier.Subscription, error) {
	// events is JSON; filter at the application level after reading
	// enabled subscriptions for this user.
	subs, err := r.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []*notifier.Subscription
	for _, s := range subs {
		if s.Enabled && s.Subscribes(eventType) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *botSubRepo) ListForUser(ctx context.Context, userID string) ([]*notifier.Subscription, error) {
	const q = `
		SELECT id, user_id, bot_type, target_address, events, enabled, created_at
		FROM bot_subscriptions
		WHERE user_id = ?
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("subs.ListForUser: %w", err)
	}
	defer rows.Close()
	var out []*notifier.Subscription
	for rows.Next() {
		var (
			s       notifier.Subscription
			events  string
			enabled int
			cAt     string
		)
		if err := rows.Scan(&s.ID, &s.UserID, &s.BotType, &s.TargetAddress, &events, &enabled, &cAt); err != nil {
			return nil, fmt.Errorf("subs.Scan: %w", err)
		}
		s.Enabled = enabled != 0
		s.CreatedAt = parseTime(cAt)
		if events != "" {
			_ = json.Unmarshal([]byte(events), &s.Events)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}
