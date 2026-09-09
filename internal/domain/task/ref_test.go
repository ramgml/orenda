package task

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRefNumber(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		wantN int
		want  bool
	}{
		{"T uppercase", "T42", 42, true},
		{"T lowercase", "t42", 42, true},
		{"T1", "T1", 1, true},
		{"T999", "T999", 999, true},
		{"T1000000", "T1000000", 1000000, true},
		{"T0 rejected", "T0", 0, false},
		{"T-1 rejected", "T-1", 0, false},
		{"Tempty rejected", "T", 0, false},
		{"legacy hash rejected", "#42", 0, false},
		{"legacy bare rejected", "42", 0, false},
		{"empty rejected", "", 0, false},
		{"UUID rejected", "01234567-89ab-cdef-0123-456789abcdef", 0, false},
		{"mixed rejected", "T42a", 0, false},
		{"spaces rejected", "T 42", 0, false},
		{"leading space rejected", " T42", 0, false},
		{"trailing space rejected", "T42 ", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := ParseRefNumber(tc.ref)
			assert.Equal(t, tc.want, ok, "ParseRefNumber(%q) ok", tc.ref)
			if ok {
				assert.Equal(t, tc.wantN, n, "ParseRefNumber(%q) number", tc.ref)
			}
		})
	}
}

func TestRefNotFoundError(t *testing.T) {
	err := &RefNotFoundError{Ref: "T42"}
	assert.Equal(t, "task T42 not found", err.Error())
	assert.ErrorIs(t, err, ErrNotFound)

	err2 := &RefNotFoundError{Ref: "t999"}
	assert.Equal(t, "task t999 not found", err2.Error())
	assert.ErrorIs(t, err2, ErrNotFound)
}

func TestLegacyRefNotFoundError(t *testing.T) {
	err := &LegacyRefNotFoundError{Ref: "#42"}
	assert.Equal(t,
		`task #42 not found; use "T42" — task refs are T-prefixed since Task 48`,
		err.Error())
	assert.ErrorIs(t, err, ErrNotFound)

	err2 := &LegacyRefNotFoundError{Ref: "42"}
	assert.Contains(t, err2.Error(), `use "T42"`)
	assert.ErrorIs(t, err2, ErrNotFound)
}

// stubRepo answers "not found" for every lookup so ResolveRef's
// error shaping can be pinned without a database.
type stubRepo struct{ Repository }

func (stubRepo) GetByID(context.Context, string) (*Task, error) {
	return nil, ErrNotFound
}

func (stubRepo) GetByNumber(context.Context, int) (*Task, error) {
	return nil, ErrNotFound
}

// TestResolveRef_LegacyHint: legacy-shaped refs get the migration
// hint, unknown T-refs keep the ref-naming error, everything else
// keeps the bare sentinel.
func TestResolveRef_LegacyHint(t *testing.T) {
	ctx := context.Background()
	repo := stubRepo{}

	for _, ref := range []string{"#42", "42"} {
		_, err := ResolveRef(ctx, repo, ref)
		require.Error(t, err, ref)
		var legacy *LegacyRefNotFoundError
		require.ErrorAs(t, err, &legacy, ref)
		assert.Contains(t, err.Error(), `use "T42"`, ref)
		assert.ErrorIs(t, err, ErrNotFound, ref)
	}

	_, err := ResolveRef(ctx, repo, "T42")
	require.Error(t, err)
	var refErr *RefNotFoundError
	require.ErrorAs(t, err, &refErr)

	_, err = ResolveRef(ctx, repo, "01234567-89ab-cdef-0123-456789abcdef")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	var legacy *LegacyRefNotFoundError
	assert.NotErrorAs(t, err, &legacy)
}
