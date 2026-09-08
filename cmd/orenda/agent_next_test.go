package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task 188: `agent next` — peek mode and the --await long-poll loop.
//
// The cobra surface is exercised end-to-end via runAgentCLI (see
// agent_test.go) against an httptest server that records every
// request. The no-work exit (os.Exit(2)) is recorded by swapping the
// package-level osExit hook — a real exit would kill the test binary.
// printGroupedAgentTasks and `agent await` still call os.Exit
// directly; those paths are out of scope here (pre-existing).

const (
	// One ready task, human number 42.
	t188ReadyList = `{"tasks":[{"task":{"id":"t-uuid-1","number":42,"title":"Do the thing"},"ready":true}],"count":1}`
	// One NOT-ready row — the defensive branch (?ready=true should
	// never return this, but the CLI must not claim it).
	t188NotReadyList = `{"tasks":[{"task":{"id":"t-uuid-1","number":42,"title":"Do the thing"},"ready":false}],"count":1}`
	t188EmptyList    = `{"tasks":[],"count":0}`
	t188ClaimResp    = `{"task":{"id":"t-uuid-1","number":42,"title":"Do the thing"},"status":"in_progress","holder":"t188-agent"}`
)

// nextReq is one recorded request.
type nextReq struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

// nextFakeServer is a recording fake of the orenda agent API slice
// used by `agent next`: flat ready-list GETs, events/await POSTs
// (always 204 = timeout wake-up) and claim POSTs.
type nextFakeServer struct {
	srv *httptest.Server

	mu       sync.Mutex
	reqs     []nextReq
	listGets int      // flat list GETs served so far
	lists    []string // scripted list bodies; the last one repeats; empty = always empty
}

func newNextFakeServer(t *testing.T, lists ...string) *nextFakeServer {
	t.Helper()
	f := &nextFakeServer{lists: lists}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *nextFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, nextReq{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.Query(), Body: body})
	f.mu.Unlock()

	path := r.URL.EscapedPath()
	switch {
	case r.Method == http.MethodGet && path == "/api/v1/agent/tasks":
		f.mu.Lock()
		idx := f.listGets
		f.listGets++
		var out string
		switch {
		case len(f.lists) == 0:
			out = t188EmptyList
		case idx < len(f.lists):
			out = f.lists[idx]
		default:
			out = f.lists[len(f.lists)-1]
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(out))
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/events/await"):
		// 204 No Content = chunk timeout, no events (the wake-up
		// the loop treats identically to an event).
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/claim"):
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(t188ClaimResp))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unexpected request"}`))
	}
}

// snapshot returns a copy of the recorded requests.
func (f *nextFakeServer) snapshot() []nextReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nextReq(nil), f.reqs...)
}

// count returns how many recorded requests match method and path
// suffix ("" matches any path).
func (f *nextFakeServer) count(method, pathSuffix string) int {
	n := 0
	for _, r := range f.snapshot() {
		if r.Method != method {
			continue
		}
		if pathSuffix == "" || strings.HasSuffix(r.Path, pathSuffix) {
			n++
		}
	}
	return n
}

// stubExit swaps the osExit hook for a recorder and returns a pointer
// to the recorded code (-1 = never called).
func stubExit(t *testing.T) *int {
	t.Helper()
	code := -1
	prev := osExit
	osExit = func(c int) { code = c }
	t.Cleanup(func() { osExit = prev })
	return &code
}

func TestAgentNext_Peek_DoesNotClaim(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next", "--peek")
	require.NoError(t, err)

	assert.Equal(t, 1, f.count(http.MethodGet, "/api/v1/agent/tasks"))
	assert.Zero(t, f.count(http.MethodPost, "/claim"), "peek must never claim")
	assert.Contains(t, out, "#42  Do the thing  (t-uuid-1)")
	assert.Equal(t, -1, *exitCode, "non-empty peek must not exit")
}

func TestAgentNext_Peek_JsonMode_Passthrough(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next", "--peek", "--json")
	require.NoError(t, err)

	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &v), "stdout must be a JSON object: %q", out)
	assert.Equal(t, float64(1), v["count"])
	tasks, ok := v["tasks"].([]any)
	require.True(t, ok, "tasks must be an array")
	require.Len(t, tasks, 1)
	task := tasks[0].(map[string]any)["task"].(map[string]any)
	assert.Equal(t, float64(42), task["number"])
	assert.Equal(t, "t-uuid-1", task["id"])
	assert.Zero(t, f.count(http.MethodPost, "/claim"))
	assert.Equal(t, -1, *exitCode)
}

func TestAgentNext_Claim_Default(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next")
	require.NoError(t, err)

	reqs := f.snapshot()
	require.Len(t, reqs, 2, "expected one list GET then one claim POST")
	assert.Equal(t, http.MethodGet, reqs[0].Method)
	assert.Equal(t, "/api/v1/agent/tasks", reqs[0].Path)
	assert.Equal(t, "true", reqs[0].Query.Get("ready"))
	assert.Equal(t, "5", reqs[0].Query.Get("limit"), "default limit is 5")
	assert.Equal(t, http.MethodPost, reqs[1].Method)
	assert.Equal(t, "/api/v1/agent/tasks/t-uuid-1/claim", reqs[1].Path)

	assert.Contains(t, out, "#42  Do the thing  (t-uuid-1)", "candidate header")
	assert.Contains(t, out, `"in_progress"`, "claim response echoed")
	assert.Equal(t, -1, *exitCode)
}

