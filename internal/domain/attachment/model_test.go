package attachment_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/attachment"
)

func TestAttachment_Validate_Defaults(t *testing.T) {
	a := &attachment.Attachment{
		TargetID:     "t-1",
		Filename:     "report.pdf",
		Size:         1024,
		Path:         "/data/uploads/x.pdf",
		SHA256:       strings.Repeat("a", 64),
		UploadedByID: "u-1",
	}
	require.NoError(t, a.Validate())
	assert.Equal(t, attachment.TargetTask, a.TargetType)
	assert.Equal(t, attachment.UploaderUser, a.UploadedByType)
}

func TestAttachment_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(a *attachment.Attachment)
	}{
		{"missing target", func(a *attachment.Attachment) { a.TargetID = "" }},
		{"missing filename", func(a *attachment.Attachment) { a.Filename = "" }},
		{"missing path", func(a *attachment.Attachment) { a.Path = "" }},
		{"zero size", func(a *attachment.Attachment) { a.Size = 0 }},
		{"bad sha256", func(a *attachment.Attachment) { a.SHA256 = "short" }},
		{"missing uploader", func(a *attachment.Attachment) { a.UploadedByID = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &attachment.Attachment{
				TargetID:     "t",
				Filename:     "x",
				Path:         "y",
				Size:         1,
				SHA256:       strings.Repeat("a", 64),
				UploadedByID: "u",
			}
			tc.mut(a)
			assert.Error(t, a.Validate())
		})
	}
}
