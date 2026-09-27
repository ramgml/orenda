package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveDSN covers the D4 priority chain: an explicit dsn wins
// outright; the parts assemble into a libpq URL with defaults; a
// missing database is an error instead of a guess.
func TestResolveDSN(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RuntimeConfig
		want    string
		wantErr string
	}{
		{
			name: "dsn wins over parts",
			cfg: RuntimeConfig{
				DSN:      "postgres://u:p@other:9999/x",
				Host:     "ignored",
				Port:     1,
				User:     "ignored",
				Database: "ignored-too",
			},
			want: "postgres://u:p@other:9999/x",
		},
		{
			name: "full parts with sslmode",
			cfg: RuntimeConfig{
				Host:     "db.local",
				Port:     5433,
				User:     "orenda",
				Password: "pw",
				Database: "orenda",
				SSLMode:  "require",
			},
			want: "postgres://orenda:pw@db.local:5433/orenda?sslmode=require",
		},
		{
			name: "defaults for host, port, sslmode",
			cfg:  RuntimeConfig{Database: "orenda"},
			want: "postgres://127.0.0.1:5432/orenda",
		},
		{
			name: "user without password",
			cfg:  RuntimeConfig{User: "orenda", Database: "orenda"},
			want: "postgres://orenda@127.0.0.1:5432/orenda",
		},
		{
			name:    "missing database rejected",
			cfg:     RuntimeConfig{Host: "db.local"},
			wantErr: "database is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDSN(tc.cfg)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestBuildEmbeddedOptions covers the bootstrap defaults: port 5433,
// postgres/postgres credentials, and the hard requirement for a
// database name.
func TestBuildEmbeddedOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		opts, err := BuildEmbeddedOptions("orenda", 0, "", "")
		require.NoError(t, err)
		assert.Equal(t, DefaultEmbeddedPort, opts.Port)
		assert.Equal(t, DefaultEmbeddedUser, opts.Username)
		assert.Equal(t, DefaultEmbeddedPassword, opts.Password)
		assert.Equal(t, "orenda", opts.Database)
	})

	t.Run("overrides respected", func(t *testing.T) {
		opts, err := BuildEmbeddedOptions("orenda", 21561, "boots", "secret")
		require.NoError(t, err)
		assert.Equal(t, 21561, opts.Port)
		assert.Equal(t, "boots", opts.Username)
		assert.Equal(t, "secret", opts.Password)
	})

	t.Run("database required", func(t *testing.T) {
		_, err := BuildEmbeddedOptions("", 0, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires a database name")
	})
}

// TestEmbeddedConnection pins the dial shape of an embedded cluster:
// loopback, the cluster port and bootstrap credentials, sslmode off —
// initdb creates no certificates.
func TestEmbeddedConnection(t *testing.T) {
	opts, err := BuildEmbeddedOptions("orenda", 0, "", "")
	require.NoError(t, err)
	conn := opts.Connection()
	assert.Equal(t, RuntimeConfig{
		Host:     "127.0.0.1",
		Port:     DefaultEmbeddedPort,
		User:     DefaultEmbeddedUser,
		Password: DefaultEmbeddedPassword,
		Database: "orenda",
		SSLMode:  "disable",
	}, conn)

	dsn, err := ResolveDSN(conn)
	require.NoError(t, err)
	assert.Equal(t, "postgres://postgres:postgres@127.0.0.1:5433/orenda?sslmode=disable", dsn)
}

// TestTargetDescription covers the log-safe rendering: parts get
// defaults filled in; a DSN is reduced to its parsed target (the
// credential-carrying userinfo must never reach a log line).
func TestTargetDescription(t *testing.T) {
	tests := []struct {
		name string
		cfg  RuntimeConfig
		want string
	}{
		{name: "parts with defaults", cfg: RuntimeConfig{Database: "orenda"}, want: "127.0.0.1:5432/orenda"},
		{name: "parts explicit", cfg: RuntimeConfig{Host: "db.local", Port: 5433, Database: "orenda"}, want: "db.local:5433/orenda"},
		{
			name: "dsn reduced, no credentials",
			cfg:  RuntimeConfig{DSN: "postgres://user:secret@db.local:5433/orenda?sslmode=disable"},
			want: "db.local:5433/orenda",
		},
		{name: "unparseable dsn", cfg: RuntimeConfig{DSN: "://nope"}, want: "postgres (unparseable dsn)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TargetDescription(tc.cfg))
		})
	}
}
