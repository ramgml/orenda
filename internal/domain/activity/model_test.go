package activity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
)

func TestActivity_Validate_Defaults(t *testing.T) {
	a := &activity.Activity{
		TaskID:  "t-1",
		ActorID: "u-1",
		Action:  activity.ActionCreated,
	}
	require.NoError(t, a.Validate())
	assert.Equal(t, activity.ActorUser, a.ActorType)
}

func TestActivity_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(a *activity.Activity)
	}{
		{"missing task", func(a *activity.Activity) { a.TaskID = "" }},
		{"missing actor", func(a *activity.Activity) { a.ActorID = "" }},
		{"missing action", func(a *activity.Activity) { a.Action = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &activity.Activity{TaskID: "t", ActorID: "u", Action: activity.ActionCreated}
			tc.mut(a)
			assert.Error(t, a.Validate())
		})
	}
}
