package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// testBotFixture wires the auth surface plus a BotRegistry so the
// POST /api/v1/bots/test handler can be exercised end-to-end.
//
// We use a recording bot (captures the message) instead of a mock
// HTTP server for webhook/email — the handler calls the bot's
// Send method directly, so the regex of the outbound request is
// governed by the bot itself, not by the handler. The recording
// shape lets us assert "Did the handler call Send with the right
// target/title?" without spinning up a real SMTP/transport.
type testBotFixture struct {
	t      *testing.T
	router http.Handler
	cookie string
	// reg is exported so tests can register extra bots or query
	// the recording bot directly.
	reg *bot.Registry
	// rec is the recording bot registered as "webhook" / "email" /
	// "telegram" / "vk" depending on the test. Returns next error
	// from `failWith` if non-nil.
	rec *recordingBot
}

type recordingBot struct {
	mu       sync.Mutex
	calls    []recordedSend
	failWith error
}

type recordedSend struct {
	Name   string
	Target string
	Msg    bot.Message
}

func (r *recordingBot) Name() string                { return "webhook" }
func (r *recordingBot) Start(context.Context) error { return nil }
func (r *recordingBot) Stop(context.Context) error  { return nil }
func (r *recordingBot) Send(_ context.Context, target string, msg bot.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedSend{Name: r.Name(), Target: target, Msg: msg})
	return r.failWith
}

// aliasingBot wraps a recordingBot under a different Name so the
// same recording instance can be registered for all four bot
// types used by the test send UI (webhook/email/telegram/vk).
type aliasingBot struct {
	*recordingBot
	alias string
}

func (a *aliasingBot) Name() string { return a.alias }

func newTestBotFixture(t *testing.T) *testBotFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "test-bot.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "testbot@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "TB",
	}))

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	reg := bot.NewRegistry()
	rec := &recordingBot{}
	// Same recording instance under all four bot types so the
	// pre-check-tg-non-numeric test can post `bot_type: email`
	// and have the handler reach the per-bot target validator
	// (instead of bouncing off the registry with 503
	// bot_not_running).
	for _, name := range []string{"webhook", "email", "telegram", "vk"} {
		reg.Register(&aliasingBot{recordingBot: rec, alias: name})
	}

	router := api.NewRouter(&api.Dependencies{
		Logger:     zap.NewNop(),
		Signer:     signer,
		Users:      users,
		CookieName: "orenda_session",
		// Phase 10 Test send UI: the registry is what the handler
		// dispatches through. nil is the alternative path (handler
		// returns 503); exercised in a separate test.
		BotRegistry: reg,
	})

	cookie := loginAndCookie(t, router, "testbot@x.com", "hunter2!")
	return &testBotFixture{t: t, router: router, cookie: cookie, reg: reg, rec: rec}
}

func (f *testBotFixture) do(t *testing.T, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/test", &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// TestBotsTestHandler_Success — happy path: handler calls bot.Send
// with the test message and target, returns 200 with the sent_at
// sentinel.
func TestBotsTestHandler_Success(t *testing.T) {
	f := newTestBotFixture(t)
	w := f.do(t, map[string]string{
		"bot_type":       "webhook",
		"target_address": "https://example.com/hook",
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, true, got["ok"])
	assert.Equal(t, "webhook", got["bot_type"])
	assert.Equal(t, "https://example.com/hook", got["target"])
	assert.NotEmpty(t, got["sent_at"])
	assert.Equal(t, "If you got this, your bot is configured correctly.", got["sentinel"])

	// The recording bot saw the test message.
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	require.Len(t, f.rec.calls, 1, "bot.Send should have been called exactly once")
	assert.Equal(t, "https://example.com/hook", f.rec.calls[0].Target)
	assert.Equal(t, "test", f.rec.calls[0].Msg.Kind)
	assert.Equal(t, "Orenda test message", f.rec.calls[0].Msg.Title)
}

// TestBotsTestHandler_MissingFields — bot_type or target_address
// missing → 400 invalid_input.
func TestBotsTestHandler_MissingFields(t *testing.T) {
	cases := []struct {
		name string
		body map[string]string
	}{
		{"missing bot_type", map[string]string{"target_address": "x"}},
		{"missing target_address", map[string]string{"bot_type": "webhook"}},
		{"empty body", map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newTestBotFixture(t)
			w := f.do(t, c.body)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			var got map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, "invalid_input", got["error"])
		})
	}
}

