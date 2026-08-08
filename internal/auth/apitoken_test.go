package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/auth"
)

func TestNewAPIToken_Unique(t *testing.T) {
	t1, err := auth.NewAPIToken()
	require.NoError(t, err)
	t2, err := auth.NewAPIToken()
	require.NoError(t, err)
	assert.NotEqual(t, t1, t2)
}

func TestAPIToken_Shape(t *testing.T) {
	tok, err := auth.NewAPIToken()
	require.NoError(t, err)
	// 32 bytes -> 43 base64url chars (no padding).
	assert.Len(t, tok, 43)
	// base64url alphabet only.
	for _, c := range tok {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		assert.True(t, ok, "unexpected char %q in %q", c, tok)
	}
}

func TestHashAndVerifyAPIToken(t *testing.T) {
	plain, err := auth.NewAPIToken()
	require.NoError(t, err)
	hash, err := auth.HashAPIToken(plain, 4)
	require.NoError(t, err)
	assert.NoError(t, auth.VerifyAPIToken(hash, plain))
	assert.ErrorIs(t, auth.VerifyAPIToken(hash, "wrong-plain"), auth.ErrInvalidPassword)
}

func TestNormalizeAPIToken(t *testing.T) {
	assert.Equal(t, "abc", auth.NormalizeAPIToken("  abc  "))
	assert.Equal(t, "abc", auth.NormalizeAPIToken("abc"))
	assert.Equal(t, "", auth.NormalizeAPIToken(strings.TrimSpace(" \t ")))
}
