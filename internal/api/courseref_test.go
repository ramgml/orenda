package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ramgml/orenda/internal/domain/course"
)

func TestParseCourseRef(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		wantN int
		want  bool
	}{
		{"C uppercase", "C7", 7, true},
		{"C lowercase", "c7", 7, true},
		{"C1", "C1", 1, true},
		{"C999", "C999", 999, true},
		{"C0 rejected", "C0", 0, false},
		{"C-1 rejected", "C-1", 0, false},
		{"Cempty rejected", "C", 0, false},
		{"empty rejected", "", 0, false},
		{"UUID rejected", "01234567-89ab-cdef-0123-456789abcdef", 0, false},
		{"mixed rejected", "C7a", 0, false},
		{"spaces rejected", "C 7", 0, false},
		{"leading space rejected", " C7", 0, false},
		{"trailing space rejected", "C7 ", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := course.ParseCourseRef(tc.ref)
			assert.Equal(t, tc.want, ok, "ParseCourseRef(%q) ok", tc.ref)
			if ok {
				assert.Equal(t, tc.wantN, n, "ParseCourseRef(%q) number", tc.ref)
			}
		})
	}
}

func TestParseLessonRef(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		wantN int
		want  bool
	}{
		{"L uppercase", "L10", 10, true},
		{"L lowercase", "l10", 10, true},
		{"L1", "L1", 1, true},
		{"L999", "L999", 999, true},
		{"L0 rejected", "L0", 0, false},
		{"L-1 rejected", "L-1", 0, false},
		{"Lempty rejected", "L", 0, false},
		{"empty rejected", "", 0, false},
		{"UUID rejected", "01234567-89ab-cdef-0123-456789abcdef", 0, false},
		{"mixed rejected", "L7a", 0, false},
		{"spaces rejected", "L 7", 0, false},
		{"leading space rejected", " L7", 0, false},
		{"trailing space rejected", "L7 ", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := course.ParseLessonRef(tc.ref)
			assert.Equal(t, tc.want, ok, "ParseLessonRef(%q) ok", tc.ref)
			if ok {
				assert.Equal(t, tc.wantN, n, "ParseLessonRef(%q) number", tc.ref)
			}
		})
	}
}

func TestCourseRefNotFoundError(t *testing.T) {
	err := &course.RefNotFoundError{Ref: "C7"}
	assert.Equal(t, "course C7 not found", err.Error())
	assert.ErrorIs(t, err, course.ErrNotFound)
}

func TestLessonRefNotFoundError(t *testing.T) {
	err := &course.LessonRefNotFoundError{Ref: "L10"}
	assert.Equal(t, "lesson L10 not found", err.Error())
	assert.ErrorIs(t, err, course.ErrNotFound)
}