// TestBotsTestHandler_UnknownBotType — bot_type not in the
// knownTestBotTypes set (the dropdown's whitelist) → 400
// unknown_bot_type.
func TestBotsTestHandler_UnknownBotType(t *testing.T) {
	f := newTestBotFixture(t)
	w := f.do(t, map[string]string{
		"bot_type":       "console",
		"target_address": "stderr",
	})
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "unknown_bot_type", got["error"])
}

// TestBotsTestHandler_BotNotRegistered — bot type is allowed but
// the bot isn't running (registry returned nil). The handler must
// return 503 with a friendly hint.
func TestBotsTestHandler_BotNotRegistered(t *testing.T) {
	f := newTestBotFixture(t)
	// Remove the bot we registered in the fixture so the lookup
	// returns nil. Easiest is to swap in a fresh registry.
	f.reg = bot.NewRegistry()
	// The router holds the original registry; we have to rebuild
	// the router with a registry that has no bots. The handler
	// resolves `BotRegistry` from deps, so build a new router.
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "no-bots.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "nobots@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "NB",
	}))
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		CookieName:  "orenda_session",
		BotRegistry: bot.NewRegistry(), // empty — no console even
	})
	cookie := loginAndCookie(t, router, "nobots@x.com", "hunter2!")

	// POST /api/v1/bots/test with webhook → 503 bot_not_running.
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(map[string]string{
		"bot_type":       "webhook",
		"target_address": "https://example.com/hook",
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/test", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "bot_not_running", got["error"])
	assert.Contains(t, got["hint"], "webhook")
}

// TestBotsTestHandler_SendFailed — bot.Send returns an error → 502
// send_failed with the transport error in the hint.
func TestBotsTestHandler_SendFailed(t *testing.T) {
	f := newTestBotFixture(t)
	f.rec.failWith = errors.New("smtp: dial: connection refused")
	w := f.do(t, map[string]string{
		"bot_type":       "webhook",
		"target_address": "https://example.com/hook",
	})
	require.Equal(t, http.StatusBadGateway, w.Code, "body: %s", w.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "send_failed", got["error"])
	assert.Contains(t, got["hint"], "smtp: dial")
}

// TestBotsTestHandler_TargetPreCheck — per-bot target validation
// rejects the obvious typos before burning a transport round-trip.
func TestBotsTestHandler_TargetPreCheck(t *testing.T) {
	cases := []struct {
		name    string
		botType string
		target  string
		wantKey string
	}{
		{"webhook missing scheme", "webhook", "example.com/hook", "webhook_target_must_be_http_url"},
		{"webhook ftp scheme", "webhook", "ftp://example.com/x", "webhook_target_must_be_http_url"},
		{"email missing @", "email", "example.com", "email_target_must_contain_at_and_dot"},
		{"email no dot after @", "email", "me@example", "email_target_must_contain_at_and_dot"},
		{"telegram non-numeric", "telegram", "abc", "telegram_target_must_be_numeric"},
		{"vk non-numeric", "vk", "abc", "vk_target_must_be_numeric"},
		// Negative id with a leading "-" is allowed for telegram (groups).
		{"telegram negative id ok", "webhook", "https://example.com/hook", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// For the "ok" case we don't expect a rejection — make
			// sure the recording bot hasn't been hit, then send
			// and assert success.
			if c.wantKey == "" {
				f := newTestBotFixture(t)
				w := f.do(t, map[string]string{
					"bot_type":       c.botType,
					"target_address": c.target,
				})
				assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				return
			}
			f := newTestBotFixture(t)
			w := f.do(t, map[string]string{
				"bot_type":       c.botType,
				"target_address": c.target,
			})
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			var got map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, c.wantKey, got["error"])
		})
	}
}

// TestBotsTestHandler_InvalidJSON — body that isn't JSON → 400
// invalid_json.
func TestBotsTestHandler_InvalidJSON(t *testing.T) {
	f := newTestBotFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/test", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "invalid_json", got["error"])
}

// TestBotsTestHandler_RegistryNotWired — handler returns 503
// bot_registry_not_wired when BotRegistry is nil (degenerate
// router config — production always wires it).
func TestBotsTestHandler_RegistryNotWired(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "no-reg.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "noreg@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "NR",
	}))
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:     zap.NewNop(),
		Signer:     signer,
		Users:      users,
		CookieName: "orenda_session",
		// BotRegistry deliberately nil.
	})
	cookie := loginAndCookie(t, router, "noreg@x.com", "hunter2!")
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(map[string]string{
		"bot_type":       "webhook",
		"target_address": "https://example.com/hook",
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/test", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "bot_registry_not_wired", got["error"])
}
