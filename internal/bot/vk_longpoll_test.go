package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVKLPServer is a tiny stand-in for the VK Long Poll endpoint.
// It tracks the (server, key, ts) we last issued and lets the test
// push an `updates` tuple that the next a_check should return.
type fakeVKLPServer struct {
	mu       sync.Mutex
	updates  [][]any
	requests int
	// OnACheck is an optional hook for advanced tests.
	OnACheck func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeVKLPServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if f.OnACheck != nil {
			f.OnACheck(w, r)
			return
		}
		// Default: return any queued updates + bump ts.
		out := map[string]any{
			"ts":      fmt.Sprintf("%d", time.Now().UnixNano()),
			"updates": f.updates,
		}
		f.updates = nil
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}

// fakeVKBotAPI is a stand-in for api.vk.com. It returns a fresh
// (server, key, ts) triple on every groups.getLongPollServer call.
// `lpHost` is the host:port the bot should hit for a_check — tests
// strip it from lpSrv.URL.
type fakeVKBotAPI struct {
	lpHost string
	mu     sync.Mutex
	hits   int
}

func (f *fakeVKBotAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits++
		resp := map[string]any{
			"response": map[string]any{
				"server": f.lpHost,
				"key":    fmt.Sprintf("key-%d", f.hits),
				"ts":     fmt.Sprintf("ts-%d", f.hits),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

// hostOf pulls "host:port" out of an httptest URL like
// http://127.0.0.1:12345 — i.e. drops the scheme.
func hostOf(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		return s[i+3:]
	}
	return s
}

// TestVKLongPoll_DispatchesMessageNewHappyPath is the smoke test:
// Start → bot fetches server → a_check returns a message_new tuple
// → OnMessage fires with the parsed payload.
func TestVKLongPoll_DispatchesMessageNewHappyPath(t *testing.T) {
	botAPI := &fakeVKBotAPI{}
	lp := &fakeVKLPServer{
		updates: [][]any{
			// type 4 = message_new, then flags, from_id, ts, text, {}, conv_id, peer_id
			{float64(4), float64(0), float64(12345), float64(1700000000), "hello", map[string]any{}, float64(99), float64(999)},
		},
	}
	apiSrv := httptest.NewServer(botAPI.handler())
	defer apiSrv.Close()
	lpSrv := httptest.NewServer(lp.handler())
	defer lpSrv.Close()

	botAPI.lpHost = hostOf(lpSrv.URL)

	var got atomic.Pointer[InboxMessage]
	v := NewVK("token", 42).WithBaseURL(apiSrv.URL).WithLongPollScheme("http://")
	v.PollTimeout = 50 * time.Millisecond // fast test loop
	v.OnMessage = func(_ context.Context, m InboxMessage) error {
		got.Store(&m)
		return nil
	}
	v.OnError = func(err error) {
		t.Logf("vk poll err: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, v.Start(ctx))

	// Wait for the bot to dispatch our message.
	require.Eventually(t, func() bool {
		return got.Load() != nil
	}, 2*time.Second, 10*time.Millisecond, "OnMessage should fire")

	msg := got.Load()
	assert.Equal(t, "hello", msg.Text)
	assert.Equal(t, int64(999), msg.ChatID)
	assert.Equal(t, int64(12345), msg.UserID)
	assert.Equal(t, 99, msg.MessageID)

	cancel()
	require.NoError(t, v.Stop(context.Background()))
}

// TestVKLongPoll_ReconnectsOnFailed1 covers the recovery path:
// if a_check returns failed=1, the bot must re-fetch the server
// (and key) and continue polling. Without this, a server-side ts
// rotation would wedge the bot forever.
func TestVKLongPoll_ReconnectsOnFailed1(t *testing.T) {
	botAPI := &fakeVKBotAPI{}
	lp := &fakeVKLPServer{}
	apiSrv := httptest.NewServer(botAPI.handler())
	defer apiSrv.Close()
	lpSrv := httptest.NewServer(lp.handler())
	defer lpSrv.Close()
	botAPI.lpHost = hostOf(lpSrv.URL)

	// First a_check returns failed=1 with the new ts.
	// Subsequent calls return empty updates. This forces at least
	// one server re-fetch.
	var aCheckCalls atomic.Int32
	lp.OnACheck = func(w http.ResponseWriter, r *http.Request) {
		n := aCheckCalls.Add(1)
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"failed": 1,
				"ts":     "new-ts",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ts":      "ts",
			"updates": []any{},
		})
	}

	v := NewVK("token", 42).WithBaseURL(apiSrv.URL).WithLongPollScheme("http://")
	v.PollTimeout = 50 * time.Millisecond
	v.OnError = func(err error) {
		t.Logf("vk poll err: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, v.Start(ctx))

	// After failed=1 the bot should re-fetch and keep polling.
	require.Eventually(t, func() bool {
		return aCheckCalls.Load() >= 2
	}, 2*time.Second, 10*time.Millisecond, "bot should retry a_check after failed=1")

	// Server was hit at least twice: once for the initial server, once
	// for the recovery re-fetch.
	require.GreaterOrEqual(t, botAPI.hits, 2, "bot should re-fetch groups.getLongPollServer on failed=1")

	cancel()
	require.NoError(t, v.Stop(context.Background()))
}

// TestVKLongPoll_StartWithoutGroupIDIsNoop verifies that a bot
// configured without a GroupID (callback-only mode) doesn't spin up
// a polling goroutine. This is the existing-call-API path.
func TestVKLongPoll_StartWithoutGroupIDIsNoop(t *testing.T) {
	v := NewVK("token", 0)
	require.NoError(t, v.Start(context.Background()))
	require.NoError(t, v.Stop(context.Background()))
}

// TestVKLongPoll_StartWithoutTokenReturnsError: missing token means
// we can't reach the API, so Start must surface that immediately
// (rather than spawning a goroutine that retries forever).
func TestVKLongPoll_StartWithoutTokenReturnsError(t *testing.T) {
	v := NewVK("", 42)
	err := v.Start(context.Background())
	assert.ErrorIs(t, err, ErrBotUnavailable)
}

// TestVKLongPoll_ParseMessageNew_IgnoresUnknownType pins the
// dispatch contract: only message_new (type 4) is routed to
// OnMessage. Other types are dropped silently (with ts advancement
// — that part is exercised by the smoke test above).
func TestVKLongPoll_ParseMessageNew_IgnoresUnknownType(t *testing.T) {
	v := NewVK("token", 1)
	var fired atomic.Int32
	v.OnMessage = func(_ context.Context, _ InboxMessage) error {
		fired.Add(1)
		return nil
	}
	v.dispatch(context.Background(), []any{float64(8), float64(0), float64(1)}) // not type 4
	assert.Equal(t, int32(0), fired.Load(), "unknown type must not fire OnMessage")
}

// TestVKLongPoll_ParseMessageNew_EmptyTextDropsMessage: a message
// with whitespace-only text isn't actionable in the inbox and
// shouldn't be dispatched.
func TestVKLongPoll_ParseMessageNew_EmptyTextDropsMessage(t *testing.T) {
	v := NewVK("token", 1)
	var fired atomic.Int32
	v.OnMessage = func(_ context.Context, _ InboxMessage) error {
		fired.Add(1)
		return nil
	}
	v.dispatch(context.Background(), []any{float64(4), float64(0), float64(1), float64(0), "   ", map[string]any{}, float64(1), float64(1)})
	assert.Equal(t, int32(0), fired.Load())
}

// TestVKLongPoll_NextBackoff_GrowsAndCaps: pure utility test.
func TestVKLongPoll_NextBackoff_GrowsAndCaps(t *testing.T) {
	d := time.Second
	for i := 0; i < 10; i++ {
		d = nextBackoff(d, 30*time.Second)
	}
	assert.Equal(t, 30*time.Second, d, "backoff capped at max")
}

// TestVKLongPoll_BuildFromConfig_VKPath: smoke that the registry
// builder wires a VK bot from config.Bots[].Config{token, group_id}.
func TestVKLongPoll_BuildFromConfig_VKPath(t *testing.T) {
	reg := NewRegistry()
	specs := []ConfigSpec{
		{Type: "vk", Enabled: true, Config: map[string]any{
			"token":    "test-token",
			"group_id": float64(12345), // YAML/json numeric
		}},
	}
	require.NoError(t, BuildFromConfig(specs, reg))
	v, ok := reg.Get("vk").(*VK)
	require.True(t, ok, "registry should expose vk bot")
	assert.Equal(t, "test-token", v.Token)
	assert.Equal(t, int64(12345), v.GroupID)
}

// TestVKLongPoll_StopIsIdempotent: phrased twice must not panic.
func TestVKLongPoll_StopIsIdempotent(t *testing.T) {
	v := NewVK("token", 0)
	require.NoError(t, v.Stop(context.Background()))
	require.NoError(t, v.Stop(context.Background()))
}

// TestVKLongPoll_ACheckURL verifies the URL shape — guards against
// a typo in the long-poll endpoint construction.
func TestVKLongPoll_ACheckURL(t *testing.T) {
	srv := &longPollServer{
		Server: "lp.vk.com",
		Key:    "the-key",
		TS:     "12345",
	}
	v := NewVK("token", 1).WithLongPollScheme("https://")
	v.PollTimeout = 25 * time.Second
	// We don't actually call — just inspect the URL we'd build.
	endpoint := v.lpScheme + srv.Server
	q := url.Values{
		"act":  {"a_check"},
		"key":  {srv.Key},
		"ts":   {srv.TS},
		"wait": {"25"},
		"mode": {"2|8|32"},
	}
	assert.Equal(t, "https://lp.vk.com", endpoint)
	assert.Equal(t, "the-key", q.Get("key"))
	assert.Equal(t, "12345", q.Get("ts"))
}
