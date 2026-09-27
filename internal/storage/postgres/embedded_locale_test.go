package postgres

// T365 review fix: the embedded cluster locale is pinned to a UTF-8
// locale with a C-like fail-fast — byte-order C/POSIX ctype folds no
// non-ASCII case, so Cyrillic full-text search would silently miss
// matches. These tests cover the pure assert logic (ungated) and the
// real fail-fast path (gated integration, same gate as
// TestEmbeddedLifecycle).

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLikeLocale(t *testing.T) {
	tests := []struct {
		locale string
		want   bool
	}{
		{"C", true},
		{"POSIX", true},
		{"posix", true},
		{" c ", true},
		// The C.UTF-8 alias is NOT the byte-order C collation: it is a
		// real UTF-8 locale and must pass the assert.
		{"C.UTF-8", false},
		{"C.utf8", false},
		{"en_US.UTF-8", false},
		{"ru_RU.UTF-8", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, cLikeLocale(tt.locale), "cLikeLocale(%q)", tt.locale)
	}
}

func TestLocaleIssue(t *testing.T) {
	tests := []struct {
		name     string
		ctype    string
		collate  string
		wantFail bool
		wantIn   string
	}{
		{name: "utf8 ok", ctype: "C.UTF-8", collate: "C.UTF-8"},
		{name: "national utf8 ok", ctype: "ru_RU.UTF-8", collate: "ru_RU.UTF-8"},
		{name: "ctype C", ctype: "C", collate: "C.UTF-8", wantFail: true, wantIn: "datctype"},
		{name: "collate POSIX", ctype: "C.UTF-8", collate: "POSIX", wantFail: true, wantIn: "datcollate"},
		{name: "both C", ctype: "C", collate: "C", wantFail: true, wantIn: "datctype"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := localeIssue(tt.ctype, tt.collate)
			if !tt.wantFail {
				assert.Empty(t, issue)
				return
			}
			require.NotEmpty(t, issue)
			assert.Contains(t, issue, tt.wantIn)
		})
	}
}

// TestEmbedded_CTypeFailsFast proves acceptance (б): a cluster pinned
// to a C-like locale does not start silently — StartEmbedded fails
// loudly with the remediation, and the postmaster does not outlive the
// failed start. Gated like the lifecycle smoke:
//
//	ORENDA_TEST_EMBEDDED_PG=1 go test ./internal/storage/postgres/ -run TestEmbedded_CTypeFailsFast -v
func TestEmbedded_CTypeFailsFast(t *testing.T) {
	if os.Getenv("ORENDA_TEST_EMBEDDED_PG") != "1" {
		t.Skip("integration smoke: set ORENDA_TEST_EMBEDDED_PG=1 to run the embedded lifecycle against a real postmaster")
	}

	dataPath := filepath.Join(t.TempDir(), "postgres")
	scratch, err := ScratchRuntimePath()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	opts, err := BuildEmbeddedOptions("orenda_t365_ctype", freeTestPort(t), "", "")
	require.NoError(t, err)
	opts.DataPath = dataPath
	opts.RuntimePath = scratch
	opts.Logs = testLogWriter{t: t}
	opts.Locale = "C"

	emb, err := StartEmbedded(opts)
	require.Error(t, err, "a C-locale cluster must fail the locale assert, not start silently")
	assert.Nil(t, emb, "the failed cluster must be stopped by StartEmbedded itself")
	assert.Contains(t, err.Error(), "C-like", "the error must name the broken locale")
	assert.Contains(t, err.Error(), dataPath, "the error must point at the data directory to reinitialize")
	requireNoPostmaster(t, dataPath)
	assert.NoFileExists(t, filepath.Join(dataPath, "postmaster.pid"))
}

// freeTestPort reserves an ephemeral loopback port and releases it, so
// the smoke never collides with a real embedded cluster on the default
// 5433 (mirrors pgtest.freePort).
func freeTestPort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}
