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
//     data), do the atomic file swap, run migrations, integrity +
//     FK check, and exit maintenance. On any failure we exit
//     maintenance so the operator isn't stuck.
//
// The split lets the UI wrap this endpoint with a single
// "POST /maintenance/on → POST /backups/restore → window.reload"
// sequence without the user touching the CLI.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

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
		if deps.WSHub != nil {
			// Close every subscriber; their clients reconnect on the
			// next request and read the restored DB. We don't need to
			// wait — the subscribers see close and reconnect.
			deps.WSHub.Close()
		}
		// We need a fresh Service that writes to the live DB path. The
		// backup service is stateless beyond its Config, so reusing
		// the constructor is fine.
		dbPath := deps.DBPath
		if dbPath == "" {
			dbPath = "orenda.db"
		}
		if err := backup.New(backup.Config{
			SnapshotDir: "data/backups",
			DBPath:      dbPath,
		}, nil).Restore(r.Context(), in.Path, dbPath); err != nil {
			// Exit maintenance so the operator isn't stuck.
			MaintenanceOff()
			if errors.Is(err, backup.ErrNotSQLite) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not_sqlite"})
				return
			}
			writeError(w, err)
			return
		}
		// Verify + migrate: open the restored DB and check integrity.
		// The CLI version already does this end-to-end; the API
		// version delegates to the same Verify + Migrate flow.
		if err := runMaintenanceVerify(dbPath); err != nil {
			MaintenanceOff()
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":  "verify_failed",
				"detail": err.Error(),
			})
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
