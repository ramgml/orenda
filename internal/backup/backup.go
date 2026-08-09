// Package backup provides the backup service: git push of the mirror
// directory, SQLite online snapshot, and WAL archiving.
//
// Phase 7 ships the primitives; the scheduler (7.5) calls them on a
// tick, the CLI (7.6) exposes them as commands, and the handlers (7.7)
// surface them via the REST API.
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors.
var (
	ErrNoRemote      = errors.New("backup: no git remote configured")
	ErrPushFailed    = errors.New("backup: git push failed")
	ErrNotFound      = errors.New("backup: snapshot not found")
	ErrInvalidInput  = errors.New("backup: invalid input")
	ErrServerRunning = errors.New("backup: server is running — stop it before restoring in place")
	ErrNotSQLite     = errors.New("backup: source is not a valid sqlite database")
)

// Config drives the backup behaviour.
type Config struct {
	// MirrorDir is the directory with the markdown mirror (data/mirror).
	MirrorDir string

	// SnapshotDir is where sqlite snapshots live (data/snapshots).
	SnapshotDir string

	// DBPath is the live database file (data/orenda.db).
	DBPath string

	// RemoteURL is the git remote (e.g. git@github.com:user/backup.git).
	RemoteURL string

	// RemoteAuth is the auth material — an HTTPS token or the path to an
	// SSH key. Empty means "use the user's ssh-agent / credential store".
	RemoteAuth string

	// SnapshotRotationDays: keep daily snapshots for N days. Older ones
	// are removed at snapshot creation time.
	SnapshotRotationDays int
}

// Service bundles the dependencies.
type Service struct {
	cfg Config
	db  *sql.DB
}

// New returns a backup Service.
func New(cfg Config, db *sql.DB) *Service {
	return &Service{cfg: cfg, db: db}
}

// ----------------------------------------------------------------------------
// Git (7.3)
// ----------------------------------------------------------------------------

// EnsureGitRepo makes sure MirrorDir is a git repo with the configured
// remote. Returns an error only on catastrophic failure; missing remote
// is tolerated (and surfaced via ErrNoRemote at Push time).
func (s *Service) EnsureGitRepo(ctx context.Context) error {
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.cfg.MirrorDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
		}
		return nil
	}

	// Init if not a repo.
	if _, err := os.Stat(filepath.Join(s.cfg.MirrorDir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(s.cfg.MirrorDir, 0o755); err != nil {
			return err
		}
		if err := git("init", "-q"); err != nil {
			return err
		}
		if err := git("config", "user.email", "orenda@localhost"); err != nil {
			return err
		}
		if err := git("config", "user.name", "Orenda Backup"); err != nil {
			return err
		}
	}

	// Set remote if configured and not already set.
	if s.cfg.RemoteURL == "" {
		return nil
	}
	if err := git("remote", "get-url", "origin"); err != nil {
		return git("remote", "add", "origin", s.cfg.RemoteURL)
	}
	return git("remote", "set-url", "origin", s.cfg.RemoteURL)
}

// CommitAndPush stages every file in MirrorDir, commits with a dated
// message, and pushes to origin. Returns ErrNoRemote when no remote is
// configured.
func (s *Service) CommitAndPush(ctx context.Context, message string) error {
	if s.cfg.RemoteURL == "" {
		return ErrNoRemote
	}
	if err := s.EnsureGitRepo(ctx); err != nil {
		return err
	}

	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.cfg.MirrorDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
		}
		return nil
	}

	if err := git("add", "-A"); err != nil {
		return err
	}
	// Skip if nothing to commit.
	if err := git("diff", "--cached", "--quiet"); err == nil {
		return nil // nothing to commit
	}
	if err := git("commit", "-q", "-m", message); err != nil {
		return err
	}
	if err := git("push", "-q", "origin", "HEAD"); err != nil {
		return ErrPushFailed
	}
	return nil
}

// ----------------------------------------------------------------------------
// SQLite snapshot (7.4)
// ----------------------------------------------------------------------------

// Snapshot creates a SQLite backup file at
// SnapshotDir/orenda-YYYYMMDD-HHMMSS.db and rotates old snapshots per
// cfg.SnapshotRotationDays. Returns the path written.
func (s *Service) Snapshot(ctx context.Context) (string, error) {
	if s.cfg.DBPath == "" {
		return "", ErrInvalidInput
	}
	if err := os.MkdirAll(s.cfg.SnapshotDir, 0o755); err != nil {
		return "", fmt.Errorf("backup snapshot: mkdir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(s.cfg.SnapshotDir, "orenda-"+ts+".db")

	// VACUUM INTO refuses to overwrite; pick a unique suffix if the
	// timestamp collides with an earlier snapshot in the same second.
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(s.cfg.SnapshotDir,
			fmt.Sprintf("orenda-%s-%02d.db", ts, i))
	}

	// Use the SQLite backup API via a direct Exec — modernc.org/sqlite
	// supports VACUUM INTO which produces a consistent snapshot.
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return "", fmt.Errorf("backup snapshot: %w", err)
	}

	// Rotate.
	if s.cfg.SnapshotRotationDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -s.cfg.SnapshotRotationDays)
		entries, err := os.ReadDir(s.cfg.SnapshotDir)
		if err == nil {
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					_ = os.Remove(filepath.Join(s.cfg.SnapshotDir, e.Name()))
				}
			}
		}
	}

	return dst, nil
}

