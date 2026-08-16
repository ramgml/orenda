package comment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/comment"
)

func TestComment_Validate_Defaults(t *testing.T) {
	c := &comment.Comment{
		TargetID: "t-1",
		AuthorID: "u-1",
		BodyMD:   "hello",
	}
	require.NoError(t, c.Validate())
	assert.Equal(t, comment.TargetTask, c.TargetType)
	assert.Equal(t, comment.AuthorUser, c.AuthorType)
}

func TestComment_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(c *comment.Comment)
	}{
		{"missing target", func(c *comment.Comment) { c.TargetID = ""; c.AuthorID = "u"; c.BodyMD = "x" }},
		{"missing author", func(c *comment.Comment) { c.TargetID = "t"; c.AuthorID = ""; c.BodyMD = "x" }},
		{"missing body", func(c *comment.Comment) { c.TargetID = "t"; c.AuthorID = "u"; c.BodyMD = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &comment.Comment{}
			tc.mut(c)
			assert.Error(t, c.Validate())
		})
	}
}
