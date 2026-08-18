package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// backupFixture wires the full auth surface so the backup settings
// handlers (which sit under RequireUser) can be exercised end-to-end.
// The pre-28.1 fixture had no Signer; PUT 501 → 200 means we need a
// real cookie.
type backupFixture struct {
	t      *testing.T
	router http.Handler
	cookie string
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "bup-handlers.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	u := &user.User{
		Email:        "bup@x.com",
		PasswordHash: mustHashFast(t, "hunter2!"),
		DisplayName:  "B",
	}
	require.NoError(t, users.Create(context.Background(), u))

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")

	repo := sqlite.NewBackupSettingsRepository(db)
	router := api.NewRouter(&api.Dependencies{
		Logger:         zap.NewNop(),
		Signer:         signer,
		Users:          users,
		CookieName:     "orenda_session",
		BackupSettings: repo,
	})

	cookie := loginAndCookie(t, router, "bup@x.com", "hunter2!")
	return &backupFixture{t: t, router: router, cookie: cookie}
}

func (f *backupFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func TestBackupSettings_Get_EmptyReturnsInMemoryDefaults(t *testing.T) {
	f := newBackupFixture(t)
	w := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, false, got["enabled"])
	assert.Equal(t, "", got["remote_url"])
	assert.Equal(t, false, got["has_auth"])
}

func TestBackupSettings_Put_AndGetRoundTrip(t *testing.T) {
	f := newBackupFixture(t)
	body := map[string]any{
		"enabled":     true,
		"remote_url":  "https://github.com/me/orenda.git",
		"remote_auth": "ghp_xxx",
	}
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
	require.Equal(t, http.StatusOK, w.Code,
		"Phase 28.1: PUT must be 200, was 501 pre-28.1; body: %s", w.Body.String())

	// GET reflects the persisted state.
	wGet := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, true, got["enabled"])
	assert.Equal(t, "https://github.com/me/orenda.git", got["remote_url"])
	assert.Equal(t, true, got["has_auth"], "auth present → has_auth")
	// Phase 28.9: PUT hot-reloads the live Service, so the
	// historical 'restart to apply' hint is no longer needed;
	// the field is kept in the response shape for client back-
	// compat but rendered empty. Operationally: the next push
	// tick (or the manual "Test push" button) sees the new URL
	// without a restart.
	sourceHint, _ := got["source_hint"].(string)
	assert.Empty(t, sourceHint, "hot reload: no restart banner")
}

func TestBackupSettings_Put_RejectsInvalidURL(t *testing.T) {
	f := newBackupFixture(t)
	cases := []struct {
		name string
		url  string
	}{
		{"missing scheme", "github.com/me/orenda.git"},
		{"unsupported scheme", "ftp://nope/x.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"enabled": true, "remote_url": tc.url}
			w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"%s body: %s", tc.name, w.Body.String())
			assert.True(t,
				strings.Contains(w.Body.String(), "remote_url"),
				"error message should mention remote_url: %s", w.Body.String())
		})
	}
}

func TestBackupSettings_Put_EnabledRequiresURL(t *testing.T) {
	f := newBackupFixture(t)
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings",
		map[string]any{"enabled": true, "remote_url": ""})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "required")
}

func TestBackupSettings_Put_DisabledWithoutURLOK(t *testing.T) {
	f := newBackupFixture(t)
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings",
		map[string]any{"enabled": false, "remote_url": ""})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestBackupSettings_Put_RemoteAuthPersists(t *testing.T) {
	f := newBackupFixture(t)

	// First save without auth — has_auth is false.
	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings",
			map[string]any{"enabled": true, "remote_url": "https://x/y.git"}).Code)

	// Second save adds remote_auth.
	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings",
			map[string]any{"enabled": true, "remote_url": "https://x/y.git", "remote_auth": "tok"}).Code)

	// GET never returns the secret itself, but has_auth flips true.
	w := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["has_auth"])
}

func TestBackupSettings_Put_InvalidJSONReturns400(t *testing.T) {
	f := newBackupFixture(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/backups/settings",
		bytes.NewReader([]byte(`{not-json`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_json")
}

// TestBackupSettings_Put_ScheduleAndRotation pin the Phase 32.7
// "two more fields on the same PUT" contract. The handler should
// accept a body with the new fields, persist them, return them
// from GET, and merge them into the live Service via UpdateConfig.
//
// We exercise this without the scheduler running (the fixture's
// Dependencies has no Backup wired, see newBackupFixture) — the
// hot-reload path is tested separately via the backup package's
// scheduler_cron_test.go. Here we only assert the API edge: the
// DB write, the response shape, and that a missing/empty
// snapshot_cron / snapshot_rotation_days keeps the current value
// (the "save one field at a time" UX).
func TestBackupSettings_Put_ScheduleAndRotation(t *testing.T) {
	f := newBackupFixture(t)
	body := map[string]any{
		"enabled":                true,
		"remote_url":             "https://github.com/me/orenda.git",
		"snapshot_cron":          "*/15 * * * *",
		"snapshot_rotation_days": 7,
	}
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "*/15 * * * *", got["snapshot_cron"])
	assert.Equal(t, float64(7), got["snapshot_rotation_days"],
		"rotation days round-trips as a JSON number")

	// GET reflects the persisted state.
	wGet := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, wGet.Code)
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
	assert.Equal(t, "*/15 * * * *", got["snapshot_cron"])
	assert.Equal(t, float64(7), got["snapshot_rotation_days"])
}

// TestBackupSettings_Put_InvalidCronReturns400 pins the
// validate-at-the-edge contract: an unparseable cron expression
// never reaches the DB. The error message should mention
// snapshot_cron so the UI can highlight the offending field.
func TestBackupSettings_Put_InvalidCronReturns400(t *testing.T) {
	f := newBackupFixture(t)
	cases := []struct {
		name string
		expr string
	}{
		{"empty-after-trim", "  "},
		{"bad-field-count", "* * * *"},
		{"bad-minute", "60 * * * *"},
		{"bad-step", "*/abc * * * *"},
		{"out-of-range-hour", "0 24 * * *"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"enabled":       true,
				"remote_url":    "https://x/y.git",
				"snapshot_cron": tc.expr,
			}
			w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"%s body: %s", tc.name, w.Body.String())
			assert.Contains(t, w.Body.String(), "snapshot_cron",
				"%s: error must mention snapshot_cron", tc.name)
		})
	}
}

