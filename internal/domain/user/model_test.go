package user_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/user"
)

func TestUser_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(u *user.User)
		wantErr bool
	}{
		{
			name: "valid",
			mutate: func(u *user.User) {
				u.Email = "a@b.c"
				u.PasswordHash = "bcrypt$2a$..."
				u.DisplayName = "Alice"
			},
			wantErr: false,
		},
		{
			name:    "missing email",
			mutate:  func(u *user.User) { u.PasswordHash = "x"; u.DisplayName = "Alice" },
			wantErr: true,
		},
		{
			name:    "missing password hash",
			mutate:  func(u *user.User) { u.Email = "a@b.c"; u.DisplayName = "Alice" },
			wantErr: true,
		},
		{
			name:    "missing display name",
			mutate:  func(u *user.User) { u.Email = "a@b.c"; u.PasswordHash = "x" },
			wantErr: true,
		},
		{
			name: "default role",
			mutate: func(u *user.User) {
				u.Email = "a@b.c"
				u.PasswordHash = "x"
				u.DisplayName = "Alice"
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := &user.User{}
			tc.mutate(u)
			err := u.Validate()
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, user.ErrInvalidInput)
			} else {
				require.NoError(t, err)
				if u.Role == "" {
					t.Fatal("expected Validate to default the role")
				}
				assert.Equal(t, user.RoleOwner, u.Role)
			}
		})
	}
}
