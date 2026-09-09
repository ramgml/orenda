package task_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/mirror"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Task 193: when backup.enabled=false the serve wiring used to assign a
// TYPED-nil *mirror.Service into Service.Mirror (a MirrorWriter
// interface). A typed nil wrapped in an interface compares non-nil, so
// the `if s.Mirror == nil` guards inside the service passed and
// MirrorSave dereferenced the nil *mirror.Service via
// mirror.(*Service).writeFile — a panic the HTTP layer surfaced as a
// 500 on task create/PATCH. These tests pin the nil-mirror path end to
// end: with the exact typed-nil value the old wiring produced, the
// service entry points must be silent no-ops, not panics.
func TestMirrorSave_TypedNilMirrorIsNoOp(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Mirror = (*mirror.Service)(nil) // the exact value the old wiring produced

	require.NotEmpty(t, cols)
	backlog := cols[0]
	tr := &task.Task{ProjectID: p.ID, ColumnID: backlog.ID, Title: "nil-mirror"}
	require.NoError(t, repo.Create(context.Background(), tr))

	assert.NotPanics(t, func() {
		svc.MirrorSave(context.Background(), tr)
	}, "MirrorSave with a typed-nil Mirror must be a no-op, not a panic")

	assert.NotPanics(t, func() {
		svc.MirrorDelete(tr.ID)
	}, "MirrorDelete with a typed-nil Mirror must be a no-op, not a panic")

	// The DB write itself must be unaffected by the nil mirror: a
	// Move after the failed-era create still succeeds.
	moved, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: cols[len(cols)-1].ID,
	})
	require.NoError(t, err)
	assert.Equal(t, cols[len(cols)-1].ID, moved.ColumnID)
}

// Unconditional construction (the fix) is also pinned: a real mirror
// service pointed at a temp dir lets MirrorSave actually write the
// projection file, proving the nil path above is about the typed-nil
// value, not about MirrorSave refusing to work.
func TestMirrorSave_RealMirrorWritesProjection(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)

	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
	mirrorDir := t.TempDir()
	svc.Mirror = mirror.New(mirrorDir)

	require.NotEmpty(t, cols)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "real mirror"}
	require.NoError(t, repo.Create(context.Background(), tr))

	svc.MirrorSave(context.Background(), tr)

	assert.DirExists(t, mirrorDir+"/tasks")
	entries, err := osReadDir(mirrorDir + "/tasks")
	require.NoError(t, err)
	require.Len(t, entries, 1, "exactly one mirrored task file")
	assert.Equal(t, tr.ID+".md", entries[0])
}

// The wiki side shares the same typed-nil hazard via PageMirror; the
// wiring fix covers both. This test keeps the project/service shape
// honest: MirrorSave must also survive a nil *task.Task argument.
func TestMirrorSave_NilTaskArgIsNoOp(t *testing.T) {
	db := setupMoveDB(t)
	_, _ = setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Mirror = mirror.New(t.TempDir())

	assert.NotPanics(t, func() {
		svc.MirrorSave(context.Background(), nil)
	})
}

// osReadDir lists a directory's file names, sorted (the mirror
// filename contract is `tasks/<id>.md`).
func osReadDir(dir string) ([]string, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dirents))
	for _, de := range dirents {
		names = append(names, de.Name())
	}
	return names, nil
}
