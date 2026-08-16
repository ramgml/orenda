package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/attachment"
)

func TestAttachmentRepo_CreateAndListByTarget(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewAttachmentRepository(db)

	a := &attachment.Attachment{
		TargetID:     "t-1",
		Filename:     "report.pdf",
		Mime:         "application/pdf",
		Size:         1024,
		Path:         "/data/uploads/x.pdf",
		SHA256:       strings.Repeat("a", 64),
		UploadedByID: "u-1",
	}
	require.NoError(t, repo.Create(context.Background(), a))
	assert.NotEmpty(t, a.ID)

	got, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, "report.pdf", got.Filename)

	list, err := repo.ListByTarget(context.Background(), attachment.TargetTask, "t-1")
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestAttachmentRepo_FindBySHA256(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewAttachmentRepository(db)

	sha := strings.Repeat("b", 64)
	a := &attachment.Attachment{
		TargetID: "t-1", Filename: "x.txt", Mime: "text/plain", Size: 10,
		Path: "/p", SHA256: sha, UploadedByID: "u",
	}
	require.NoError(t, repo.Create(context.Background(), a))

	found, err := repo.FindBySHA256(context.Background(), sha)
	require.NoError(t, err)
	assert.Equal(t, a.ID, found.ID)

	_, err = repo.FindBySHA256(context.Background(), strings.Repeat("z", 64))
	require.Error(t, err)
}

func TestAttachmentRepo_Delete(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewAttachmentRepository(db)
	a := &attachment.Attachment{
		TargetID: "t", Filename: "x", Mime: "x", Size: 1,
		Path: "/p", SHA256: strings.Repeat("c", 64), UploadedByID: "u",
	}
	require.NoError(t, repo.Create(context.Background(), a))
	require.NoError(t, repo.Delete(context.Background(), a.ID))
	_, err := repo.GetByID(context.Background(), a.ID)
	assert.ErrorIs(t, err, attachment.ErrNotFound)

	err = repo.Delete(context.Background(), "no-such")
	assert.ErrorIs(t, err, attachment.ErrNotFound)
}

func TestAttachmentRepo_ValidateError(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewAttachmentRepository(db)

	// Bad SHA256.
	err := repo.Create(context.Background(), &attachment.Attachment{
		TargetID: "t", Filename: "x", Path: "/p", Size: 1,
		SHA256: "short", UploadedByID: "u",
	})
	require.Error(t, err)
}
