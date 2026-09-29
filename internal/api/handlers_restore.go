// Package api — Phase 22.3: restoreBackupHandler with maintenance mode.
//
// Two restore paths:
//
//  1. Default (no `force=true`): the server is up and the operator
//     just POSTed here. We tell them to use the CLI (the
//     server-running guard the CLI also enforces). Same UX as
//     Phase 22's first cut; the new "use the maintenance mode" hint
//     replaces the "stop the server" hint.
//
//  2. With `force=true` AND maintenance mode on: the operator has
//     already entered maintenance (or is calling this from the
//     UI's "Restore" button which does it for them). We drain WS
//     subscribers (they'll reconnect, then pick up the restored
//     data), restore into a staging copy next to the live database,
//     run migrations + integrity + FK check on the copy (T374:
//     verify-before-swap — a failed verify removes the copy and
//     leaves the live database byte-for-byte intact), and only then
//     swap it in: safety-copy → atomic rename. On any failure we
//     exit maintenance so the operator isn't stuck.
//
// The split lets the UI wrap this endpoint with a single
// "POST /maintenance/on → POST /backups/restore → window.reload"
// sequence without the user touching the CLI.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/ramgml/orenda/internal/backup"
)

type restoreBackupRequest struct {
	Path  string `json:"path"`
	Force bool   `json:"force"` // Phase 22.3: skip the "stop the server" hint
}

func restoreBackupHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in restoreBackupRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Path == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_path"})
			return
		}
		if _, err := os.Stat(in.Path); err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "snapshot_not_found"})
				return
			}
			writeError(w, err)
			return
		}

		// Path 1: operator didn't enter maintenance — recommend the
		// CLI (which is the proven path).
		if !in.Force || !IsMaintenanceOn() {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "server_running",
				"hint": "Stop the server first, then run: orenda backup restore --from " + in.Path + " --yes\n" +
					"Or POST /api/v1/maintenance/on, then POST /api/v1/backups/restore with force=true",
				"snapshot": in.Path,
			})
			return
		}

		// Path 2: maintenance is on — drain WS, swap, verify, exit.
		if deps.Backup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_not_wired"})
			return
		}
		// T366: on postgres the artifact is a pg_dump archive and the
		// live database is never swapped under a running server — the
		// maintenance "restore" becomes a scratch-restore verify (the
		// operator promotes via `orenda backup restore --to <database>`).
		// No WS drain: the live data didn't change, subscribers stay
		// connected.
		if deps.Backup.Dialect() == backup.DialectPostgres {
			if err := runMaintenanceVerify(r.Context(), deps.Backup, in.Path); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":  "verify_failed",
					"detail": err.Error(),
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":   "verified",
				"snapshot": in.Path,
				"hint": "the dump restored into a scratch database and was verified; " +
					"the live database is untouched — promote with: orenda backup restore --from " +
					in.Path + " --to <database> --yes",
			})
			return
		}
		if deps.WSHub != nil {
			// Close every subscriber; their clients reconnect on the
			// next request and read the restored DB. We don't need to
			// wait — the subscribers see close and reconnect.
			deps.WSHub.Close()
		}
		// T374: same verify-before-swap order as the CLI. The snapshot
		// lands in a staging copy NEXT TO the live database (same
		// filesystem, so the promotion is an atomic rename); the live
		// file itself is only replaced once the copy verifies. A failed
		// verify removes the staging copy and leaves the live database
		// byte-for-byte intact.
		dbPath := deps.DBPath
		if dbPath == "" {
			dbPath = "orenda.db"
		}
		staging := backup.StagingPath(dbPath, time.Now())
		if err := backup.New(backup.Config{
			SnapshotDir: "data/backups",
			DBPath:      dbPath,
		}, nil).Restore(r.Context(), in.Path, staging); err != nil {
			backup.CleanupStaging(staging)
			// Exit maintenance so the operator isn't stuck.
			MaintenanceOff()
			if errors.Is(err, backup.ErrNotSQLite) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not_sqlite"})
				return
			}
			writeError(w, err)
			return
		}
		// Verify + migrate the staging copy: open it and check
		// integrity — the same Verify + Migrate flow the CLI runs
		// before its swap.
		if err := runMaintenanceVerify(r.Context(), deps.Backup, staging); err != nil {
			backup.CleanupStaging(staging)
			MaintenanceOff()
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":  "verify_failed",
				"detail": err.Error(),
			})
			return
		}
		// Verified — now the destructive moment. Safety-copy the live
		// database first (rollback parity with the CLI), drop the
		// destination's stale -wal/-shm sidecars so sqlite starts a
		// clean journal with the restored file, then rename the staged
		// copy in. Any failure here removes the staging copy and leaves
		// the live database untouched.
		safetyPath := ""
		if _, statErr := os.Stat(dbPath); statErr == nil {
			safetyPath = backup.SafetyCopyPath(dbPath, time.Now())
			if err := backup.CopyFile(dbPath, safetyPath); err != nil {
				backup.CleanupStaging(staging)
				MaintenanceOff()
				writeError(w, fmt.Errorf("backup restore: safety-copy %s: %w", safetyPath, err))
				return
			}
		}
		for _, side := range []string{dbPath + "-wal", dbPath + "-shm"} {
			if rmErr := os.Remove(side); rmErr != nil && !os.IsNotExist(rmErr) {
				backup.CleanupStaging(staging)
				MaintenanceOff()
				writeError(w, fmt.Errorf("backup restore: remove sidecar %s: %w", side, rmErr))
				return
			}
		}
		if err := os.Rename(staging, dbPath); err != nil {
			backup.CleanupStaging(staging)
			if safetyPath != "" {
				_ = os.Remove(safetyPath) // live was never modified; the safety copy would be dead weight
			}
			MaintenanceOff()
			writeError(w, fmt.Errorf("backup restore: swap in restored database: %w", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "restored",
			"snapshot": in.Path,
			"hint": "WS drained; clients will reconnect and pick up the restored data. " +
				"POST /api/v1/maintenance/off is automatic; reload the SPA.",
		})
		// Note: maintenance stays on after a successful restore so
		// the operator can verify the data and then explicitly exit
		// via POST /api/v1/maintenance/off. We don't auto-exit because
		// the operator may want to do more (e.g. take a fresh snapshot).
	}
}
