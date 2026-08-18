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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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
//
// Phase 28.9: cfg is held in an atomic.Pointer so PUT
// /api/v1/backups/settings can swap the live configuration without
// waiting for a restart. Readers (push, snapshot, scheduler) get
// the new config on their next call; in-flight operations are
// unaffected (they already snapshotted the old cfg by value into
// their local stack). Without atomic.Pointer we'd have a
// data-race warning on the first PUT.
type Service struct {
	cfg atomic.Pointer[Config]
	db  *sql.DB
}

// New returns a backup Service.
func New(cfg Config, db *sql.DB) *Service {
	s := &Service{db: db}
	s.cfg.Store(&cfg)
	return s
}

// getCfg returns a defensive copy of the current configuration.
// Callers can mutate the returned struct freely (e.g. build a
// path) without affecting Service state.
func (s *Service) getCfg() Config {
	if p := s.cfg.Load(); p != nil {
		return *p
	}
	return Config{}
}

// UpdateConfig atomically replaces the live configuration.
// Called by the PUT handler once the new settings have been
// validated and persisted to the backup_settings DB table.
// Operators restart-dependent knobs (mirror_dir, snapshot_dir,
// db_path) keep a deferred comment in PLAN.md — only the
// remote/url/auth triplet is hot-swapped today because that's
// what fits this layer's existing call sites.
func (s *Service) UpdateConfig(cfg Config) {
	s.cfg.Store(&cfg)
}

// Config returns a defensive copy of the current live
// configuration. The listBackupSettingsHandler uses it as the
// "in-memory default" against which DB overrides are merged.
func (s *Service) Config() Config {
	return s.getCfg()
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
		cmd.Dir = s.getCfg().MirrorDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
		}
		return nil
	}

	// Init if not a repo.
	if _, err := os.Stat(filepath.Join(s.getCfg().MirrorDir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(s.getCfg().MirrorDir, 0o755); err != nil {
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
	if s.getCfg().RemoteURL == "" {
		return nil
	}
	if err := git("remote", "get-url", "origin"); err != nil {
		return git("remote", "add", "origin", s.getCfg().RemoteURL)
	}
	return git("remote", "set-url", "origin", s.getCfg().RemoteURL)
}

// CommitAndPush stages every file in MirrorDir, commits with a dated
// message, and pushes to origin. Returns ErrNoRemote when no remote is
// configured.
func (s *Service) CommitAndPush(ctx context.Context, message string) error {
	if s.getCfg().RemoteURL == "" {
		return ErrNoRemote
	}
	if err := s.EnsureGitRepo(ctx); err != nil {
		return err
	}

	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.getCfg().MirrorDir
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
// Push with snapshot (Phase 32.5 pilot task #1)
// ----------------------------------------------------------------------------

// PushWithSnapshot runs Snapshot() first, copies the latest snapshot
// into the mirror's `snapshots/` directory (so it travels with the
// next git push), writes a manifest with the hash and size, then
// calls CommitAndPush with a contextual commit message.
//
// The snapshot ends up in the mirror repo at
// `<mirror>/snapshots/orenda-LATEST.db` along with
// `<mirror>/snapshots/manifest.json`. After a fresh `git clone` of
// the mirror, an operator can:
//  1. read manifest.json → get the latest snapshot path + sha256
//  2. run `sha256sum <file>` → verify it matches
//  3. use the snapshot for restore
//
// Why the file lives in the mirror git repo (not a separate scp/rsync):
// the pilot's purpose is to close the RPO gap, not to optimise storage.
// 50MB binary tracked in git is acceptable for a small single-owner
// install; multi-tenant / large installs can swap in LFS or a separate
// object store later without changing the wire contract (manifest.json
// stays identical).
//
// Schema version is read from a fresh query so the manifest captures
// what's actually in the snapshot, not what the binary thinks was
// applied before VACUUM INTO.
func (s *Service) PushWithSnapshot(ctx context.Context) error {
	if s.getCfg().DBPath == "" {
		return ErrInvalidInput
	}
	cfg := s.getCfg()

	snapPath, err := s.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("backup push --with-snapshots: snapshot: %w", err)
	}

	// Hash the snapshot before moving it.
	hash, size, err := hashFile(snapPath)
	if err != nil {
		return fmt.Errorf("backup push --with-snapshots: hash: %w", err)
	}

	// Stage the snapshot in the mirror. The mirror's snapshots/
	// directory is created on first use and is git-tracked.
	snapshotsDir := filepath.Join(cfg.MirrorDir, "snapshots")
	if err := os.MkdirAll(snapshotsDir, 0o755); err != nil {
		return fmt.Errorf("backup push --with-snapshots: mkdir mirror/snapshots: %w", err)
	}
	dstName := "orenda-LATEST.db"
	dstPath := filepath.Join(snapshotsDir, dstName)
	if err := copyFile(snapPath, dstPath); err != nil {
		return fmt.Errorf("backup push --with-snapshots: copy to mirror: %w", err)
	}

	schemaVersion := s.schemaVersion(ctx)
	manifest := SnapshotManifest{
		Latest:        "snapshots/" + dstName,
		LatestSHA256:  hash,
		LatestSize:    size,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		SchemaVersion: schemaVersion,
	}
	manifestBytes, err := json.MarshalIndent(&manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("backup push --with-snapshots: marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(snapshotsDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return fmt.Errorf("backup push --with-snapshots: write manifest: %w", err)
	}

	// CommitAndPush uses a fixed commit message ("manual backup"). For
	// the with-snapshots variant we override it so the log entry is
	// distinguishable in `git log` and the sourcecraft UI.
	if err := s.CommitAndPush(ctx, "backup push --with-snapshots: "+manifest.Timestamp); err != nil {
		return fmt.Errorf("backup push --with-snapshots: commit/push: %w", err)
	}
	return nil
}

// LatestSnapshotFromMirror reads the manifest at
// `<mirror>/snapshots/manifest.json`, verifies the snapshot file's
// sha256 matches what the manifest recorded, and returns the local
// path inside the mirror repo. The caller (typically
// `backup restore --from <mirror>/snapshots/orenda-LATEST.db`) can
// then use the standard Restore path to swap the live DB.
//
// Returns ErrNotFound if the manifest or snapshot file is missing.
// Returns an error (wrapping ErrInvalidInput) if the sha256 doesn't
// match — half-uploaded snapshot, partial mirror clone, etc.
func (s *Service) LatestSnapshotFromMirror() (string, SnapshotManifest, error) {
	cfg := s.getCfg()
	manifestPath := filepath.Join(cfg.MirrorDir, "snapshots", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", SnapshotManifest{}, ErrNotFound
		}
		return "", SnapshotManifest{}, fmt.Errorf("backup: read manifest: %w", err)
	}
	var manifest SnapshotManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", SnapshotManifest{}, fmt.Errorf("backup: parse manifest: %w", err)
	}
	snapPath := filepath.Join(cfg.MirrorDir, manifest.Latest)
	hash, _, err := hashFile(snapPath)
	if err != nil {
		return "", SnapshotManifest{}, fmt.Errorf("backup: hash snapshot: %w", err)
	}
	if hash != manifest.LatestSHA256 {
		return "", SnapshotManifest{}, fmt.Errorf("backup: snapshot sha256 mismatch (got %s, manifest says %s) — partial mirror clone?", hash, manifest.LatestSHA256)
	}
	return snapPath, manifest, nil
}

// hashFile returns the sha256 hex digest and the byte size of the
// file at path. Reads in 64 KiB chunks to avoid loading 50+ MB
// snapshots into memory.
func hashFile(path string) (hexSHA256 string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copyFile copies src to dst, creating dst if needed. Atomic via
// tmp+rename so a half-written file isn't visible.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// schemaVersion reads the highest applied migration version from the
// schema_migrations table. The version column is TEXT (e.g. "022_…"),
// so we strip the leading numeric prefix and scan it as an int. Falls
// back to 0 if the table doesn't exist (older orenda.db before Phase
// 7's tracking table was added). Best-effort: the manifest just
// records a hint for the operator, so any read error silently
// degrades to 0 rather than failing the whole push.
func (s *Service) schemaVersion(ctx context.Context) int {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), '') FROM schema_migrations`,
	).Scan(&v)
	if err != nil || v == "" {
		return 0
	}
	// "022_study_planning" → 22. Parse the leading numeric prefix.
	for i, r := range v {
		if r < '0' || r > '9' {
			n, _ := strconv.Atoi(v[:i])
			return n
		}
	}
	n, _ := strconv.Atoi(v)
	return n
}

// ----------------------------------------------------------------------------
// SQLite snapshot (7.4)
// ----------------------------------------------------------------------------

// Snapshot creates a SQLite backup file at
// SnapshotDir/orenda-YYYYMMDD-HHMMSS.db and rotates old snapshots per
// cfg.SnapshotRotationDays. Returns the path written.
func (s *Service) Snapshot(ctx context.Context) (string, error) {
	if s.getCfg().DBPath == "" {
		return "", ErrInvalidInput
	}
	if err := os.MkdirAll(s.getCfg().SnapshotDir, 0o755); err != nil {
		return "", fmt.Errorf("backup snapshot: mkdir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(s.getCfg().SnapshotDir, "orenda-"+ts+".db")

	// VACUUM INTO refuses to overwrite; pick a unique suffix if the
	// timestamp collides with an earlier snapshot in the same second.
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(s.getCfg().SnapshotDir,
			fmt.Sprintf("orenda-%s-%02d.db", ts, i))
	}

	// Use the SQLite backup API via a direct Exec — modernc.org/sqlite
	// supports VACUUM INTO which produces a consistent snapshot.
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return "", fmt.Errorf("backup snapshot: %w", err)
	}

	// Rotate.
	if s.getCfg().SnapshotRotationDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -s.getCfg().SnapshotRotationDays)
		entries, err := os.ReadDir(s.getCfg().SnapshotDir)
		if err == nil {
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					_ = os.Remove(filepath.Join(s.getCfg().SnapshotDir, e.Name()))
				}
			}
		}
	}

	return dst, nil
}

// ListSnapshots returns snapshot files ordered newest-first.
func (s *Service) ListSnapshots(ctx context.Context) ([]SnapshotInfo, error) {
	entries, err := os.ReadDir(s.getCfg().SnapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup list: %w", err)
	}
	out := make([]SnapshotInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SnapshotInfo{
			Path:    filepath.Join(s.getCfg().SnapshotDir, e.Name()),
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

// SnapshotManifest is the manifest committed to the mirror repo each
// time `backup push --with-snapshots` runs. It records which snapshot
// file is the "latest" along with the hash and size needed to verify
// it after a restore.
//
// Phase 32.5 pilot task #1: an operator recovering from a fresh clone
// of the mirror repo should be able to find and verify the most recent
// snapshot without having to dig through history. Manifest tracks the
// hash so a half-uploaded snapshot is detectable.
type SnapshotManifest struct {
	Latest        string `json:"latest"`         // path inside mirror, e.g. "snapshots/orenda-LATEST.db"
	LatestSHA256  string `json:"latest_sha256"`  // hex sha256 of the snapshot bytes
	LatestSize    int64  `json:"latest_size"`    // bytes
	Timestamp     string `json:"timestamp"`      // ISO8601 of when manifest was written
	SchemaVersion int    `json:"schema_version"` // highest applied migration version
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

// SafetyCopyPath returns the path the CLI writes a "pre-restore" copy
// of the live database to. The suffix carries a Unix timestamp so
// multiple restores don't clobber each other's safety copies.
//
// We never overwrite an existing safety copy (restoring twice in the
// same second is the operator's problem to manage); a timestamp
// granularity of one second is good enough for the install's recovery
// workflow.
func SafetyCopyPath(destPath string, t time.Time) string {
	if destPath == "" {
		return ""
	}
	return fmt.Sprintf("%s.pre-restore-%d", destPath, t.Unix())
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

// newUUID returns a random id for backup_log rows. Delegates to
// google/uuid (already a dependency) — the previous hand-rolled
// /dev/urandom reader silently produced an all-zeros id on error.
func newUUID() string {
	return uuid.NewString()
}
