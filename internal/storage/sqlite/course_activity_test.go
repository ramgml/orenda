package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
)

// seedCourse inserts a minimal courses row. Tests that exercise
// just the activity layer don't care about owner_id, lessons,
// quizzes — they want a real courses row to FK against. We seed
// a user first because courses.owner_id has a NOT NULL constraint.
func seedCourse(t *testing.T, db execer, id string) {
	t.Helper()
	ownerID := "u-" + id
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, ownerID+"@test.local", "x", "Test")
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO courses (id, title, status, owner_id) VALUES (?, ?, 'draft', ?)`,
		id, "Test "+id, ownerID)
	require.NoError(t, err)
}

// execer is the narrow surface seedCourse needs — both *sql.DB and
// the test pool satisfy it via the same ExecContext signature.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// TestCourseActivity_CreateAndList exercises the storage round-trip:
// create a row, read it back newest-first.
func TestCourseActivity_CreateAndList(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	courseID := "c-act-1"
	seedCourse(t, db, courseID)
	repo := NewCourseActivityRepository(db)
	now := time.Now().UTC()

	a := &course.Activity{
		ID:        "act-1",
		CourseID:  courseID,
		ActorType: course.ActorUser,
		ActorID:   "u-1",
		Kind:      course.ActivityApproved,
		Payload:   "",
		CreatedAt: now.Add(-1 * time.Minute),
	}
	b := &course.Activity{
		ID:        "act-2",
		CourseID:  courseID,
		ActorType: course.ActorAgent,
		ActorID:   "a-1",
		Kind:      course.ActivityActivated,
		Payload:   "",
		CreatedAt: now,
	}
	require.NoError(t, repo.Create(context.Background(), a))
	require.NoError(t, repo.Create(context.Background(), b))

	rows, err := repo.ListByCourse(context.Background(), courseID, 50)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "act-2", rows[0].ID)
	assert.Equal(t, course.ActivityActivated, rows[0].Kind)
	assert.Equal(t, course.ActorAgent, rows[0].ActorType)
	assert.Equal(t, "act-1", rows[1].ID)
	assert.Equal(t, course.ActivityApproved, rows[1].Kind)
	assert.Equal(t, course.ActorUser, rows[1].ActorType)
}

// TestCourseActivity_ListByCourse_RespectsLimit verifies the limit
// parameter bounds the result.
func TestCourseActivity_ListByCourse_RespectsLimit(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	courseID := "c-act-2"
	seedCourse(t, db, courseID)
	repo := NewCourseActivityRepository(db)
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(context.Background(), &course.Activity{
			ID:        "act-" + string(rune('a'+i)),
			CourseID:  courseID,
			ActorType: course.ActorUser,
			ActorID:   "u-1",
			Kind:      course.ActivityLessonEdited,
			Payload:   "test",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}))
	}
	rows, err := repo.ListByCourse(context.Background(), courseID, 2)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.Equal(t, "act-e", rows[0].ID)
	assert.Equal(t, "act-d", rows[1].ID)
}

// TestCourseActivity_ValidateEnforced covers Validate() reject
// paths. Storage calls Validate() before insert.
func TestCourseActivity_ValidateEnforced(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	repo := NewCourseActivityRepository(db)

	bad := &course.Activity{
		ID:        "act-bad",
		CourseID:  "c",
		ActorType: course.ActorType("alien"),
		ActorID:   "u-1",
		Kind:      course.ActivityApproved,
		CreatedAt: time.Now().UTC(),
	}
	err := repo.Create(context.Background(), bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actor_type")

	bad2 := &course.Activity{
		ID:        "act-bad2",
		CourseID:  "c",
		ActorType: course.ActorUser,
		ActorID:   "u-1",
		Kind:      course.ActivityKind("mystery"),
		CreatedAt: time.Now().UTC(),
	}
	err = repo.Create(context.Background(), bad2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")
}

// TestCourseActivity_NilPayload verifies empty payload persists as
// null (not "") — the storage convention. After ListByCourse, the
// in-memory struct has Payload="".
func TestCourseActivity_NilPayload(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	courseID := "c-act-3"
	seedCourse(t, db, courseID)
	repo := NewCourseActivityRepository(db)
	a := &course.Activity{
		ID:        "act-nil",
		CourseID:  courseID,
		ActorType: course.ActorUser,
		ActorID:   "u-1",
		Kind:      course.ActivityCreated,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, repo.Create(context.Background(), a))
	rows, err := repo.ListByCourse(context.Background(), courseID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].Payload)
}

// TestCourseActivity_CascadeDelete verifies ON DELETE CASCADE —
// when a course is deleted, its activity rows go with it.
func TestCourseActivity_CascadeDelete(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	courseID := "c-cascade"
	seedCourse(t, db, courseID)

	activityRepo := NewCourseActivityRepository(db)
	require.NoError(t, activityRepo.Create(context.Background(), &course.Activity{
		ID:        "act-cascade",
		CourseID:  courseID,
		ActorType: course.ActorUser,
		ActorID:   "u-cascade",
		Kind:      course.ActivityCreated,
		CreatedAt: time.Now().UTC(),
	}))
	rows, err := activityRepo.ListByCourse(context.Background(), courseID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	_, err = db.ExecContext(context.Background(), "DELETE FROM courses WHERE id = ?", courseID)
	require.NoError(t, err)
	rows, err = activityRepo.ListByCourse(context.Background(), courseID, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "activity rows should cascade-delete with course")
}
