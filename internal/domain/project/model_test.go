package project_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

func TestProject_Validate(t *testing.T) {
	t.Run("valid with defaults", func(t *testing.T) {
		p := &project.Project{Name: "Orenda", OwnerID: "u-1"}
		require.NoError(t, p.Validate())
		assert.Equal(t, "#3b82f6", p.Color)
	})

	t.Run("missing name", func(t *testing.T) {
		p := &project.Project{OwnerID: "u-1"}
		require.Error(t, p.Validate())
	})

	t.Run("missing owner", func(t *testing.T) {
		p := &project.Project{Name: "Orenda"}
		require.Error(t, p.Validate())
	})
}

func TestDefaultColumns(t *testing.T) {
	require.NotEmpty(t, project.DefaultColumns)
	assert.Equal(t, "backlog", project.DefaultColumns[0])
	assert.Equal(t, "rejected", project.DefaultColumns[len(project.DefaultColumns)-1],
		"T376: the rejected parking column appends after done — no existing position shifts")
}

// T376 (PRD F-T-3): the default board must carry a column for every
// canonical status except the auto-only `blocked`, in AllStatuses
// order — a status without a column would break the 27.8 invariant
// `task.status ≡ column.status` on a fresh project (syncColumnToStatus
// would find nothing to move the card to).
func TestDefaultColumns_CoverCanonicalStatuses(t *testing.T) {
	var want []string
	for _, s := range task.AllStatuses {
		if s == task.StatusBlocked {
			continue // auto-only: no default column carries it
		}
		want = append(want, string(s))
	}
	assert.Equal(t, want, project.DefaultColumns)
}
