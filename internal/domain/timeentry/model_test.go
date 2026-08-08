package timeentry_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/timeentry"
)

func TestTimeEntry_Validate_Defaults(t *testing.T) {
	e := &timeentry.TimeEntry{
		TaskID:    "t-1",
		AgentID:   "a-1",
		StartedAt: time.Now(),
	}
	require.NoError(t, e.Validate())
	assert.Equal(t, timeentry.SourceTimer, e.Source)
}

func TestTimeEntry_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(e *timeentry.TimeEntry)
	}{
		{"missing task", func(e *timeentry.TimeEntry) {
			e.AgentID = "a"
			e.StartedAt = time.Now()
		}},
		{"missing agent", func(e *timeentry.TimeEntry) {
			e.TaskID = "t"
			e.StartedAt = time.Now()
		}},
		{"missing started_at", func(e *timeentry.TimeEntry) {
			e.TaskID = "t"
			e.AgentID = "a"
		}},
		{"end before start", func(e *timeentry.TimeEntry) {
			e.TaskID = "t"
			e.AgentID = "a"
			e.StartedAt = time.Now()
			e.EndedAt = ptr(time.Now().Add(-time.Hour))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &timeentry.TimeEntry{}
			tc.mut(e)
			assert.Error(t, e.Validate())
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }
