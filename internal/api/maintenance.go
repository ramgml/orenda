// Package api — Phase 22.3: maintenance mode.
//
// Maintenance mode is the API-side counterpart to the CLI's
// "stop the server, restore, restart" path. When the operator
// needs to restore from a snapshot while the server is running —
// typically because the UI restore button is more discoverable
// than the CLI — they POST /maintenance/on to flip a flag. The
// flag gates all write methods (POST/PUT/PATCH/DELETE) and the
// /backups/restore endpoint. WS subscribers see the disconnect,
// reconnect, and the fresh DB.
//
// Why we don't just let the UI run restore while the server is up:
// the restore swaps the on-disk SQLite file. If a writer holds the
// file at that moment, the WAL/SHM sidecars end up inconsistent.
// The CLI path works because the server is stopped first; the
// UI path achieves the same effect by refusing writes + draining
// WS during the swap.
//
// The flag is in-process only — restart resets it. That's
// intentional: a crashed restore leaves the operator back in the
// CLI path with a single `orenda backup restore --from` to recover.
package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage"
)

// maintenanceFlag is the process-wide toggle. atomic.Bool gives us
// lock-free reads on the request path; the toggle is rare and
// uses CompareAndSwap for safety.
var maintenanceFlag atomic.Bool

// MaintenanceOn flips the flag on. Returns the previous state so
// the caller can log a "already in maintenance" warning.
func MaintenanceOn() bool {
	return !maintenanceFlag.CompareAndSwap(false, true)
}

// MaintenanceOff flips the flag off.
func MaintenanceOff() bool {
	return !maintenanceFlag.CompareAndSwap(true, false)
}

// IsMaintenanceOn reports whether maintenance mode is currently
// active. The middleware uses this on every request.
func IsMaintenanceOn() bool {
	return maintenanceFlag.Load()
}

// maintenanceMiddleware rejects non-GET (and non-HEAD) methods
// while maintenance is on. The /maintenance/on and /off endpoints
// mount *outside* this middleware so the operator can still flip
// the flag.
//
// The /healthz, /info, /stats, /openapi.yaml endpoints always pass
// through (read-only) — monitoring tools shouldn't lose visibility
// during maintenance.
func maintenanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !maintenanceFlag.Load() {
			next.ServeHTTP(w, r)
			return
		}
		// Always allow safe reads + maintenance toggle + the SPA.
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			next.ServeHTTP(w, r)
			return
		}
		// Allow the maintenance toggle itself + the restore endpoint
		// + SPA fallback (any non-/api/ path serves the SPA's
		// index.html). The restore POST is the one write that is
		// DESIGNED to run under maintenance (Phase 22.3): the handler
		// refuses it without force=true and with maintenance off, so
		// exempting it here doesn't open a write path — before this
		// exemption the documented maintenance/on → restore flow was
		// unreachable (the middleware 503'd it first).
		if r.URL.Path == "/api/v1/maintenance/off" ||
			r.URL.Path == "/api/v1/maintenance/on" ||
			r.URL.Path == "/api/v1/backups/restore" ||
			r.URL.Path == "/" ||
			!strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "maintenance_mode",
			"message": "Server is in maintenance mode; only reads are allowed. " +
				"POST /api/v1/maintenance/off to resume writes.",
		})
	})
}

// maintenanceToggleHandler is a tiny endpoint pair to flip the
// flag. The mount path lives outside the maintenance middleware
// so the operator can always toggle. The toggle isn't auth-gated
// because (a) the operator has shell access to the box anyway, and
// (b) accidentally flipping maintenance is non-destructive.
func maintenanceToggleHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if action == "on" {
			wasOn := MaintenanceOn()
			writeJSON(w, http.StatusOK, map[string]any{
				"maintenance": true,
				"was_already": wasOn,
			})
			return
		}
		wasOff := MaintenanceOff()
		writeJSON(w, http.StatusOK, map[string]any{
			"maintenance": false,
			"was_already": wasOff,
		})
	}
}

// runMaintenanceVerify verifies the restore artifact for the live
// dialect (T366, wiki:storage-adapters D8: maintenance verify is
// per-dialect).
//
//   - sqlite: the artifact is the swapped live db file — open it, run
//     migrate + integrity_check + foreign_key_check (unchanged).
//   - postgres: the artifact is a pg_dump -Fc archive — RestorePostgres
//     restores it into a scratch database on the target server, checks
//     pg_restore --list readability plus the applied migrations, and
//     drops the scratch. The live database is never modified under a
//     running server; promotion is an operator action (CLI `--to`).
//
// Best-effort at the HTTP layer: the handler surfaces the error text,
// not a typed payload. svc == nil keeps the sqlite path for fixtures.
func runMaintenanceVerify(ctx context.Context, svc *backup.Service, artifactPath string) error {
	if svc != nil && svc.Dialect() == backup.DialectPostgres {
		// A real restore can legitimately take minutes; the sqlite
		// pragma pair keeps its 30s budget.
		pgCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		_, err := svc.RestorePostgres(pgCtx, artifactPath, "")
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A verified restore artifact is always a sqlite snapshot file;
	// the sqlite dialect is pinned here regardless of storage.driver.
	sdb, err := storage.Open(ctx, storage.Config{
		Driver:        string(storage.DialectSQLite),
		Path:          artifactPath,
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	if err != nil {
		return fmt.Errorf("open restored db: %w", err)
	}
	db := sdb.DB
	defer func() { _ = db.Close() }()
	if err := storage.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if sdb.Dialect() != storage.DialectSQLite {
		return fmt.Errorf("maintenance verify: not implemented for the %s driver", sdb.Dialect())
	}
	if err := runMaintenancePragma(ctx, db, "integrity_check"); err != nil {
		return err
	}
	if err := runMaintenancePragma(ctx, db, "foreign_key_check"); err != nil {
		return err
	}
	return nil
}

func runMaintenancePragma(ctx context.Context, db *sql.DB, pragma string) error {
	row := db.QueryRowContext(ctx, "PRAGMA "+pragma)
	var s string
	if err := row.Scan(&s); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("%s: %w", pragma, err)
	}
	if s != "" && s != "ok" {
		return fmt.Errorf("%s: %s", pragma, s)
	}
	return nil
}
