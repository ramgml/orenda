package wiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRefNumber(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		wantN int
		want  bool
	}{
		{"W uppercase", "W42", 42, true},
		{"W lowercase", "w42", 42, true},
		{"W1", "W1", 1, true},
		{"W999", "W999", 999, true},
		{"W1000000", "W1000000", 1000000, true},
		{"W0 rejected", "W0", 0, false},
		{"W-1 rejected", "W-1", 0, false},
		{"Wempty rejected", "W", 0, false},
		{"UUID rejected", "01234567-89ab-cdef-0123-456789abcdef", 0, false},
		{"mixed rejected", "W42a", 0, false},
		{"spaces rejected", "W 42", 0, false},
		{"leading space rejected", " W42", 0, false},
		{"trailing space rejected", "W42 ", 0, false},
		{"slug rejected", "my-page", 0, false},
		{"T prefix rejected", "T42", 0, false},
		{"P prefix rejected", "P42", 0, false},
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
	err := &RefNotFoundError{Ref: "W42"}
	assert.Equal(t, "page W42 not found", err.Error())
	assert.ErrorIs(t, err, ErrNotFound)

	err2 := &RefNotFoundError{Ref: "w999"}
	assert.Equal(t, "page w999 not found", err2.Error())
	assert.ErrorIs(t, err2, ErrNotFound)
}

func TestIsWRefFormat(t *testing.T) {
	assert.True(t, IsWRefFormat("W42"))
	assert.True(t, IsWRefFormat("w1"))
	assert.False(t, IsWRefFormat("42"))
	assert.False(t, IsWRefFormat("#42"))
	assert.False(t, IsWRefFormat("my-page"))
	assert.False(t, IsWRefFormat("T42"))
}
