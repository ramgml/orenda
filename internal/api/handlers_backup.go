// Package api — Phase 7 handlers: backup settings + status + snapshots + test.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/backup"
)

// backupSettingsInput is the JSON body of PUT /backups/settings.
type backupSettingsInput struct {
	RemoteURL  string `json:"remote_url"`
	RemoteAuth string `json:"remote_auth"`
	Enabled    bool   `json:"enabled"`
}

// listBackupSettingsHandler returns the current settings.
//
// Phase 7 keeps the settings in memory (cmd/orenda already loaded them
// from config). Persisted overrides via backup_settings land in Phase 9.
func listBackupSettingsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":    deps.BackupEnabled,
			"remote_url": deps.BackupRemoteURL,
			"has_auth":   deps.BackupRemoteURL != "" && deps.BackupRemoteAuthSet,
		})
	}
}

// putBackupSettingsHandler accepts settings overrides. Phase 7 returns
// 501 — settings live in config.yaml until Phase 9 adds the backup_settings
// table write path.
func putBackupSettingsHandler(_ Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in backupSettingsInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "PUT /backups/settings is Phase 9 (config.yaml is the source of truth)",
		})
	}
}

// testBackupPushHandler runs one CommitAndPush and returns the outcome.
func testBackupPushHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Backup == nil {
			http.Error(w, "backup service not wired", http.StatusServiceUnavailable)
			return
		}
		if err := deps.Backup.CommitAndPush(r.Context(), "test push from UI"); err != nil {
			switch {
			case errors.Is(err, backup.ErrNoRemote):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no_remote"})
			case errors.Is(err, backup.ErrPushFailed):
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "push_failed"})
			default:
				writeError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "pushed"})
	}
}

// backupSnapshotHandler creates a snapshot immediately.
func backupSnapshotHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Backup == nil {
			http.Error(w, "backup service not wired", http.StatusServiceUnavailable)
			return
		}
		path, err := deps.Backup.Snapshot(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"path": path})
	}
}

// listBackupSnapshotsHandler returns the snapshot list.
func listBackupSnapshotsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Backup == nil {
			http.Error(w, "backup service not wired", http.StatusServiceUnavailable)
			return
		}
		list, err := deps.Backup.ListSnapshots(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
	}
}

// restoreBackupHandler lives in handlers_restore.go (Phase 22.3 —
// maintenance-mode path). The original handler returned a CLI
// hint; the new one accepts a `force=true` body when maintenance
// is on and runs the swap in-process.

// listBackupLogHandler returns recent backup_log entries.
func listBackupLogHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Backup == nil {
			http.Error(w, "backup service not wired", http.StatusServiceUnavailable)
			return
		}
		limit := parseLimitParam(r.URL.Query().Get("limit"), 50, 200)
		logs, err := deps.Backup.ListLog(r.Context(), limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"log": logs})
	}
}
