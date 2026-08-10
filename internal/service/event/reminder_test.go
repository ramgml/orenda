package event

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/service/notifier"
)

// fakeRepo is a minimal task.Repository implementation used to drive
// the Reminder's scan logic. We only model the methods the scheduler
// actually calls.
type fakeRepo struct {
	tasks []*task.Task
	err   error
}

func (f *fakeRepo) Create(context.Context, *task.Task) error { return nil }
func (f *fakeRepo) GetByID(_ context.Context, id string) (*task.Task, error) {
	for _, t := range f.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, task.ErrNotFound
}
func (f *fakeRepo) ListByProject(context.Context, task.Filter) ([]*task.Task, error) {
	return nil, nil
}
func (f *fakeRepo) ListInRange(_ context.Context, from, to time.Time, _ string) ([]*task.Task, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*task.Task, 0)
	for _, t := range f.tasks {
		if t.StartAt == nil || t.EndAt == nil {
			continue
		}
		// Reminder asks for tasks whose start_at falls in [from, to]
		// (a half-open band: from <= start < to). Half-open is fine for
		// this use case because the scheduler is a coarse scan.
		if !t.StartAt.Before(from) && t.StartAt.Before(to) {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeRepo) Update(context.Context, *task.Task) error        { return nil }
func (f *fakeRepo) Delete(context.Context, string) error            { return nil }
func (f *fakeRepo) AddSubtask(context.Context, *task.Subtask) error { return nil }
func (f *fakeRepo) ListSubtasks(context.Context, string) ([]*task.Subtask, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateSubtask(context.Context, *task.Subtask) error    { return nil }
func (f *fakeRepo) DeleteSubtask(context.Context, string) error           { return nil }
func (f *fakeRepo) CountByColumn(context.Context, string) (int, error)    { return 0, nil }
func (f *fakeRepo) FirstColumnID(context.Context, string) (string, error) { return "", nil }

// Checklist stubs — the reminder path doesn't exercise them but
// task.Repository now requires them. Returning empty results is fine.
func (f *fakeRepo) AddChecklist(context.Context, string, string) (*task.ChecklistRow, error) {
	return nil, nil
}
func (f *fakeRepo) ListChecklists(context.Context, string) ([]task.ChecklistRow, error) {
	return nil, nil
}
func (f *fakeRepo) DeleteChecklist(context.Context, string) error { return nil }
func (f *fakeRepo) AddChecklistItem(context.Context, string, string) (*task.ChecklistItemRow, error) {
	return nil, nil
}
func (f *fakeRepo) ListChecklistItems(context.Context, string) ([]task.ChecklistItemRow, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateChecklistItem(context.Context, string, *bool, *string) error {
	return nil
}
func (f *fakeRepo) DeleteChecklistItem(context.Context, string) error { return nil }
func (f *fakeRepo) GetSubtask(context.Context, string) (*task.Subtask, error) {
	return nil, task.ErrNotFound
}

// withStart helper — Set StartAt + EndAt on a task for the fake.
func withStart(t *task.Task, start time.Time) *task.Task {
	t.StartAt = &start
	end := start.Add(30 * time.Minute)
	t.EndAt = &end
	return t
}

func TestReminder_FiresForUpcomingTasks(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{tasks: []*task.Task{
		withStart(&task.Task{ID: "t-1", Title: "Stand-up"}, now.Add(45*time.Minute)),
		withStart(&task.Task{ID: "t-2", Title: "Lunch"}, now.Add(2*time.Hour)),
		withStart(&task.Task{ID: "t-3", Title: "1:1"}, now.Add(59*time.Minute)),
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(_ context.Context, e notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			assert.Equal(t, "event.upcoming_1h", e.Type)
			return nil
		},
		NotifyProjectOwner: func(_ context.Context, id string) (string, string, string, error) {
			return "owner-1", "title", "/cal", nil
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 2, atomic.LoadInt32(&fired), "only tasks in the 30-60min band should fire")
}

func TestReminder_OwnerLookupFailureSkipsTask(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{tasks: []*task.Task{
		withStart(&task.Task{ID: "t-1", Title: "x"}, now.Add(40*time.Minute)),
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(context.Context, notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			return nil
		},
		NotifyProjectOwner: func(context.Context, string) (string, string, string, error) {
			return "", "", "", errors.New("owner lookup failed")
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired), "owner-lookup failures should not fire")
}

func TestReminder_NoTasksInWindow(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{tasks: []*task.Task{
		withStart(&task.Task{ID: "t-1", Title: "x"}, now.Add(10*time.Minute)),
		withStart(&task.Task{ID: "t-2", Title: "y"}, now.Add(3*time.Hour)),
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(context.Context, notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			return nil
		},
		NotifyProjectOwner: func(context.Context, string) (string, string, string, error) {
			return "o", "t", "/", nil
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired))
}

func TestReminder_NilDepsNoPanic(t *testing.T) {
	r := &Reminder{
		Repo: &fakeRepo{tasks: []*task.Task{
			withStart(&task.Task{ID: "x"}, time.Now().Add(45*time.Minute)),
		}},
		Log: slog.Default(),
	}
	assert.NotPanics(t, func() { _ = r.RunScanForTest(context.Background()) })
}

func TestReminder_SkipsTasksWithoutStartAt(t *testing.T) {
	// Tasks without a StartAt should be ignored — they're not calendar
	// events. The fake's ListInRange already filters these out, but we
	// double-check via the scan loop's nil-guard.
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	t1 := &task.Task{ID: "t-1", Title: "no time"} // StartAt nil
	_ = withStart                                 // ensure helper is referenced
	repo := &fakeRepo{tasks: []*task.Task{t1}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(context.Context, notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			return nil
		},
		NotifyProjectOwner: func(context.Context, string) (string, string, string, error) {
			return "o", "t", "/", nil
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired))
}

// Compile-time check that fakeRepo satisfies task.Repository.
var _ task.Repository = (*fakeRepo)(nil)
