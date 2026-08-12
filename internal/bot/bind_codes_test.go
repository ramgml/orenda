package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBindCodes_IssueAndConsume pins the happy path: the bot
// issues a code on /start, the user pastes it into the UI, the
// UI hits POST /bots/telegram/bind which calls Consume(). The
// code returns the chat id and is then removed from the store.
func TestBindCodes_IssueAndConsume(t *testing.T) {
	b := newBindCodes()
	got := b.Issue(12345, "alice")
	assert.Len(t, got.Code, 6, "code is 6 hex chars")
	assert.Equal(t, int64(12345), got.ChatID)
	assert.Equal(t, "alice", got.Username)
	assert.True(t, got.Expires.After(time.Now()), "code expires in the future")

	consumed, err := b.Consume(got.Code)
	require.NoError(t, err)
	assert.Equal(t, int64(12345), consumed.ChatID)
	assert.Equal(t, "alice", consumed.Username)
}

// TestBindCodes_ConsumeOnce asserts the one-shot semantics: the
// second Consume on the same code returns ErrBindCodeUnknown.
// That's the UI's defence against a double-click.
func TestBindCodes_ConsumeOnce(t *testing.T) {
	b := newBindCodes()
	got := b.Issue(42, "")

	_, err := b.Consume(got.Code)
	require.NoError(t, err)

	_, err = b.Consume(got.Code)
	assert.ErrorIs(t, err, ErrBindCodeUnknown)
}

// TestBindCodes_Expiry makes sure a stale code can't sneak past
// the API: we backdate Expires and Consume returns ErrBindCodeExpired
// so the handler can return 410 Gone rather than 404.
func TestBindCodes_Expiry(t *testing.T) {
	b := newBindCodes()
	got := b.Issue(7, "")
	// Map values aren't addressable, so we replace the entire
	// entry with a copy that has a past expiry.
	b.mu.Lock()
	b.codes[got.Code] = BindCode{
		Code:     got.Code,
		ChatID:   got.ChatID,
		Username: got.Username,
		Expires:  time.Now().Add(-time.Second),
	}
	b.mu.Unlock()

	_, err := b.Consume(got.Code)
	assert.ErrorIs(t, err, ErrBindCodeExpired)
}

// TestBindCodes_UnknownCode pins the negative path. The API
// layer uses this same call to surface "code_unknown" without
// leaking which codes are valid (timing-safe-ish: same map lookup
// either way, but at least the error class is consistent).
func TestBindCodes_UnknownCode(t *testing.T) {
	b := newBindCodes()
	_, err := b.Consume("NOPE12")
	assert.ErrorIs(t, err, ErrBindCodeUnknown)
}

// TestBindCodes_IssueUnique asserts each Issue call returns a
// fresh code — important because the store uses code-as-key, and
// a duplicate would let the second user reuse the first user's
// bind. With 6 hex chars (24 bits) and TTL=10m the collision rate
// is negligible in practice; we just want to make sure no
// short-circuit gives us a duplicate.
func TestBindCodes_IssueUnique(t *testing.T) {
	b := newBindCodes()
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		c := b.Issue(int64(i), "")
		require.False(t, seen[c.Code], "duplicate code %s after %d issues", c.Code, i)
		seen[c.Code] = true
	}
}

// TestNewTelegram_InitialisesBindCodes guards the constructor's
// invariant — every Telegram bot starts with an empty bind-codes
// store, never nil. The API layer relies on this so it doesn't
// have to nil-check before every Consume call.
func TestNewTelegram_InitialisesBindCodes(t *testing.T) {
	tg := NewTelegram("test-token")
	require.NotNil(t, tg.BindCodes, "BindCodes must be non-nil on a fresh bot")
}
