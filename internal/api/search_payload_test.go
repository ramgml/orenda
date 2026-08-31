package api_test

// T103: the /api/v1/search payload contract. Page hits must carry the
// wiki slug — the frontend links to /wiki/<slug>, and the page route
// resolves slugs only (a bare UUID would 404).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	searchservice "github.com/ramgml/orenda/internal/service/search"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func TestSearchPayload_PageHitsCarrySlug(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	const (
		ownerEmail = "search-payload@x.com"
		password   = "hunter2!"
		slugPrefix = "search-payload-"
	)
	slug := slugPrefix + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Searcher",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	wikis := sqlite.NewWikiRepository(db)
	_, err := wikis.Create(context.Background(), &wiki.Page{
		Slug:      slug,
		Title:     "Search payload probe",
		ContentMD: "The quasisearch word appears exactly here.",
	})
	require.NoError(t, err)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := &api.Dependencies{
		Logger:        zap.NewNop(),
		Signer:        signer,
		Users:         users,
		Tokens:        sqlite.NewAPITokenRepository(db),
		WikiService:   wikiservice.New(wikis, hub),
		SearchService: searchservice.New(sqlite.NewSearchRepository(db), hub),
		CookieName:    "orenda_session",
	}
	router := api.NewRouter(deps)
	t.Cleanup(deps.RateLimitClose)

	// Login and grab the session cookie.
	body, _ := json.Marshal(map[string]string{"email": ownerEmail, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login: %s", rr.Body.String())
	var cookie string
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Name + "=" + c.Value
		}
	}
	require.NotEmpty(t, cookie)

	// FTS over the page content, filtered to pages only.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/search?q=quasisearch&type=page&limit=20", nil)
	req.Header.Set("Cookie", cookie)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "search: %s", rr.Body.String())

	var payload struct {
		Hits []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"hits"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Hits, "expected at least one page hit")
	hit := payload.Hits[0]
	assert.Equal(t, "page", hit.Type)
	assert.Equal(t, slug, hit.Slug, "page hit must carry the wiki slug")
	assert.NotEmpty(t, hit.ID)
}
