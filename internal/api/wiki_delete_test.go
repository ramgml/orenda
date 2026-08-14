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
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "w@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value
	return router, cookie
}

// PUT /pages/{slug} with only title + content_md (no slug in body)
// must succeed — the Save button on the wiki editor sends this exact
// shape, and the handler must take the slug from the URL. Regression
// test for the 500 that the Save button used to produce.
func TestWiki_PutUpdatesWithoutSlugInBody(t *testing.T) {
	router, cookie := wikiRouter(t)

	// Seed a page.
	seedBody, _ := json.Marshal(map[string]string{
		"slug": "notes", "title": "Old title", "content_md": "old body",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/pages", bytes.NewReader(seedBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	createRR := httptest.NewRecorder()
	router.ServeHTTP(createRR, createReq)
	require.Equal(t, http.StatusOK, createRR.Code, "create: %s", createRR.Body.String())

	// PUT with only title + content_md. No slug.
	putBody, _ := json.Marshal(map[string]string{
		"title": "Updated title", "content_md": "updated body",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/pages/notes", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	putRR := httptest.NewRecorder()
	router.ServeHTTP(putRR, putReq)
	require.Equal(t, http.StatusOK, putRR.Code, "put: %s", putRR.Body.String())

	var updated map[string]any
	require.NoError(t, json.Unmarshal(putRR.Body.Bytes(), &updated))
	assert.Equal(t, "notes", updated["slug"], "URL slug is authoritative")
	assert.Equal(t, "Updated title", updated["title"])
	assert.Equal(t, "updated body", updated["content_md"])
}

// PATCH /pages/{slug}/move with parent_id moves the page under that
// parent. The next Tree fetch must show it as a child.
func TestWiki_Move_ToParent(t *testing.T) {
	router, cookie := wikiRouter(t)

	// Create parent + child at root.
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "docs", "title": "Docs"}))
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "draft", "title": "Draft"}))

	// Look up the parent's id via the tree.
	treeReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages", nil)
	treeReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	treeRR := httptest.NewRecorder()
	router.ServeHTTP(treeRR, treeReq)
	require.Equal(t, http.StatusOK, treeRR.Code)
	var treeResp struct {
		Tree []struct {
			Page struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"page"`
			Children []struct {
				Page struct {
					ID string `json:"id"`
				} `json:"page"`
			} `json:"children"`
		} `json:"tree"`
	}
	require.NoError(t, json.Unmarshal(treeRR.Body.Bytes(), &treeResp))

	var parentID string
	for _, n := range treeResp.Tree {
		if n.Page.Slug == "docs" {
			parentID = n.Page.ID
		}
	}
	require.NotEmpty(t, parentID, "docs page must exist in tree")

	// Move "draft" under "docs".
	moveBody, _ := json.Marshal(map[string]string{"parent_id": parentID})
	moveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/draft/move", bytes.NewReader(moveBody))
	moveReq.Header.Set("Content-Type", "application/json")
	moveReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	moveRR := httptest.NewRecorder()
	router.ServeHTTP(moveRR, moveReq)
	require.Equal(t, http.StatusNoContent, moveRR.Code, "move: %s", moveRR.Body.String())

	// Tree now nests draft under docs.
	treeReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/pages", nil)
	treeReq2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	treeRR2 := httptest.NewRecorder()
	router.ServeHTTP(treeRR2, treeReq2)
	require.Equal(t, http.StatusOK, treeRR2.Code)
	require.NoError(t, json.Unmarshal(treeRR2.Body.Bytes(), &treeResp))
	for _, n := range treeResp.Tree {
		if n.Page.Slug == "docs" {
			require.Len(t, n.Children, 1, "docs should have one child")
			// We don't have slug on TreeNode.Page — just verify the id
			// matches the child's id by fetching the page list.
		}
	}
	// Confirm via direct page lookup: get draft and check parent_id.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/draft", nil)
	getReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR := httptest.NewRecorder()
	router.ServeHTTP(getRR, getReq)
	var draft map[string]any
	require.NoError(t, json.Unmarshal(getRR.Body.Bytes(), &draft))
	assert.NotEmpty(t, draft["parent_id"], "draft should have parent_id set")
}

// Moving a page under itself is a 400 (cycle).
func TestWiki_Move_RejectsSelf(t *testing.T) {
	router, cookie := wikiRouter(t)
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "a", "title": "A"}))

	// Get A's id via GET.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/a", nil)
	getReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR := httptest.NewRecorder()
	router.ServeHTTP(getRR, getReq)
	require.Equal(t, http.StatusOK, getRR.Code)
	var page map[string]any
	require.NoError(t, json.Unmarshal(getRR.Body.Bytes(), &page))
	id := page["id"].(string)

	// Try to move A under itself.
	moveBody, _ := json.Marshal(map[string]string{"parent_id": id})
	moveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/a/move", bytes.NewReader(moveBody))
	moveReq.Header.Set("Content-Type", "application/json")
	moveReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	moveRR := httptest.NewRecorder()
	router.ServeHTTP(moveRR, moveReq)
	assert.Equal(t, http.StatusBadRequest, moveRR.Code, "self-move should 400")
}

// Moving a parent under its own child would create a cycle → 400.
func TestWiki_Move_RejectsCycle(t *testing.T) {
	router, cookie := wikiRouter(t)
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "parent", "title": "Parent"}))
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "child", "title": "Child"}))

	// Make child a child of parent.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/parent", nil)
	getReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR := httptest.NewRecorder()
	router.ServeHTTP(getRR, getReq)
	var parent map[string]any
	require.NoError(t, json.Unmarshal(getRR.Body.Bytes(), &parent))
	parentID := parent["id"].(string)

	moveBody, _ := json.Marshal(map[string]string{"parent_id": parentID})
	mReq := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/child/move", bytes.NewReader(moveBody))
	mReq.Header.Set("Content-Type", "application/json")
	mReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	mRR := httptest.NewRecorder()
	router.ServeHTTP(mRR, mReq)
	require.Equal(t, http.StatusNoContent, mRR.Code)

	// Now try to move parent under child — should reject.
	// Get child id.
	getReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/pages/child", nil)
	getReq2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR2 := httptest.NewRecorder()
	router.ServeHTTP(getRR2, getReq2)
	var child map[string]any
	require.NoError(t, json.Unmarshal(getRR2.Body.Bytes(), &child))
	childID := child["id"].(string)

	moveBody3, _ := json.Marshal(map[string]string{"parent_id": childID})
	mReq2 := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/parent/move", bytes.NewReader(moveBody3))
	mReq2.Header.Set("Content-Type", "application/json")
	mReq2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	mRR2 := httptest.NewRecorder()
	router.ServeHTTP(mRR2, mReq2)
	assert.Equal(t, http.StatusBadRequest, mRR2.Code, "cycle move should 400")
}

// Move to root by sending empty parent_id.
func TestWiki_Move_ToRoot(t *testing.T) {
	router, cookie := wikiRouter(t)
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "p", "title": "P"}))
	createPage(t, router, cookie, mustJSON(t, map[string]string{"slug": "c", "title": "C"}))

	// Get IDs.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/pages/p", nil)
	getReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	getRR := httptest.NewRecorder()
	router.ServeHTTP(getRR, getReq)
	var p map[string]any
	require.NoError(t, json.Unmarshal(getRR.Body.Bytes(), &p))

	// Move c under p.
	body1, _ := json.Marshal(map[string]string{"parent_id": p["id"].(string)})
	r1 := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/c/move", bytes.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	r1.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, r1)
	require.Equal(t, http.StatusNoContent, rr1.Code)

	// Move c back to root (empty parent_id).
	body2, _ := json.Marshal(map[string]string{"parent_id": ""})
	r2 := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/c/move", bytes.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	r2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, r2)
	assert.Equal(t, http.StatusNoContent, rr2.Code, "body: %s", rr2.Body.String())
}

// Russian (Cyrillic) title + auto-Latin slug is a valid save too.
func TestWiki_PutAcceptsNonASCIITitle(t *testing.T) {
	router, cookie := wikiRouter(t)
	putBody, _ := json.Marshal(map[string]string{
		"title":      "Обновлённый заголовок",
		"content_md": "текст на русском",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/pages/cyrillic", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	putRR := httptest.NewRecorder()
	router.ServeHTTP(putRR, putReq)
	require.Equal(t, http.StatusOK, putRR.Code, "put: %s", putRR.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(putRR.Body.Bytes(), &out))
	assert.Equal(t, "cyrillic", out["slug"])
	assert.Equal(t, "Обновлённый заголовок", out["title"])
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
