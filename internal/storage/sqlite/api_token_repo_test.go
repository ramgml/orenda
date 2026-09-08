package sqlite

// T182: ListAllHashes must carry expires_at through to auth.TokenRow — the
// auth middleware reads the deadline from the projected row. The first cut
// projected t.TokenRow while ExpiresAt still lived as an outer StoredToken
// field, so the map always carried nil and the middleware could never see
// the deadline. This test pins the projection.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/user"
)

func TestListAllHashes_ProjectsExpiresAt(t *testing.T) {
	t.Parallel()
	db := setupAgentDB(t)
	ctx := context.Background()

	users := NewUserRepository(db)
	u := &user.User{Email: "lah-" + newUUID()[:8] + "@x.com", PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(ctx, u))
	repo := NewAPITokenRepository(db)

	// Second precision: the storage layout is "YYYY-MM-DD HH:MM:SS", so
	// truncate before writing to make the round-trip exact.
	future := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	row, err := repo.Create(ctx, u.ID, "lah-token", "fakehash", "[]", &future)
	require.NoError(t, err)
	require.NotNil(t, row.ExpiresAt, "GetByID must decode the stored deadline")

	hashes, err := repo.ListAllHashes(ctx)
	require.NoError(t, err)
	got, ok := hashes["fakehash"]
	require.True(t, ok, "token must be keyed by its hash")
	require.NotNil(t, got.ExpiresAt, "ListAllHashes must carry ExpiresAt (T182 projection)")
	assert.True(t, got.ExpiresAt.Equal(future),
		"projected deadline must equal the stored one: got %v want %v", got.ExpiresAt, future)

	// NULL expiry round-trips as nil — never expires.
	_, err = repo.Create(ctx, u.ID, "lah-token-null", "fakehash-null", "[]", nil)
	require.NoError(t, err)
	hashes, err = repo.ListAllHashes(ctx)
	require.NoError(t, err)
	assert.Nil(t, hashes["fakehash-null"].ExpiresAt, "NULL expires_at must scan as nil")
}
