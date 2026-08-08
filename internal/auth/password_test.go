package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/auth"
)

func TestHashPassword_VerifyRoundtrip(t *testing.T) {
	hash, err := auth.HashPassword("hunter2", 4) // low cost for speed
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NoError(t, auth.VerifyPassword(hash, "hunter2"))
}

func TestVerifyPassword_RejectsWrongPlain(t *testing.T) {
	hash, err := auth.HashPassword("correct", 4)
	require.NoError(t, err)
	err = auth.VerifyPassword(hash, "wrong")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidPassword)
}

func TestVerifyPassword_RejectsMalformedHash(t *testing.T) {
	err := auth.VerifyPassword("not-a-bcrypt-hash", "anything")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidPassword)
}

func TestHashPassword_EmptyPlainRejected(t *testing.T) {
	_, err := auth.HashPassword("", 4)
	require.Error(t, err)
}

func TestHashPassword_CostOutOfRange(t *testing.T) {
	_, err := auth.HashPassword("x", 100)
	require.Error(t, err)
}
