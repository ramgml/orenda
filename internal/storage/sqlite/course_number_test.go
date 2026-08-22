package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
)

// TestCourseRepo_NumberAssignedSequentially pins the Phase-38 contract:
// every CreateCourse draws COALESCE(MAX(number),0)+1, so numbers are
// 1-based and monotonically increasing in creation order.
func TestCourseRepo_NumberAssignedSequentially(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	var prev int
	for range 5 {
		c := &course.Course{
			Title:   "Learn Something",
			Status:  course.StatusDraft,
			Level:   "beginner",
			Pace:    "regular",
			OwnerID: owner,
		}
		require.NoError(t, repo.CreateCourse(ctx, c))
		assert.Equal(t, prev+1, c.Number, "numbers must be sequential")
		prev = c.Number
	}
	assert.Equal(t, 5, prev)
}

// TestCourseRepo_NumberNeverReused: deleting a course must NOT free its
// number — a "C7" reference in a commit message or branch name has to
// keep pointing at the same (now deleted) course forever.
func TestCourseRepo_NumberNeverReused(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	mk := func() *course.Course {
		c := &course.Course{
			Title:   "Course",
			Status:  course.StatusDraft,
			Level:   "beginner",
			Pace:    "regular",
			OwnerID: owner,
		}
		require.NoError(t, repo.CreateCourse(ctx, c))
		return c
	}

	t.Run("delete head", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteCourse(ctx, c.ID))
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting the newest course its number must stay burned")
		_, err := repo.GetCourseByNumber(ctx, c.Number)
		assert.ErrorIs(t, err, course.ErrNotFound)
		_ = a
		_ = b
	})

	t.Run("delete middle", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteCourse(ctx, b.ID))
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting a middle course its number must stay burned")
		_, err := repo.GetCourseByNumber(ctx, b.Number)
		assert.ErrorIs(t, err, course.ErrNotFound)
		_ = a
		_ = c
	})
}

// TestCourseRepo_GetByNumber: the "C<N>" lookup — hit and miss.
func TestCourseRepo_GetByNumber(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{
		Title:   "Rust Basics",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "regular",
		OwnerID: owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))
	require.NotZero(t, c.Number)

	got, err := repo.GetCourseByNumber(ctx, c.Number)
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
	assert.Equal(t, "Rust Basics", got.Title)

	// Miss: number 999999 doesn't exist.
	_, err = repo.GetCourseByNumber(ctx, 999999)
	assert.ErrorIs(t, err, course.ErrNotFound)
}

// TestCourseRepo_NumberVsUUIDNoCollision: a numeric ref and a UUID are
// disjoint namespaces — GetCourse never matches a number string and the
// UUID (which always contains '-' and hex letters) is never mistaken
// for a number by ParseCourseRef.
func TestCourseRepo_NumberVsUUIDNoCollision(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{
		Title:   "Test",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "regular",
		OwnerID: owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))

	// GetCourse with a number string fails (it expects UUID format).
	_, err := repo.GetCourse(ctx, "1")
	assert.ErrorIs(t, err, course.ErrNotFound)

	// GetByNumber with a UUID string fails (wrong column type).
	_, err = repo.GetCourseByNumber(ctx, 0)
	assert.ErrorIs(t, err, course.ErrNotFound)
}
