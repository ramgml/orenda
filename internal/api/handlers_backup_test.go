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
