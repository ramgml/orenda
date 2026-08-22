package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseProjectRef(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		wantN int
		want  bool
	}{
		{"P uppercase", "P7", 7, true},
		{"P lowercase", "p7", 7, true},
		{"P1", "P1", 1, true},
		{"P999", "P999", 999, true},
		{"P1000000", "P1000000", 1000000, true},
		{"P0 rejected", "P0", 0, false},
		{"P-1 rejected", "P-1", 0, false},
		{"Pempty rejected", "P", 0, false},
		{"empty rejected", "", 0, false},
		{"UUID rejected", "01234567-89ab-cdef-0123-456789abcdef", 0, false},
		{"mixed rejected", "P7a", 0, false},
		{"spaces rejected", "P 7", 0, false},
		{"leading space rejected", " P7", 0, false},
		{"trailing space rejected", "P7 ", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := ParseProjectRef(tc.ref)
			assert.Equal(t, tc.want, ok, "ParseProjectRef(%q) ok", tc.ref)
			if ok {
				assert.Equal(t, tc.wantN, n, "ParseProjectRef(%q) number", tc.ref)
			}
		})
	}
}

func TestProjectRefNotFoundError(t *testing.T) {
	err := &RefNotFoundError{Ref: "P7"}
	assert.Equal(t, "project P7 not found", err.Error())
	assert.ErrorIs(t, err, ErrNotFound)

	err2 := &RefNotFoundError{Ref: "p999"}
	assert.Equal(t, "project p999 not found", err2.Error())
	assert.ErrorIs(t, err2, ErrNotFound)
}
