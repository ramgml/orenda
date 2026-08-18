package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coursedomain "github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// stubActivityRepo captures rows in-memory; sufficient for verifying
// that the recorder calls Create with the right kind and that the
// recorder resolves actor correctly via IdentitySource.
type stubActivityRepo struct {
	rows []*coursedomain.Activity
}

func (s *stubActivityRepo) Create(_ context.Context, a *coursedomain.Activity) error {
	s.rows = append(s.rows, a)
	return nil
}

func TestService_ActivityRecorder_Wired(t *testing.T) {
	// Recorder integration: row with kind=approved, actor from
	// IdentitySource. Service-level ApproveCurriculum integration is
	// covered separately by the handler-level tests; here we verify
	// the recorder's contract.
	stub := &stubActivityRepo{}
	rec := coursesvc.NewCourseActivityRecorder(stub)
	rec.IdentitySource = func(_ context.Context) (coursedomain.ActorType, string, bool) {
		return coursedomain.ActorUser, "u-test", true
	}

	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-x", coursedomain.ActivityApproved, ""))
	require.Len(t, stub.rows, 1)
	assert.Equal(t, "c-x", stub.rows[0].CourseID)
	assert.Equal(t, coursedomain.ActivityApproved, stub.rows[0].Kind)
	assert.Equal(t, coursedomain.ActorUser, stub.rows[0].ActorType)
	assert.Equal(t, "u-test", stub.rows[0].ActorID)
}

func TestService_ActivityRecorder_NoIdentity_SilentlySkipped(t *testing.T) {
	stub := &stubActivityRepo{}
	rec := coursesvc.NewCourseActivityRecorder(stub)
	// IdentitySource stays nil — should not panic, should not write.
	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-x", coursedomain.ActivityApproved, ""))
	assert.Empty(t, stub.rows)
}

func TestService_ActivityRecorder_GenerateID(t *testing.T) {
	stub := &stubActivityRepo{}
	rec := coursesvc.NewCourseActivityRecorder(stub)
	rec.IdentitySource = func(_ context.Context) (coursedomain.ActorType, string, bool) {
		return coursedomain.ActorAgent, "a-1", true
	}
	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-1", coursedomain.ActivityActivated, ""))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-1", coursedomain.ActivityActivated, ""))
	require.Len(t, stub.rows, 2)
	assert.NotEqual(t, stub.rows[0].ID, stub.rows[1].ID)
	assert.Len(t, stub.rows[0].ID, 36) // UUIDv7: 8-4-4-4-12
}

func TestService_ActivityRecorder_KindFromAgentActivate(t *testing.T) {
	// The split between Approved and Activated kinds is the whole
	// point of Phase 32.5 pilot task #2: the audit feed shows which
	// path the operator took. Verify both kinds reach the repo.
	stub := &stubActivityRepo{}
	rec := coursesvc.NewCourseActivityRecorder(stub)
	rec.IdentitySource = func(_ context.Context) (coursedomain.ActorType, string, bool) {
		return coursedomain.ActorAgent, "a-tutor", true
	}
	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-x", coursedomain.ActivityApproved, ""))
	require.NoError(t, rec.RecordCourseAuto(context.Background(), "c-x", coursedomain.ActivityActivated, ""))
	require.Len(t, stub.rows, 2)
	// Both recorded; recorder doesn't choose the kind — the caller
	// (ApproveCurriculum vs ActivateCourse in courseSvc) does.
	kinds := []coursedomain.ActivityKind{stub.rows[0].Kind, stub.rows[1].Kind}
	assert.Contains(t, kinds, coursedomain.ActivityApproved)
	assert.Contains(t, kinds, coursedomain.ActivityActivated)
}
