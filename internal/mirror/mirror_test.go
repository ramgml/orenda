package mirror_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/wiki"
	"github.com/ramgml/orenda/internal/mirror"
)

func TestMirror_WriteTask(t *testing.T) {
	dir := t.TempDir()
	svc := mirror.New(dir)

	tr := &task.Task{
		ID:          "t-1",
		Title:       "Write tests",
		Description: "Cover the mirror with tests.",
		Status:      task.StatusInProgress,
		Priority:    task.PriorityHigh,
		UpdatedAt:   time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
	}
	checklists := []task.Checklist{
		{ID: "cl-1", TaskID: "t-1", Title: "Pre-flight"},
	}
	itemsByList := map[string][]task.ChecklistItem{
		"cl-1": {
			{ID: "i-1", ChecklistID: "cl-1", Title: "Set up", Done: true},
			{ID: "i-2", ChecklistID: "cl-1", Title: "Run them", Done: false},
		},
	}
	comments := []*comment.Comment{
		{
			ID: "c1", TargetID: "t-1", AuthorType: "user", AuthorID: "u-1",
			BodyMD: "Looks good.", CreatedAt: time.Date(2026, 8, 8, 12, 30, 0, 0, time.UTC),
		},
	}

	path, err := svc.WriteTask(tr, checklists, itemsByList, comments, nil)
	require.NoError(t, err)
	assert.FileExists(t, path)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(body)

	assert.Contains(t, text, `id: "t-1"`)
	assert.Contains(t, text, `status: "in_progress"`)
	assert.Contains(t, text, `# Write tests`)
	assert.Contains(t, text, "Cover the mirror with tests.")
	assert.Contains(t, text, "## Checklists")
	assert.Contains(t, text, "### Pre-flight")
	assert.Contains(t, text, "- [x] Set up")
	assert.Contains(t, text, "- [ ] Run them")
	assert.Contains(t, text, "Looks good.")
}

func TestMirror_WritePage(t *testing.T) {
	dir := t.TempDir()
	svc := mirror.New(dir)

	p := &wiki.Page{
		ID:        "p-1",
		Slug:      "architecture",
		Title:     "Architecture",
		ContentMD: "See [[db-schema]].",
		UpdatedAt: time.Now(),
	}
	path, err := svc.WritePage(p)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), `slug: "architecture"`)
	assert.Contains(t, string(body), "# Architecture")
	assert.Contains(t, string(body), "See [[db-schema]].")
}

func TestMirror_DeleteTask_NoErrorOnMissing(t *testing.T) {
	dir := t.TempDir()
	svc := mirror.New(dir)
	// Missing file → nil error.
	assert.NoError(t, svc.DeleteTask("no-such"))
}

func TestMirror_WriteTask_FilenameIsID(t *testing.T) {
	dir := t.TempDir()
	svc := mirror.New(dir)
	path, err := svc.WriteTask(&task.Task{
		ID: "abc", Title: "x",
	}, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, filepath.Join("tasks", "abc.md")))
}

// Phase 13: tags + colour land in the frontmatter so a plain git
// diff of the mirror dir tells the label story.
func TestMirror_WriteTask_TagsAndColor(t *testing.T) {
	dir := t.TempDir()
	svc := mirror.New(dir)
	path, err := svc.WriteTask(
		&task.Task{
			ID:    "t-coloured",
			Title: "Polish the picker",
			Color: "#0ea5e9",
		},
		nil, nil, nil,
		[]task.Tag{
			{ID: "tg-1", Name: "frontend", Color: "#22c55e"},
			{ID: "tg-2", Name: "ux"},
		},
	)
	require.NoError(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(body)
	assert.Contains(t, text, `color: "#0ea5e9"`)
	assert.Contains(t, text, `tags: ["frontend", "ux"]`)
}

// Task 193: with backup.enabled=false the wiring used to hand the
// task/wiki services a TYPED-nil *mirror.Service — assigning a typed
// nil into the MirrorWriter / PageMirror interface fields yields a
// non-nil interface value, so the callers' `if s.Mirror == nil`
// guards passed and the call paniced on the nil receiver inside
// writeFile (→ 500 on task create/PATCH). The mirror package is the
// last line of defence: every entry point must tolerate a nil
// receiver. (The wiring now always constructs a real Service; this
// test pins the panic-safety contract itself.)
func TestMirror_TypedNilReceiverIsPanicFree(t *testing.T) {
	var svc *mirror.Service = nil // the exact value the old wiring produced

	tests := []struct {
		name string
		call func()
	}{
		{
			name: "WriteTask",
			call: func() { _, _ = svc.WriteTask(&task.Task{ID: "t", Title: "x"}, nil, nil, nil, nil) },
		},
		{
			name: "WritePage",
			call: func() { _, _ = svc.WritePage(&wiki.Page{ID: "p", Slug: "s"}) },
		},
		{
			name: "DeleteTask",
			call: func() { _ = svc.DeleteTask("t") },
		},
		{
			name: "DeletePage",
			call: func() { _ = svc.DeletePage("s") },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, tc.call)
		})
	}
}
