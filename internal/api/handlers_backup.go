// Package api — Phase 7 handlers: backup settings + status + snapshots + test.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ramgml/orenda/internal/backup"
)

// backupSettingsInput is the JSON body of PUT /backups/settings.
//
// Phase 28.1 (polish.1) is what gives the operator a way to set
// remote URL / auth / enabled flag from the UI without restarting
// the process — config.yaml is still the cold-start fallback the
// running `*backup.Service` was wired from (cmd/orenda/main.go).
// Settings take effect on the next process restart.
type backupSettingsInput struct {
	RemoteURL  string `json:"remote_url"`
	RemoteAuth string `json:"remote_auth"`
	Enabled    *bool  `json:"enabled"` // pointer so missing field ≠ explicit false
}

// backupSettingsResponse is the GET / PUT response shape.
//
// We surface `enabled`/`remote_url`/`has_auth` the same way the
// pre-28.1 GET did (back-compat for the existing UI), plus
// `updated_at` so the UI can show "saved 30 seconds ago".
type backupSettingsResponse struct {
	Enabled    bool   `json:"enabled"`
	RemoteURL  string `json:"remote_url"`
	HasAuth    bool   `json:"has_auth"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	SourceHint string `json:"source_hint,omitempty"`
}

// Setting keys persisted in backup_settings. We use short, stable
// names; if you change them, write a one-shot migration.
const (
	bsKeyEnabled    = "enabled"
	bsKeyRemoteURL  = "remote_url"
	bsKeyRemoteAuth = "remote_auth"
)

// listBackupSettingsHandler returns the persisted overrides (when
// present) layered over the in-memory start-time config. Because the
// operator can edit settings through the UI, the "current value"
// answer can diverge from what `*backup.Service` is actually using
// right now — `source_hint` makes that visible.
func listBackupSettingsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// In-memory defaults the running `*backup.Service` is
		// currently using — read via deps so the call works for
		// tests without an attached DB (deps.BackupSettings is nil
		// in the partial fixture).
		current := backup.Config{}
		if deps.Backup != nil {
			current = deps.Backup.Config()
		}
		settings := backupSettingsResponse{
			Enabled:   current.RemoteURL != "",
			RemoteURL: current.RemoteURL,
			HasAuth:   current.RemoteURL != "" && current.RemoteAuth != "",
		}
		// DB overrides on top: a row in backup_settings authored by
		// the UI wins over the in-memory config (it's the more
		// recent intent). Pre-Phase-28.9 the DB rows were advisory
		// only (a restart was required). After 28.9 the PUT handler
		// merges them straight back into the live Service, so the
		// distinction between in-memory and DB-merge has shrunk
		// to "is the operator's persisted intent reflected in the
		// running process right now?" — they always say yes.
		if deps.BackupSettings != nil {
			if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyEnabled); err == nil && ok {
				var on bool
				if jerr := json.Unmarshal(raw, &on); jerr == nil {
					settings.Enabled = on
				}
			}
			remoteInDB := ""
			if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyRemoteURL); err == nil && ok {
				s, _ := jsonString(raw)
				remoteInDB = s
			}
			if remoteInDB != "" {
				settings.RemoteURL = remoteInDB
				hasAuthInDB := false
				if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyRemoteAuth); err == nil && ok && len(raw) > 0 {
					hasAuthInDB = true
				}
				settings.HasAuth = hasAuthInDB
			}
		}
		// Phase 28.9: after PUT the live service already mirrors the
		// DB rows, so the SourceHint banner is no longer needed for
		// the in-process mismatch case. We keep the field in the
		// response shape for client back-compat (the UI only renders
		// it when non-empty) and emit an empty string here. If a
		// future cron-only field gains hot-reload, this is the place
		// to flag it.
		settings.SourceHint = ""
		writeJSON(w, http.StatusOK, settings)
	}
}

// jsonString is a helper to pull a string back out of json.RawMessage
// without panicking on null or non-strings; the write path always
// emits well-formed JSON, so a parse failure means we return "".
func jsonString(raw json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

// putBackupSettingsHandler persists UI-editable override settings.
// The running `*backup.Service` reads URL/auth/schedule from the
// in-memory Config it was wired with at startup — these overrides
// take effect on the next restart. See SESSION.md "Фаза «Полировка»".
func putBackupSettingsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.BackupSettings == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup_settings_repo_unavailable"})
			return
		}

		var in backupSettingsInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		ctx := r.Context()
		// Snapshot the current live configuration (post-Phase-28.9
		// this is the freshly-merged DB-only state, no in-memory
		// distortion). We use it as the default for omitted fields
		// and as the donor for restart-dependent knobs (mirror_dir,
		// snapshot_dir, db_path, rotation days) that the PUT
		// handler doesn't own — those stay in-memory only.
		var current backup.Config
		if deps.Backup != nil {
			current = deps.Backup.Config()
		}
		// Defaults: missing `enabled` → keep current. We first try
		// the DB (operator could have just toggled it there) and fall
		// back to the in-memory config.
		currentEnabled := current.RemoteURL != ""
		if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyEnabled); err == nil && ok {
			var b bool
			if json.Unmarshal(raw, &b) == nil {
				currentEnabled = b
			}
		}
		if in.Enabled == nil {
			in.Enabled = &currentEnabled
		}

		// Pull remote_url / auth from DB if the body didn't carry
		// them explicitly. Empty != "set to empty"; the only way
		// out of "has remote" is the operator clearing the field
		// and saving, which the UI handles by passing "" (we then
		// write "" over the existing value).
		if in.RemoteURL == "" {
			if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyRemoteURL); err == nil && ok {
				s, _ := jsonString(raw)
				in.RemoteURL = s
			} else {
				// Phase 28.9: fall back to the live cfg the
				// operator wired at startup (no longer a
				// separate Dependencies mirror field — the
				// Service holds the authoritative copy now).
				if deps.Backup != nil {
					in.RemoteURL = deps.Backup.Config().RemoteURL
				}
			}
		}
		if in.RemoteAuth == "" {
			if raw, ok, err := deps.BackupSettings.GetByKey(ctx, bsKeyRemoteAuth); err == nil && ok {
				s, _ := jsonString(raw)
				in.RemoteAuth = s
			}
		}

		if err := validateBackupSettingsInput(in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		if err := deps.BackupSettings.SetKey(ctx, bsKeyEnabled, boolJSON(*in.Enabled)); err != nil {
			writeBackupJSONError(w, err, "persist enabled")
			return
		}
		// Always write remote_url/auth — these JSON columns
		// persist "" too. Clear=delete is a separate path we
		// don't expose yet; clear-then-default is handled by the
		// GET merge above.
		if err := deps.BackupSettings.SetKey(ctx, bsKeyRemoteURL, jsonRaw(in.RemoteURL)); err != nil {
			writeBackupJSONError(w, err, "persist remote_url")
			return
		}
		if err := deps.BackupSettings.SetKey(ctx, bsKeyRemoteAuth, jsonRaw(in.RemoteAuth)); err != nil {
			writeBackupJSONError(w, err, "persist remote_auth")
			return
		}

		// Phase 28.9: hot-reload the live service. Push ticks and
		// the manual "Test push" button now see the new remote on
		// their next call; in-flight pushes from before the PUT
		// finish on the old URL (they snapshotted cfg into a local
		// var by value). The settings knob is fully owned by the
		// handler from here — the operator sees no restart hint.
		if deps.Backup != nil {
			deps.Backup.UpdateConfig(backup.Config{
				// Keep the restart-dependent knobs from the
				// live config — those still need a restart
				// (Phase 28.9 deliberately does NOT add hot
				// reload for filesystem paths; doing so would
				// require re-mounting git repos and snapshot
				// dirs, a bigger change than this polish item).
				MirrorDir:            current.MirrorDir,
				SnapshotDir:          current.SnapshotDir,
				DBPath:               current.DBPath,
				SnapshotRotationDays: current.SnapshotRotationDays,
				// UI-editable trio, merged into the live state.
				RemoteURL:  in.RemoteURL,
				RemoteAuth: in.RemoteAuth,
			})
		}

		// Echo the persisted state back to the caller. The body
		// shape mirrors GET so the UI reload isn't needed.
		resp := backupSettingsResponse{
			Enabled:   *in.Enabled,
			RemoteURL: in.RemoteURL,
			HasAuth:   in.RemoteAuth != "",
		}
		// Phase 28.9: no SourceHint restart banner — settings are
		// hot-applied. Kept in the response shape for backwards
		// compatibility (the UI only renders it when non-empty).
		writeJSON(w, http.StatusOK, resp)
	}
}

// validateBackupSettingsInput returns the first validation problem
// or nil. Allowed URL schemes: http(s) for plain git, ssh (for
// git@… URLs the operator pastes back from GitHub's "clone" button).
// Empty remote_url is valid — combined with enabled=false it means
// "I'm not backing up yet".
func validateBackupSettingsInput(in backupSettingsInput) error {
	if *in.Enabled && in.RemoteURL == "" {
		return errors.New("remote_url is required when backup is enabled")
	}
	if in.RemoteURL == "" {
		return nil
	}
	u, err := url.Parse(in.RemoteURL)
	if err != nil {
		return fmt.Errorf("invalid remote_url: %w", err)
	}
	switch u.Scheme {
	case "http", "https", "ssh", "git":
		// ok
	default:
		return fmt.Errorf("remote_url: unsupported scheme %q (expected http, https, ssh, git)", u.Scheme)
	}
	if u.Host == "" && strings.TrimPrefix(u.Scheme+"://", "") == in.RemoteURL {
		return errors.New("remote_url is missing host")
	}
	return nil
}

// boolJSON wraps a bool as JSON bytes for the repo. We never want
// to hand the repo a string when the column is JSON-typed — that
// route is a footgun and was the source of the null-in-value bug
// caught during Phase 28.1 review.
func boolJSON(b bool) []byte {
	out, _ := json.Marshal(b)
	return out
}

func jsonRaw(s string) []byte {
	out, _ := json.Marshal(s)
	return out
}

func writeBackupJSONError(w http.ResponseWriter, err error, op string) {
	msg := err.Error()
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "persist failed: " + op + ": " + msg,
	})
}

// testBackupPushHandler runs one CommitAndPush and returns the outcome.
func testBackupPushHandler(deps *Dependencies) http.HandlerFunc {
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
func backupSnapshotHandler(deps *Dependencies) http.HandlerFunc {
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
func listBackupSnapshotsHandler(deps *Dependencies) http.HandlerFunc {
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
func listBackupLogHandler(deps *Dependencies) http.HandlerFunc {
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