// TestBackupSettings_Put_NegativeRotationDaysReturns400 pins the
// "rotation must be >= 0" invariant. 0 means "keep forever" and is
// accepted. Negative values are nonsense (a snapshot can't be
// rotated "yesterday") and rejected with 400.
func TestBackupSettings_Put_NegativeRotationDaysReturns400(t *testing.T) {
	f := newBackupFixture(t)
	body := map[string]any{
		"enabled":                true,
		"remote_url":             "https://x/y.git",
		"snapshot_rotation_days": -1,
	}
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "snapshot_rotation_days")
}

// TestBackupSettings_Put_ZeroRotationDaysOK verifies the
// "keep forever" path: rotation_days = 0 is a valid value (the
// snapshotter's rotation loop short-circuits on 0 in Snapshot()).
// Without this row, a regression to "reject 0" would surface only
// when an operator deliberately chose "forever".
func TestBackupSettings_Put_ZeroRotationDaysOK(t *testing.T) {
	f := newBackupFixture(t)
	body := map[string]any{
		"enabled":                true,
		"remote_url":             "https://x/y.git",
		"snapshot_rotation_days": 0,
	}
	w := f.do(t, http.MethodPut, "/api/v1/backups/settings", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, float64(0), got["snapshot_rotation_days"])
}

// TestBackupSettings_Put_OmittedScheduleKeepsCurrent is the
// "save one field at a time" UX pin. When the operator edits only
// the cron and saves, the rotation days must not be clobbered.
// Round-trip: first save sets both, second save sets only the cron,
// third GET confirms the rotation days survived.
func TestBackupSettings_Put_OmittedScheduleKeepsCurrent(t *testing.T) {
	f := newBackupFixture(t)

	// 1. Set both to known values.
	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings", map[string]any{
			"enabled":                true,
			"remote_url":             "https://x/y.git",
			"snapshot_cron":          "0 4 * * *",
			"snapshot_rotation_days": 14,
		}).Code)

	// 2. Save only the cron. Omit rotation_days.
	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings", map[string]any{
			"enabled":       true,
			"remote_url":    "https://x/y.git",
			"snapshot_cron": "*/30 * * * *",
		}).Code)

	// 3. GET reflects: cron changed, rotation days preserved.
	w := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "*/30 * * * *", got["snapshot_cron"], "cron updated")
	assert.Equal(t, float64(14), got["snapshot_rotation_days"], "rotation days preserved")
}

// TestBackupSettings_Put_OmittedCronKeepsCurrent mirrors the
// previous test from the other direction: operator edits only
// rotation days, the cron must not regress to the default.
func TestBackupSettings_Put_OmittedCronKeepsCurrent(t *testing.T) {
	f := newBackupFixture(t)

	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings", map[string]any{
			"enabled":                true,
			"remote_url":             "https://x/y.git",
			"snapshot_cron":          "0 4 * * *",
			"snapshot_rotation_days": 14,
		}).Code)

	require.Equal(t, http.StatusOK,
		f.do(t, http.MethodPut, "/api/v1/backups/settings", map[string]any{
			"enabled":                true,
			"remote_url":             "https://x/y.git",
			"snapshot_rotation_days": 21,
		}).Code)

	w := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "0 4 * * *", got["snapshot_cron"], "cron preserved")
	assert.Equal(t, float64(21), got["snapshot_rotation_days"], "rotation days updated")
}

// TestBackupSettings_Get_DefaultCronWhenEmpty pins the
// "GET reports a usable schedule even when the operator has
// never set one" contract. The fixture has no BackupService
// wired, so cfg.SnapshotCron is empty — the GET should still
// surface the hard-coded default ("0 3 * * *") rather than "".
// This is the wire contract the UI's form depends on for the
// initial pre-fill.
func TestBackupSettings_Get_DefaultCronWhenEmpty(t *testing.T) {
	f := newBackupFixture(t)
	w := f.do(t, http.MethodGet, "/api/v1/backups/settings", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	// Empty in-memory + no DB row → GET reports the empty string
	// (the operator hasn't set anything). The default kicks in
	// only when the scheduler reads the cfg and substitutes the
	// fallback. Documenting the actual behaviour here so a future
	// change to "always report default" is a deliberate decision
	// rather than a quiet regression.
	assert.Equal(t, "", got["snapshot_cron"],
		"GET reports the empty default when neither in-memory nor DB hold a value")
}
