package api

// T190: constant-work verifyAPIToken.
//
// verifyAPIToken must be constant-work with respect to WHICH api_tokens row
// (if any) matches: every request performs a full bcrypt pass over all
// stored hashes and the outcome is decided only after the pass completes
// (see the T190 paragraph on verifyAPIToken in auth.go). Per the task
// contract there are NO timing assertions here — wall-clock timing tests are
// flaky by nature. Instead, these tests pin the observable behavior that the
// constant-work shape must preserve, plus one structural fact (no early exit
// in the loop).
//
// This file is package api (not api_test) because verifyAPIToken and
// errAPITokenNotFound are unexported.

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/auth"
)

// stubTokenLookup is the internal-package twin of api_test.fakeTokenRepo:
// a minimal api.TokenLookup over an in-memory hash→row map.
type stubTokenLookup struct {
	hashes map[string]*auth.TokenRow
}

func (r *stubTokenLookup) ListAllHashes(_ context.Context) (map[string]auth.TokenRow, error) {
	out := make(map[string]auth.TokenRow, len(r.hashes))
	for k, v := range r.hashes {
		out[k] = *v
	}
	return out, nil
}

func (r *stubTokenLookup) TouchLastUsed(_ context.Context, _ string) error { return nil }

// seedStubRepo builds a stubTokenLookup with the given plaintext tokens, in
// seed order: i-th plaintext gets ID/UserID suffix string('a'+i). hashCost
// is kept low (4) so the suite stays fast; the tests assert behavior, not
// duration.
func seedStubRepo(t *testing.T, plains ...string) (*stubTokenLookup, []string) {
	t.Helper()
	repo := &stubTokenLookup{hashes: make(map[string]*auth.TokenRow, len(plains))}
	hashed := make([]string, len(plains))
	for i, p := range plains {
		h, err := auth.HashAPIToken(p, 4)
		require.NoError(t, err)
		hashed[i] = h
		repo.hashes[h] = &auth.TokenRow{
			ID:     "tok-" + string(rune('a'+i)),
			UserID: "user-" + string(rune('a'+i)),
			Name:   "seeded-" + string(rune('a'+i)),
			Hash:   h,
		}
	}
	return repo, hashed
}

// Each subtest gets its OWN fixture: subtests run in parallel, so sharing
// one repo (and mutating its ExpiresAt) would be a data race.
func TestVerifyAPIToken_FullPassBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plain     string
		expiresAt *time.Time // applied to the FIRST seeded row
		wantUser  string     // "" → expect errAPITokenNotFound
		seed      func(t *testing.T) (*stubTokenLookup, []string)
	}{
		{
			name:     "(a) valid token among several -> its row",
			plain:    "token-alpha",
			wantUser: "user-a", // seeded 1st
			seed: func(t *testing.T) (*stubTokenLookup, []string) {
				return seedStubRepo(t, "token-alpha", "token-beta", "token-gamma")
			},
		},
		{
			name:     "(a2) valid token among several -> its own row (other plaintext)",
			plain:    "token-gamma",
			wantUser: "user-b", // seed order: alpha, gamma, beta → gamma is 2nd
			seed: func(t *testing.T) (*stubTokenLookup, []string) {
				return seedStubRepo(t, "token-alpha", "token-gamma", "token-beta")
			},
		},
		{
			name:     "(c) unknown token -> errAPITokenNotFound",
			plain:    "token-unknown",
			wantUser: "",
			seed: func(t *testing.T) (*stubTokenLookup, []string) {
				return seedStubRepo(t, "token-alpha", "token-beta", "token-gamma")
			},
		},
		{
			name:      "(b) matching but expired row -> errAPITokenNotFound",
			plain:     "token-alpha",
			expiresAt: new(time.Now().UTC().Add(-time.Hour)),
			wantUser:  "",
			seed: func(t *testing.T) (*stubTokenLookup, []string) {
				return seedStubRepo(t, "token-alpha", "token-beta", "token-gamma")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, hashes := tt.seed(t)
			if tt.expiresAt != nil {
				repo.hashes[hashes[0]].ExpiresAt = tt.expiresAt
			}

			row, err := verifyAPIToken(context.Background(), repo, tt.plain)

			if tt.wantUser == "" {
				require.ErrorIs(t, err, errAPITokenNotFound)
				assert.Nil(t, row)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, row)
			assert.Equal(t, tt.wantUser, row.UserID)
		})
	}
}

// (d) expired and unknown must land on the SAME sentinel so the caller
// (RequireAgent) cannot — and the wire cannot — tell them apart. The second
// row stays live; the expired probe uses the second plaintext, the unknown
// probe a never-minted one.
func TestVerifyAPIToken_ExpiredEqualsUnknownSentinel(t *testing.T) {
	t.Parallel()
	repo, hashes := seedStubRepo(t, "token-live", "token-doomed")

	expired := time.Now().UTC().Add(-time.Hour)
	repo.hashes[hashes[0]].ExpiresAt = &expired // token-live expired
	_, errExpired := verifyAPIToken(context.Background(), repo, "token-live")
	_, errUnknown := verifyAPIToken(context.Background(), repo, "token-never-minted")

	require.ErrorIs(t, errExpired, errAPITokenNotFound)
	require.ErrorIs(t, errUnknown, errAPITokenNotFound)
	assert.Equal(t, errExpired, errUnknown, "expired and unknown must return the identical sentinel error")
}

// Structural sanity (not a timing test): inside the range loop the body must
// contain no break and no return — every stored hash must be compared on
// every request, or response time would correlate with the match position
// (timing oracle, CWE-208).
func TestVerifyAPIToken_StructureNoEarlyExit(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	require.NoError(t, err)
	lines := strings.Split(string(src), "\n")

	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "func verifyAPIToken(") {
			start = i
			break
		}
	}
	require.NotEqual(t, -1, start, "verifyAPIToken not found in auth.go")

	end := -1
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	require.NotEqual(t, -1, end, "closing brace of verifyAPIToken not found")

	// Strip comment lines once: the explanatory comments inside the
	// function legitimately mention "break"/"return".
	var code []string
	for _, l := range lines[start:end] {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			code = append(code, l)
		}
	}

	loopAt := -1
	for i, l := range code {
		if strings.Contains(l, "for hash, t := range hashes") {
			loopAt = i
			break
		}
	}
	require.NotEqual(t, -1, loopAt, "range loop over hashes not found inside verifyAPIToken")

	// Loop body spans from the `for` line until brace depth (opened by the
	// for line itself) returns to zero — i.e. the loop's closing brace.
	depth := 0
	loopEnd := -1
	for i := loopAt; i < len(code); i++ {
		depth += strings.Count(code[i], "{") - strings.Count(code[i], "}")
		if i > loopAt && depth == 0 {
			loopEnd = i
			break
		}
	}
	require.NotEqual(t, -1, loopEnd, "closing brace of the range loop not found")

	loopBody := strings.Join(code[loopAt:loopEnd+1], "\n")
	assert.NotRegexp(t, regexp.MustCompile(`\bbreak\b`), loopBody,
		"verifyAPIToken must not break out of the loop (timing oracle, CWE-208)")
	assert.NotRegexp(t, regexp.MustCompile(`\breturn\b`), loopBody,
		"verifyAPIToken must not return inside the range loop (timing oracle, CWE-208)")
}
