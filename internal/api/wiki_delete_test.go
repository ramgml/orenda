package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func wikiRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/w.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "w@x.com", PasswordHash: mustHashFast(t, "hunter2!"), DisplayName: "W",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       sqlite.NewTaskRepository(db),
		Tokens:      sqlite.NewAPITokenRepository(db),
		Agents:      sqlite.NewAgentRepository(db),
		Activities:  sqlite.NewActivityRepository(db),
		SyncOps:     sqlite.NewSyncOpsRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
		WikiService: wiki.New(sqlite.NewWikiRepository(db), hub),
	}
	router := api.NewRouter(deps)

	body, _ := json.Marshal(map[string]string{"email": "w@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value
	return router, cookie
}

func TestWiki_Delete(t *testing.T) {
	router, cookie := wikiRouter(t)

	// Create a page via POST.
	pageBody, _ := json.Marshal(map[string]string{
		"slug": "to-delete", "title": "Delete me", "content_md": "# bye",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/pages", bytes.NewReader(pageBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	createRR := httptest.NewRecorder()
	router.ServeHTTP(createRR, createReq)
	require.Equal(t, http.StatusOK, createRR.Code, "create: %s", createRR.Body.String())

	// Delete the page.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/pages/to-delete", nil)
	delReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	delRR := httptest.NewRecorder()
	router.ServeHTTP(delRR, delReq)
	require.Equal(t, http.StatusNoContent, delRR.Code, "delete: %s", delRR.Body.String())

	// GET → 404.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/to-delete", nil)
	getReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR := httptest.NewRecorder()
	router.ServeHTTP(getRR, getReq)
	assert.Equal(t, http.StatusNotFound, getRR.Code, "after delete, GET should 404")
}

func TestWiki_DeleteMissing(t *testing.T) {
	router, cookie := wikiRouter(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pages/no-such", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestWiki_DeleteCascadesLinks(t *testing.T) {
	router, cookie := wikiRouter(t)

	// Create target first so the source save doesn't auto-create it.
	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "target", "title": "tgt", "content_md": "x",
	}))

	// Now create source that links to [[target]] — target already exists.
	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "source", "title": "src", "content_md": "see [[target]]",
	}))

	// Confirm backlinks for "target" include "source".
	blReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/target/backlinks", nil)
	blReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	blRR := httptest.NewRecorder()
	router.ServeHTTP(blRR, blReq)
	require.Equal(t, http.StatusOK, blRR.Code)
	var bl map[string]any
	require.NoError(t, json.Unmarshal(blRR.Body.Bytes(), &bl))
	arr, _ := bl["backlinks"].([]any)
	require.Len(t, arr, 1, "expected one backlink before delete")

	// Delete "source". Backlinks for "target" must drop to zero.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/pages/source", nil)
	delReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	delRR := httptest.NewRecorder()
	router.ServeHTTP(delRR, delReq)
	require.Equal(t, http.StatusNoContent, delRR.Code)

	blReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/pages/target/backlinks", nil)
	blReq2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	blRR2 := httptest.NewRecorder()
	router.ServeHTTP(blRR2, blReq2)
	require.Equal(t, http.StatusOK, blRR2.Code)
	require.NoError(t, json.Unmarshal(blRR2.Body.Bytes(), &bl))
	arr2, _ := bl["backlinks"].([]any)
	assert.Len(t, arr2, 0, "backlinks should be cleared after source deleted")
}

func createPage(t *testing.T, router http.Handler, cookie string, body []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "create page: %s", rr.Body.String())
}

func mustJSON(t *testing.T, m map[string]string) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}
