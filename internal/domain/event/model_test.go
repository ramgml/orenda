package event_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/event"
)

func TestEvent_Validate_Defaults(t *testing.T) {
	start := time.Now()
	e := &event.Event{Title: "Meeting", StartAt: start, EndAt: start.Add(time.Hour)}
	require.NoError(t, e.Validate())
}

func TestEvent_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(e *event.Event)
	}{
		{"missing title", func(e *event.Event) {
			e.Title = ""
			e.StartAt = time.Now()
			e.EndAt = time.Now().Add(time.Hour)
		}},
		{"missing start", func(e *event.Event) {
			e.Title = "x"
			e.EndAt = time.Now()
		}},
		{"end before start", func(e *event.Event) {
			e.Title = "x"
			e.StartAt = time.Now()
			e.EndAt = time.Now().Add(-time.Hour)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &event.Event{}
			tc.mut(e)
			assert.Error(t, e.Validate())
		})
	}
}

func TestEvent_AllDay_NormalisesToDayBoundaries(t *testing.T) {
	now := time.Now()
	e := &event.Event{Title: "x", StartAt: now, EndAt: now.Add(2 * time.Hour), AllDay: true}
	require.NoError(t, e.Validate())
	// Start normalised to 00:00:00
	assert.Equal(t, 0, e.StartAt.Hour())
	// End normalised to 23:59:59
	assert.Equal(t, 23, e.EndAt.Hour())
	assert.Equal(t, 59, e.EndAt.Minute())
}
