package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/auth"
)

func TestNewSigner_EmptySecretPanics(t *testing.T) {
	assert.Panics(t, func() {
		auth.NewSigner("", time.Hour, "orenda")
	})
}

func TestSigner_IssueAndVerify(t *testing.T) {
	s := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")

	tok, err := s.Issue("user-1", "Alice", "owner")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)

	claims, err := s.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.Subject)
	assert.Equal(t, "Alice", claims.DisplayName)
	assert.Equal(t, "owner", claims.Role)
	assert.Equal(t, "orenda", claims.Issuer)
	assert.True(t, claims.ExpiresAt.Time.After(time.Now()))
}

func TestSigner_Verify_RejectsTampered(t *testing.T) {
	s := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tok, err := s.Issue("user-1", "Alice", "owner")
	require.NoError(t, err)

	// Flip a char.
	bad := []byte(tok)
	bad[10] ^= 0x20
	_, err = s.Verify(string(bad))
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestSigner_Verify_RejectsExpired(t *testing.T) {
	s := auth.NewSigner("test-secret-32-bytes-long-xxxxx", -time.Second, "orenda")
	tok, err := s.Issue("user-1", "Alice", "owner")
	require.NoError(t, err)
	_, err = s.Verify(tok)
	require.Error(t, err)
}

func TestSigner_Verify_RejectsWrongSecret(t *testing.T) {
	s1 := auth.NewSigner("secret-one", time.Hour, "orenda")
	s2 := auth.NewSigner("secret-two", time.Hour, "orenda")
	tok, err := s1.Issue("u", "u", "owner")
	require.NoError(t, err)
	_, err = s2.Verify(tok)
	require.Error(t, err)
}
