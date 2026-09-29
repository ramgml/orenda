package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubOpenAI spins an httptest server that speaks just enough of the
// chat-completions wire protocol for the client tests: it captures
// the request and replies with a canned assistant message.
type stubOpenAI struct {
	srv      *httptest.Server
	gotAuth  string
	gotModel string
	gotBody  string
	status   int
	reply    string
}

// jsonQuote encodes one string as a JSON string literal (the stub
// payloads are assembled by hand, so embedding needs proper quoting).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func newStubOpenAI(t *testing.T, status int, reply string) *stubOpenAI {
	t.Helper()
	s := &stubOpenAI{status: status, reply: reply}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		s.gotBody = string(raw)
		var req chatRequest
		_ = json.Unmarshal(raw, &req)
		s.gotModel = req.Model
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.reply))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestClient_Complete_SendsContractAndParsesReply(t *testing.T) {
	s := newStubOpenAI(t, http.StatusOK, `{"choices":[{"message":{"content":"hi there"}}]}`)
	c := NewClient(s.srv.URL+"/v1", "secret-key", "test-model", time.Second)

	out, err := c.Complete(context.Background(), "be brief", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hi there", out)

	assert.Equal(t, "Bearer secret-key", s.gotAuth)
	assert.Equal(t, "test-model", s.gotModel)
	assert.Contains(t, s.gotBody, `"role":"system"`)
	assert.Contains(t, s.gotBody, `"content":"be brief"`)
	assert.Contains(t, s.gotBody, `"temperature":0`)
}

func TestClient_Complete_NoAPIKey_OmitsAuthHeader(t *testing.T) {
	s := newStubOpenAI(t, http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`)
	c := NewClient(s.srv.URL, "", "m", 0) // timeout 0 → default

	_, err := c.Complete(context.Background(), "sys", "usr")
	require.NoError(t, err)
	assert.Empty(t, s.gotAuth, "local runtimes need no bearer")
}

func TestClient_Complete_APIErrorSurfaces(t *testing.T) {
	s := newStubOpenAI(t, http.StatusUnauthorized, `{"error":"bad key"}`)
	c := NewClient(s.srv.URL, "k", "m", time.Second)

	_, err := c.Complete(context.Background(), "s", "u")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestClient_Complete_EmptyChoices(t *testing.T) {
	s := newStubOpenAI(t, http.StatusOK, `{"choices":[]}`)
	c := NewClient(s.srv.URL, "", "m", time.Second)

	_, err := c.Complete(context.Background(), "s", "u")
	assert.True(t, errors.Is(err, ErrEmptyResponse))
}

func TestGrader_ParsesVerdict(t *testing.T) {
	s := newStubOpenAI(t, http.StatusOK,
		`{"choices":[{"message":{"content":"{\"passed\": true, \"feedback\": \"same meaning\"}"}}]}`)
	g := NewGrader(NewClient(s.srv.URL, "", "m", time.Second))

	v, err := g.Grade(context.Background(), "Столица Франции?", "Париж", "город Париж")
	require.NoError(t, err)
	assert.True(t, v.Passed)
	assert.Equal(t, "same meaning", v.Feedback)
	// The prompt carries all three inputs.
	assert.Contains(t, s.gotBody, "Столица Франции?")
	assert.Contains(t, s.gotBody, "Париж")
	assert.Contains(t, s.gotBody, "город Париж")
}

func TestGrader_StripsMarkdownFences(t *testing.T) {
	content := "```json\n" + `{"passed": false, "feedback": "nope"}` + "\n```"
	s := newStubOpenAI(t, http.StatusOK,
		`{"choices":[{"message":{"content":`+jsonQuote(content)+`}}]}`)
	g := NewGrader(NewClient(s.srv.URL, "", "m", time.Second))

	v, err := g.Grade(context.Background(), "q", "e", "a")
	require.NoError(t, err)
	assert.False(t, v.Passed)
	assert.Equal(t, "nope", v.Feedback)
}

func TestGrader_MalformedVerdictIsError(t *testing.T) {
	s := newStubOpenAI(t, http.StatusOK,
		`{"choices":[{"message":{"content":"The answer looks correct to me!"}}]}`)
	g := NewGrader(NewClient(s.srv.URL, "", "m", time.Second))

	_, err := g.Grade(context.Background(), "q", "e", "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}
