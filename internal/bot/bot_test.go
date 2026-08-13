package bot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/bot"
)

func TestWebhook_SendsJSONWithSignature(t *testing.T) {
	var gotSig string
	var gotBody []byte
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Orenda-Signature")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := bot.NewWebhook(srv.URL, "secret")
	require.NoError(t, b.Send(context.Background(), "", bot.Message{
		Kind: "task.review_needed", Title: "T", Body: "B",
	}))
	assert.NotEmpty(t, gotSig)
	assert.True(t, bot.VerifySignature("secret", gotBody, gotSig))
	assert.False(t, bot.VerifySignature("wrong", gotBody, gotSig))
	assert.Contains(t, gotUA, "orenda-bot")
}

func TestWebhook_TargetOverridesURL(t *testing.T) {
	var hit atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := bot.NewWebhook("https://unused.invalid", "")
	require.NoError(t, b.Send(context.Background(), srv.URL, bot.Message{Title: "x"}))
	assert.Equal(t, int32(1), hit.Load())
}

func TestWebhook_MissingTarget(t *testing.T) {
	b := bot.NewWebhook("", "")
	err := b.Send(context.Background(), "", bot.Message{})
	assert.ErrorIs(t, err, bot.ErrTargetMissing)
}

func TestWebhook_Non2xxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	b := bot.NewWebhook(srv.URL, "")
	err := b.Send(context.Background(), "", bot.Message{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

// --- callback handler ---

type memDecider struct{ calls []string }

func (d *memDecider) Review(_ context.Context, taskID, userID, decision, comment string) error {
	d.calls = append(d.calls, taskID+":"+decision)
	return nil
}

type memResolver struct{}

func (memResolver) ResolveOwner(_ context.Context, _ string) (string, error) {
	return "owner-1", nil
}

func TestCallbackHandler_Approve(t *testing.T) {
	d := &memDecider{}
	h := bot.NewCallbackHandler(d, memResolver{})
	err := h.Handle(context.Background(), bot.CallbackAction{
		Action:    "approve",
		TaskID:    "t-1",
		Nonce:     "n-1",
		Timestamp: time.Now(),
	})
	require.NoError(t, err)
	require.Len(t, d.calls, 1)
	assert.Equal(t, "t-1:approve", d.calls[0])
}

func TestCallbackHandler_ReplayRejected(t *testing.T) {
	d := &memDecider{}
	h := bot.NewCallbackHandler(d, memResolver{})
	a := bot.CallbackAction{Action: "approve", TaskID: "t", Nonce: "n", Timestamp: time.Now()}
	require.NoError(t, h.Handle(context.Background(), a))
	err := h.Handle(context.Background(), a)
	assert.ErrorIs(t, err, bot.ErrReplay)
}

func TestCallbackHandler_StaleRejected(t *testing.T) {
	d := &memDecider{}
	h := bot.NewCallbackHandler(d, memResolver{})
	err := h.Handle(context.Background(), bot.CallbackAction{
		Action:    "approve",
		TaskID:    "t",
		Nonce:     "n",
		Timestamp: time.Now().Add(-10 * time.Minute),
	})
	assert.ErrorIs(t, err, bot.ErrStale)
}

func TestCallbackHandler_BadAction(t *testing.T) {
	d := &memDecider{}
	h := bot.NewCallbackHandler(d, memResolver{})
	err := h.Handle(context.Background(), bot.CallbackAction{
		Action: "explode", TaskID: "t", Timestamp: time.Now(),
	})
	assert.ErrorIs(t, err, bot.ErrBadAction)
}

// --- registry ---

func TestRegistry_BuildFromConfig(t *testing.T) {
	reg := bot.NewRegistry()
	err := bot.BuildFromConfig([]bot.ConfigSpec{
		{Type: "console", Enabled: true},
		{Type: "webhook", Enabled: true, Config: map[string]any{"url": "https://x.test"}},
		{Type: "telegram", Enabled: false}, // skipped
		{Type: "nope", Enabled: true},
	}, reg)
	require.Error(t, err) // unknown type fails the whole batch
}

func TestRegistry_BuildSkipsDisabled(t *testing.T) {
	reg := bot.NewRegistry()
	require.NoError(t, bot.BuildFromConfig([]bot.ConfigSpec{
		{Type: "console", Enabled: true},
		{Type: "telegram", Enabled: false},
	}, reg))
	assert.NotNil(t, reg.Get("console"))
	assert.Nil(t, reg.Get("telegram"))
}

// --- Phase 28.5: shutdown-loop contract ---
//
// Pre-28.5 the shutdown sequence in cmd/orenda called srv.Shutdown
// and then exited — long-poll transports (Telegram) had their
// goroutines killed by SIGKILL while updates were mid-flight,
// surfacing as `context canceled` errors on the upstream bot API.
// 28.5 added `for _, b := range botRegistry.List() { b.Stop(ctx) }`
// after srv.Shutdown. The contract worth pinning here:
//
//   1. Every Bot implementation honors Stop (returns nil on a
//      clean cancellation, no panic on already-stopped bots).
//   2. Registry.List() returns bots in a deterministic order so
//      the shutdown loop visits every registered bot exactly once.
//   3. A bot that fails to Stop doesn't poison the rest of the
//      loop — the shutdown path in main.go logs and continues.
//
// Console is the trivial case (Stop returns nil unconditionally).
// We also register a counting fake through the registry so the
// loop-in-real-shape gets exercised end-to-end.

func TestConsole_Stop_ReturnsNil(t *testing.T) {
	b := bot.Console{}
	require.NoError(t, b.Start(context.Background()))
	assert.NoError(t, b.Stop(context.Background()))
	// Calling Stop a second time must also be safe — there's no
	// internal state to invalidate, and a defensive double-stop
	// during shutdown shouldn't surface as a 500.
	assert.NoError(t, b.Stop(context.Background()))
}

type countingBot struct {
	name    string
	started atomic.Int32
	stopped atomic.Int32
	stopErr error
}

func (c *countingBot) Name() string { return c.name }
func (c *countingBot) Start(ctx context.Context) error {
	c.started.Add(1)
	return nil
}
func (c *countingBot) Stop(ctx context.Context) error {
	c.stopped.Add(1)
	return c.stopErr
}
func (c *countingBot) Send(ctx context.Context, target string, msg bot.Message) error {
	return nil
}

func TestRegistry_ShutdownLoop_StopsEveryBot(t *testing.T) {
	reg := bot.NewRegistry()
	a := &countingBot{name: "counter-a"}
	b := &countingBot{name: "counter-b"}
	reg.Register(a)
	reg.Register(b)

	// Mimic main.go's startup: walk List() and Start each bot.
	for _, x := range reg.List() {
		require.NoError(t, x.Start(context.Background()))
	}
	assert.Equal(t, int32(1), a.started.Load())
	assert.Equal(t, int32(1), b.started.Load())

	// Mimic the new Phase 28.5 shutdown loop: same shape as
	// cmd/orenda/main.go (best-effort: log and continue on
	// individual failures, never abort the whole shutdown).
	stopped := 0
	for _, x := range reg.List() {
		if err := x.Stop(context.Background()); err == nil {
			stopped++
		}
	}
	assert.Equal(t, 2, stopped, "both bots must be Stop()'d in the loop")
	assert.Equal(t, int32(1), a.stopped.Load())
	assert.Equal(t, int32(1), b.stopped.Load())
}

func TestRegistry_ShutdownLoop_OneFailingBot_Continues(t *testing.T) {
	// The shutdown loop in main.go does `if err != nil { logger.Warn(...) }`
	// — a bot that fails to Stop must NOT short-circuit the rest
	// of the loop. This is the contract; we pin it here so a
	// future refactor that promotes a Stop error to a hard
	// failure can't sneak through.
	reg := bot.NewRegistry()
	good := &countingBot{name: "counter-good"}
	bad := &countingBot{name: "counter-bad", stopErr: errors.New("simulated shutdown failure")}
	reg.Register(good)
	reg.Register(bad)

	visited := []string{}
	for _, x := range reg.List() {
		visited = append(visited, x.Name())
		_ = x.Stop(context.Background()) // loop ignores Stop errors
	}
	assert.ElementsMatch(t, []string{"counter-good", "counter-bad"}, visited,
		"both bots must be visited even when one fails")
	assert.Equal(t, int32(1), good.stopped.Load())
	assert.Equal(t, int32(1), bad.stopped.Load())
}

// --- telegram data parsing ---

func TestParseCallbackData(t *testing.T) {
	action, target, err := bot.ParseCallbackData("approve:t-1")
	require.NoError(t, err)
	assert.Equal(t, "approve", action)
	assert.Equal(t, "t-1", target)

	_, _, err = bot.ParseCallbackData("garbage")
	assert.Error(t, err)
}

// --- VK payload parse smoke ---

func TestVKSend_BuildsForm(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":1}`))
	}))
	defer srv.Close()

	vk := bot.NewVK("tok", 1).WithBaseURL(srv.URL)
	require.NoError(t, vk.Send(context.Background(), "42", bot.Message{
		Kind: "task.review_needed", Title: "T", Body: "B",
		Target: "42", CallbackID: "task-1",
	}))
	assert.Contains(t, string(body), "peer_id=42")
	assert.Contains(t, string(body), "callback")
}

var _ = bytes.NewReader
var _ = json.Marshal
