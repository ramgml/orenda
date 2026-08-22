package task

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
