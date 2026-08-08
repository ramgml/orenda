package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/event"
)

func TestEventRepo_CRUD(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewEventRepository(db)

	start := time.Now().Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	e := &event.Event{
		Title:   "Sprint review",
		StartAt: start,
		EndAt:   end,
		Color:   "#3b82f6",
	}
	got, err := repo.Create(context.Background(), e)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.False(t, got.CreatedAt.IsZero())

	// GetByID
	fetched, err := repo.GetByID(context.Background(), got.ID)
	require.NoError(t, err)
	assert.Equal(t, "Sprint review", fetched.Title)

	// Update
	fetched.Title = "Sprint retro"
	require.NoError(t, repo.Update(context.Background(), fetched))

	again, err := repo.GetByID(context.Background(), got.ID)
	require.NoError(t, err)
	assert.Equal(t, "Sprint retro", again.Title)

	// ListInRange
	start2 := start.Add(-time.Hour)
	end2 := start.Add(3 * time.Hour)
	list, err := repo.ListInRange(context.Background(), start2, end2, "")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Outside range
	list, err = repo.ListInRange(context.Background(), start.Add(24*time.Hour), end.Add(24*time.Hour), "")
	require.NoError(t, err)
	assert.Empty(t, list)

	// Delete
	require.NoError(t, repo.Delete(context.Background(), got.ID))
	_, err = repo.GetByID(context.Background(), got.ID)
	assert.ErrorIs(t, err, event.ErrNotFound)
}

func TestEventRepo_AllDay(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewEventRepository(db)

	now := time.Now()
	e := &event.Event{
		Title:   "All day",
		StartAt: now,
		EndAt:   now.Add(8 * time.Hour),
		AllDay:  true,
	}
	got, err := repo.Create(context.Background(), e)
	require.NoError(t, err)
	assert.True(t, got.AllDay)
}

func TestEventRepo_UpdateNotFound(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewEventRepository(db)
	err := repo.Update(context.Background(), &event.Event{
		ID: "no-such", Title: "x", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour),
	})
	assert.ErrorIs(t, err, event.ErrNotFound)
}
