package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// BackupSetting is one (key, value) row from the backup_settings table.
//
// The table was created by 001_init.sql with no seed data — Phase 28.1
// (polish.1) is what wires writes through to it. Before that, GET /
// PUT /api/v1/backups/settings was a 501 stub (PUT) and a fixed
// in-memory snapshot of the start-time config (GET). See plan §9.7.
type BackupSetting struct {
	Key   string
	Value json.RawMessage
}

// BackupSettingsRepository persists (key, JSON value) pairs in the
// backup_settings table. The intent of this table is operator-facing
// configuration that the UI can edit without touching config.yaml —
// see docs/SESSION.md "«Фаза «Полировка»»" backlog.
//
// Settings take effect after the next process restart: the running
// `*backup.Service` reads URL/auth/schedule from its in-memory Config
// that was wired at startup (cmd/orenda/main.go). Hot-reload is
// tracked under a separate follow-up; for now this repo is the
// persistence layer the UI talks to and the next startup reads.
//
// All read methods fall back to (zero, ok=false) when a key is
// absent — callers can decide between "use the cfg default" and
// "the operator never set this". Put is "upsert": SET replaces the
// JSON blob, no merge.
type BackupSettingsRepository interface {
	GetAll(ctx context.Context) ([]BackupSetting, error)
	GetByKey(ctx context.Context, key string) (json.RawMessage, bool, error)
	SetKey(ctx context.Context, key string, value json.RawMessage) error
	// ClearByKey removes a setting (returns to "operator never set
	// this"). The Config layers can fall back to their defaults
	// after this. Useful for PUT semantics where an empty string
	// in the UI maps to "use the default", not "store empty".
	ClearByKey(ctx context.Context, key string) error
}

// ErrInvalidSettingKey is returned when the supplied key is empty
// (the table's PRIMARY KEY requires a non-empty TEXT key).
var ErrInvalidSettingKey = errors.New("backup_settings: empty key")

// ErrInvalidSettingValue is returned when the supplied value is not
// valid JSON. Settings are stored as JSON blobs and we refuse to
// persist anything else.
var ErrInvalidSettingValue = errors.New("backup_settings: value must be valid JSON")

type backupSettingsRepo struct {
	db *sql.DB
}

// NewBackupSettingsRepository wires the repo to a *sql.DB. Pass the
// same DB the rest of the storage uses — backup_settings is a
// per-installation table (one set of settings per process), not
// per-user.
func NewBackupSettingsRepository(db *sql.DB) BackupSettingsRepository {
	return &backupSettingsRepo{db: db}
}

func (r *backupSettingsRepo) GetAll(ctx context.Context) ([]BackupSetting, error) {
	const q = `SELECT key, value FROM backup_settings ORDER BY key`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("backup_settings.GetAll: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []BackupSetting
	for rows.Next() {
		var (
			key   string
			value sql.NullString
		)
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("backup_settings.GetAll scan: %w", err)
		}
		s := BackupSetting{Key: key}
		if value.Valid {
			s.Value = json.RawMessage(value.String)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backup_settings.GetAll rows: %w", err)
	}
	return out, nil
}

func (r *backupSettingsRepo) GetByKey(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if key == "" {
		return nil, false, ErrInvalidSettingKey
	}
	const q = `SELECT value FROM backup_settings WHERE key = ?`
	var raw sql.NullString
	err := r.db.QueryRowContext(ctx, q, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("backup_settings.GetByKey %q: %w", key, err)
	}
	if !raw.Valid {
		return nil, false, nil
	}
	return json.RawMessage(raw.String), true, nil
}

func (r *backupSettingsRepo) SetKey(ctx context.Context, key string, value json.RawMessage) error {
	if key == "" {
		return ErrInvalidSettingKey
	}
	if !json.Valid(value) {
		return ErrInvalidSettingValue
	}
	const q = `
		INSERT INTO backup_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`
	if _, err := r.db.ExecContext(ctx, q, key, string(value)); err != nil {
		return fmt.Errorf("backup_settings.SetKey %q: %w", key, err)
	}
	return nil
}

func (r *backupSettingsRepo) ClearByKey(ctx context.Context, key string) error {
	if key == "" {
		return ErrInvalidSettingKey
	}
	const q = `DELETE FROM backup_settings WHERE key = ?`
	if _, err := r.db.ExecContext(ctx, q, key); err != nil {
		return fmt.Errorf("backup_settings.ClearByKey %q: %w", key, err)
	}
	return nil
}
