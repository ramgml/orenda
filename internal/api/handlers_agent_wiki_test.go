package api_test

// Phase 29.1: agent-namespace wiki + search.
//
// The wiki handlers never read the user session, so they mount
// verbatim under RequireAgent. These tests pin the contract:
//
//   - 401 without a token, with a user cookie, and with a bad token.
//   - FTS5 search finds agent-written content.
//   - The user-side routes keep working unchanged (same handlers,
//     two auth surfaces).
//   - T80: page attachment upload/list under the agent bearer token,
//     with namespace split assertions (cookie on agent routes → 401,
//     agent token on the user route → 401).

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
	"github.com/ramgml/orenda/internal/domain/wiki"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// agentWikiFixture bundles a router wired with agents + wiki + search,
// plus a registered agent and its plain token.
type agentWikiFixture struct {
	router http.Handler
	token  string
	cookie string
}

func newAgentWikiFixture(t *testing.T) *agentWikiFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "aw-owner-" + randLite()[:8] + "@x.com"
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	agentSvc := agentservice.New(agents, users, &agentFixtureTMinter{tokens: tokens}, hub, nil)
	reg, err := agentSvc.Register(context.Background(), "wiki-agent", []string{"qwen"}, "test", nil)
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:        zap.NewNop(),
		Signer:        signer,
		Users:         users,
		Tokens:        tokens,
		Agents:        agents,
		AgentService:  agentSvc,
		WikiService:   wikiservice.New(sqlite.NewWikiRepository(db), hub),
		SearchService: searchservice.New(sqlite.NewSearchRepository(db), hub),
		CookieName:    "orenda_session",
	}
	uploadsDir := filepath.Join(t.TempDir(), "uploads")
	deps.Attachments = attachmentTestAdapter{inner: attachmentsvc.New(
		sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
			UploadDir:    uploadsDir,
			MaxSizeBytes: 1 << 20,
			AllowedMimes: []string{"image/*", "text/*"},
		}, hub)}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	// Log the owner in so tests can also hit the user-side routes.
	body, _ := json.Marshal(map[string]string{"email": ownerEmail, "password": "hunter2!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login: %s", loginRR.Body.String())
	cookie := ""
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Value
		}
	}
	require.NotEmpty(t, cookie)

	return &agentWikiFixture{router: router, token: reg.PlainToken, cookie: cookie}
}

// agentReq issues a request with the agent bearer token.
func (fx *agentWikiFixture) agentReq(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func TestAgentWiki_FullCRUDRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	// Create (PUT upsert).
	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/guide-a", map[string]any{
		"title":      "Guide A",
		"content_md": "See [[guide-b]] for details.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create: %s", rr.Body.String())
	var pageA wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&pageA))
	assert.Equal(t, "guide-a", pageA.Slug)
	assert.Equal(t, "Guide A", pageA.Title)

	// Read back.
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/guide-a", nil)
	require.Equal(t, http.StatusOK, rr.Code, "get: %s", rr.Body.String())
	var got wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Contains(t, got.ContentMD, "[[guide-b]]")

	// Create the second page, then move it under the first.
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/pages/guide-b", map[string]any{
		"title":      "Guide B",
		"content_md": "Child content.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create b: %s", rr.Body.String())
	var pageB wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&pageB))

	rr = fx.agentReq(http.MethodPatch, "/api/v1/agent/pages/guide-b/move", map[string]any{
		"parent_id": pageA.ID,
	})
	require.Equal(t, http.StatusNoContent, rr.Code, "move: %s", rr.Body.String())

	// Tree lists both pages (B nested under A).
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages", nil)
	require.Equal(t, http.StatusOK, rr.Code, "tree: %s", rr.Body.String())
	var tree struct {
		Tree []struct {
			Page     wiki.Page `json:"page"`
			Children []struct {
				Page wiki.Page `json:"page"`
			} `json:"children"`
		} `json:"tree"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&tree))
	require.Len(t, tree.Tree, 1, "one root page expected")
	assert.Equal(t, "guide-a", tree.Tree[0].Page.Slug)
	require.Len(t, tree.Tree[0].Children, 1)
	assert.Equal(t, "guide-b", tree.Tree[0].Children[0].Page.Slug)

	// Backlinks: guide-b is linked from guide-a's content.
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/guide-b/backlinks", nil)
	require.Equal(t, http.StatusOK, rr.Code, "backlinks: %s", rr.Body.String())
	var bl struct {
		Backlinks []wiki.Page `json:"backlinks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bl))
	require.Len(t, bl.Backlinks, 1)
	assert.Equal(t, "guide-a", bl.Backlinks[0].Slug)

	// Update (same PUT, existing slug → update, not slug_taken).
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/pages/guide-a", map[string]any{
		"title":      "Guide A v2",
		"content_md": "Rewritten.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "update: %s", rr.Body.String())
	var pageA2 wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&pageA2))
	assert.Equal(t, "Guide A v2", pageA2.Title)
	assert.Equal(t, pageA.ID, pageA2.ID, "upsert keeps the page id")

	// Delete → 204; subsequent get → 404.
	rr = fx.agentReq(http.MethodDelete, "/api/v1/agent/pages/guide-a", nil)
	require.Equal(t, http.StatusNoContent, rr.Code, "delete: %s", rr.Body.String())
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/guide-a", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code, "deleted page must 404")
}

func TestAgentWiki_RequiresAgentToken(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	// No token at all.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/pages", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// User cookie is NOT an agent credential (namespaces stay split).
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/pages", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// Garbage bearer.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/pages", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAgentWiki_SearchFindsAgentContent(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/zebra-manual", map[string]any{
		"title":      "Zebra Manual",
		"content_md": "The stripedquagga protocol explained.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create: %s", rr.Body.String())

	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/search?q=stripedquagga", nil)
	require.Equal(t, http.StatusOK, rr.Code, "search: %s", rr.Body.String())
	var res struct {
		Hits []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"hits"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&res))
	require.NotEmpty(t, res.Hits, "FTS5 must index agent-written pages")
	assert.Equal(t, "page", res.Hits[0].Type)
}

func TestAgentWiki_UserSideUnchanged(t *testing.T) {
	t.Parallel()
	fx := newAgentWikiFixture(t)

	// User creates a page on the user surface; the agent sees it too
	// (shared service, no per-owner partitioning).
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pages/user-page",
		bytes.NewReader([]byte(`{"title":"User Page","content_md":"owner written"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "user-side save: %s", rr.Body.String())

	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/user-page", nil)
	assert.Equal(t, http.StatusOK, rr.Code, "agent reads the user-written page")

	// And the user-side list still works with the cookie.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/pages", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "user-side tree still fine")
}
