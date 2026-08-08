package project_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
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
	assert.Equal(t, "done", project.DefaultColumns[len(project.DefaultColumns)-1])
}