func TestAgentNext_Peek_EmptyQueue_Exit2(t *testing.T) {
	f := newNextFakeServer(t)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next", "--peek")
	require.NoError(t, err)

	assert.Contains(t, out, "no work")
	assert.Zero(t, f.count(http.MethodPost, ""))
	assert.Equal(t, 2, *exitCode, "empty peek exits 2 like claim mode")
}

func TestAgentNext_Claim_EmptyQueue_Exit2(t *testing.T) {
	f := newNextFakeServer(t)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next")
	require.NoError(t, err)

	assert.Contains(t, out, "no work")
	assert.Equal(t, 2, *exitCode)
}

func TestAgentNext_Claim_NotReady_Exit2(t *testing.T) {
	f := newNextFakeServer(t, t188NotReadyList)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next")
	require.NoError(t, err)

	assert.Contains(t, out, "no work")
	assert.Zero(t, f.count(http.MethodPost, "/claim"), "a not-ready row must never be claimed")
	assert.Equal(t, 2, *exitCode)
}

func TestAgentNext_Await_WakesAndClaims(t *testing.T) {
	// 1st list GET: empty. After the first events/await wake-up the
	// re-list sees work and the claim path takes over.
	f := newNextFakeServer(t, t188EmptyList, t188ReadyList)
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next", "--await", "30")
	require.NoError(t, err)

	var awaitBodies []float64
	reqs := f.snapshot()
	for _, r := range reqs {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/events/await") {
			ts, ok := r.Body["timeout_s"].(float64)
			require.True(t, ok, "timeout_s must be a JSON number, got %v", r.Body["timeout_s"])
			awaitBodies = append(awaitBodies, ts)
		}
	}
	require.NotEmpty(t, awaitBodies, "at least one long-poll POST expected")
	for _, ts := range awaitBodies {
		assert.GreaterOrEqual(t, ts, float64(1), "timeout_s rounds up to >=1s")
		assert.LessOrEqual(t, ts, float64(60), "chunks capped at 60s")
	}
	assert.Equal(t, float64(30), awaitBodies[0], "first chunk = min(budget, 60) = whole budget")

	// Order: list(empty) → await → list(work) → claim.
	last := reqs[len(reqs)-1]
	require.Equal(t, http.MethodPost, last.Method)
	assert.True(t, strings.HasSuffix(last.Path, "/claim"), "claim must be the final call")

	assert.Contains(t, out, "#42  Do the thing  (t-uuid-1)")
	assert.Contains(t, out, `"in_progress"`)
	assert.Equal(t, -1, *exitCode, "claim exit 0, no recorded exit")
}

func TestAgentNext_Await_NoWork_BudgetThenExit2(t *testing.T) {
	f := newNextFakeServer(t) // always empty
	exitCode := stubExit(t)

	out, err := runAgentCLI(t, f.srv, "next", "--await", "1")
	require.NoError(t, err)

	assert.GreaterOrEqual(t, f.count(http.MethodPost, "/events/await"), 1, "at least one long-poll POST")
	assert.Greater(t, f.count(http.MethodGet, "/api/v1/agent/tasks"), 1, "loop re-listed after wake-ups")
	assert.Contains(t, out, "no work")
	assert.Equal(t, 2, *exitCode, "budget exhausted exits 2")
}

func TestAgentNext_Await_SkippedWhenWorkExists(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	_, err := runAgentCLI(t, f.srv, "next", "--await", "30")
	require.NoError(t, err)

	assert.Zero(t, f.count(http.MethodPost, "/events/await"), "work on the first list: zero long-polls")
	assert.Equal(t, 1, f.count(http.MethodGet, "/api/v1/agent/tasks"))
	assert.Equal(t, 1, f.count(http.MethodPost, "/claim"))
	assert.Equal(t, -1, *exitCode)
}

func TestAgentNext_Await_WithPeek_Rejected(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	_, err := runAgentCLI(t, f.srv, "next", "--peek", "--await", "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--await applies to claim mode (drop --peek)")
	assert.Empty(t, f.snapshot(), "rejected combo must not touch the server")
	assert.Equal(t, -1, *exitCode)
}

func TestAgentNext_Await_WithGroupBy_Rejected(t *testing.T) {
	f := newNextFakeServer(t, t188ReadyList)
	exitCode := stubExit(t)

	_, err := runAgentCLI(t, f.srv, "next", "--group-by", "project", "--await", "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--await does not combine with --group-by (it is view-only)")
	assert.Empty(t, f.snapshot(), "rejected combo must not touch the server")
	assert.Equal(t, -1, *exitCode)
}

// TestAgentNext_HelpDocumentsPeek pins the help-vs-behavior contract
// that Task 188 fixed: the flags exist and the Short line names the
// claim default plus the peek escape hatch. The original bug was help
// promising a --claim flag that did not exist.
func TestAgentNext_HelpDocumentsPeek(t *testing.T) {
	cmd := newAgentNextCmd()
	peek := cmd.Flags().Lookup("peek")
	require.NotNil(t, peek, "--peek flag must exist")
	await := cmd.Flags().Lookup("await")
	require.NotNil(t, await, "--await flag must exist")
	assert.Contains(t, cmd.Short, "--peek")
	assert.Contains(t, cmd.Short, "Claim", "Short states the claim default")
	assert.Contains(t, peek.Usage, "without claiming")
	assert.Contains(t, await.Usage, "claim mode only", "await help states its claim-mode scope")
}