// ListSnapshots returns snapshot files ordered newest-first.
func (s *Service) ListSnapshots(ctx context.Context) ([]SnapshotInfo, error) {
	entries, err := os.ReadDir(s.cfg.SnapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup list: %w", err)
	}
	var out []SnapshotInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SnapshotInfo{
			Path:    filepath.Join(s.cfg.SnapshotDir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// SnapshotInfo is one row of the snapshot list.
type SnapshotInfo struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// ----------------------------------------------------------------------------
// Log (7.7's UI and the CLI status command use this)
// ----------------------------------------------------------------------------

// LogEntry is one row of backup_log.
type LogEntry struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`   // git_push | sqlite_snapshot | wal_archive
	Status       string    `json:"status"` // success | failed
	Message      string    `json:"message"`
	SnapshotPath string    `json:"snapshot_path"`
	CreatedAt    time.Time `json:"created_at"`
}

// RecordLog writes a row to backup_log.
func (s *Service) RecordLog(ctx context.Context, entryType, status, message, snapshotPath string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backup_log (id, type, status, message, snapshot_path, created_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
	`, newUUID(), entryType, status, message, snapshotPath)
	if err != nil {
		return fmt.Errorf("backup log: %w", err)
	}
	return nil
}

// ListLog returns the most recent backup_log entries.
func (s *Service) ListLog(ctx context.Context, limit int) ([]*LogEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, status, message, snapshot_path, created_at
		FROM backup_log ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("backup log list: %w", err)
	}
	defer rows.Close()
	var out []*LogEntry
	for rows.Next() {
		var (
			e   LogEntry
			msg sql.NullString
			pth sql.NullString
			cAt string
		)
		if err := rows.Scan(&e.ID, &e.Type, &e.Status, &msg, &pth, &cAt); err != nil {
			return nil, err
		}
		e.Message = msg.String
		e.SnapshotPath = pth.String
		e.CreatedAt = parseTime(cAt)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------------
// Restore (Phase 7 follow-up)
// ----------------------------------------------------------------------------

// sqliteMagic is the 16-byte header every sqlite database starts with.
var sqliteMagic = []byte("SQLite format 3\x00")

// Restore replaces destPath with a copy of snapshotPath.
//
// The operation is intentionally filesystem-only: it does NOT touch s.db.
// Callers (the CLI) MUST guarantee the live database is closed — replacing
// a sqlite file while another process holds it open can corrupt WAL/SHM
// sidecars. The CLI does a TCP probe of cfg.ServerAddr before invoking
// this; the HTTP handler refuses outright with ErrServerRunning.
//
//   - snapshotPath must exist and start with the SQLite magic header.
//   - destPath must be non-empty; the parent directory is created if missing.
//   - The copy goes via destPath+".restore.tmp" then atomically renames in.
//   - If destPath has stale -wal / -shm sidecars (left over from a prior
//     crashed server), they are removed so sqlite starts clean.
//
// Returns ErrInvalidInput, ErrNotFound, ErrNotSQLite, or filesystem errors.
func (s *Service) Restore(_ context.Context, snapshotPath, destPath string) error {
	if snapshotPath == "" || destPath == "" {
		return ErrInvalidInput
	}

	src, err := os.Open(snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("backup restore: open snapshot: %w", err)
	}
	defer src.Close()

	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("backup restore: stat snapshot: %w", err)
	}
	if srcInfo.IsDir() {
		return ErrNotSQLite
	}

	// Verify the magic header — refuses to copy a non-sqlite file.
	var head [16]byte
	if _, err := io.ReadFull(src, head[:]); err != nil {
		return fmt.Errorf("backup restore: read snapshot header: %w", err)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup restore: rewind: %w", err)
	}
	if !bytes.Equal(head[:], sqliteMagic) {
		return ErrNotSQLite
	}

	// Ensure the parent directory exists.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("backup restore: mkdir: %w", err)
	}

	tmp := destPath + ".restore.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("backup restore: open tmp: %w", err)
	}

	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("backup restore: copy: %w", err)
	}
	// fsync the data to disk before the rename so the new file is durable.
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("backup restore: sync tmp: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup restore: close tmp: %w", err)
	}

	// Atomic swap. On POSIX rename(2) within the same filesystem is atomic.
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup restore: rename: %w", err)
	}

	// Drop any stale WAL/SHM sidecars so sqlite starts a clean journal.
	for _, side := range []string{destPath + "-wal", destPath + "-shm"} {
		if rmErr := os.Remove(side); rmErr != nil && !os.IsNotExist(rmErr) {
			// Non-fatal: sqlite will detect and rebuild, but report it.
			return fmt.Errorf("backup restore: remove sidecar %s: %w", side, rmErr)
		}
	}

	return nil
}

// IsServerRunning returns true when the orenda server is listening on
// host:port. Used by the CLI to refuse in-place restore while the live
// database is open.
//
// Cheap probe: a single dial attempt with a short timeout. Returns false
// on any error (refused, timeout, no route) — the only positive signal is
// a successful connect.
func IsServerRunning(ctx context.Context, host string, port int) bool {
	if port <= 0 {
		return false
	}
	dctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	d := net.Dialer{}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// parseTime is a copy of the shared helper from internal/storage/sqlite.
// We duplicate it here so backup doesn't import storage.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// newUUID is a small inline helper — UUIDv7 prefix isn't needed here,
// any unique string works.
func newUUID() string {
	b := make([]byte, 16)
	f, err := os.Open("/dev/urandom")
	if err == nil {
		defer f.Close()
		_, _ = f.Read(b)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
