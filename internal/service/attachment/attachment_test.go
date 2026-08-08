package attachment_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/attachment"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type memHub struct{ n int }

func (m *memHub) Publish(context.Context, ws.Event) { m.n++ }
func (m *memHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupAttach(t *testing.T) (*attachmentsvc.Service, *memHub, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/a.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	uploads := filepath.Join(dir, "uploads")
	require.NoError(t, os.MkdirAll(uploads, 0o755))

	hub := &memHub{}
	svc := attachmentsvc.New(sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
		UploadDir:    uploads,
		MaxSizeBytes: 1 << 20, // 1 MiB
		AllowedMimes: []string{"text/*", "application/pdf"},
	}, hub)
	return svc, hub, uploads
}

func TestAttachmentService_StoreAndList(t *testing.T) {
	svc, hub, uploads := setupAttach(t)

	body := []byte("hello world")
	res, err := svc.StoreFromBytes(
		context.Background(),
		attachment.TargetTask,
		"t-1", "hello.txt", "text/plain",
		attachment.UploaderUser, "u-1",
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	assert.False(t, res.Duplicate)
	assert.NotEmpty(t, res.Attachment.ID)
	assert.Equal(t, int64(len(body)), res.Attachment.Size)
	assert.NotEmpty(t, res.Attachment.Path)
	assert.FileExists(t, res.Attachment.Path)

	// sha256 matches.
	sum := sha256.Sum256(body)
	assert.Equal(t, hex.EncodeToString(sum[:]), res.Attachment.SHA256)

	// Listed.
	list, err := svc.ListByTarget(context.Background(), attachment.TargetTask, "t-1")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Hub fired.
	assert.Greater(t, hub.n, 0)

	// File lives under uploads/YYYY/MM/.
	rel, _ := filepath.Rel(uploads, res.Attachment.Path)
	parts := strings.Split(rel, string(filepath.Separator))
	require.Len(t, parts, 3) // YYYY/MM/{id}-{name}
}

func TestAttachmentService_StoreDuplicate(t *testing.T) {
	svc, _, _ := setupAttach(t)

	body := []byte("dup content")
	first, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t-1",
		"a.txt", "text/plain", attachment.UploaderUser, "u-1",
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	second, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t-2",
		"b.txt", "text/plain", attachment.UploaderUser, "u-1",
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	assert.True(t, second.Duplicate, "second store of same sha256 should dedup")
	assert.Equal(t, first.Attachment.ID, second.Attachment.ID, "dedup returns the original row")
}

func TestAttachmentService_Store_TooLarge(t *testing.T) {
	svc, _, _ := setupAttach(t)

	// 2 MiB body, 1 MiB cap.
	body := bytes.Repeat([]byte("x"), 2<<20)
	_, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t",
		"big.bin", "text/plain", attachment.UploaderUser, "u",
		bytes.NewReader(body),
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, attachmentsvc.ErrTooLarge)
}

func TestAttachmentService_Store_MimeRejected(t *testing.T) {
	svc, _, _ := setupAttach(t)

	_, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t",
		"evil.exe", "application/x-msdownload", attachment.UploaderUser, "u",
		bytes.NewReader([]byte("x")),
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, attachmentsvc.ErrMimeRejected)
}

func TestAttachmentService_Store_InvalidInput(t *testing.T) {
	svc, _, _ := setupAttach(t)

	_, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "",
		"x.txt", "text/plain", attachment.UploaderUser, "u",
		bytes.NewReader([]byte("x")),
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, attachmentsvc.ErrInvalidInput)
}

func TestAttachmentService_Delete(t *testing.T) {
	svc, _, uploads := setupAttach(t)

	res, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t",
		"x.txt", "text/plain", attachment.UploaderUser, "u",
		bytes.NewReader([]byte("x")),
	)
	require.NoError(t, err)
	assert.FileExists(t, res.Attachment.Path)

	require.NoError(t, svc.Delete(context.Background(), res.Attachment.ID))
	_, statErr := os.Stat(res.Attachment.Path)
	assert.True(t, os.IsNotExist(statErr), "file should be removed")
	_ = uploads
}

func TestAttachmentService_MimeAllowed_Wildcard(t *testing.T) {
	// New svc with image/* pattern only.
	dir := t.TempDir()
	db, _ := sqlite.Open(context.Background(), filepath.Join(dir+"/a.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	defer db.Close()
	_ = sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations")
	uploads := filepath.Join(dir, "uploads")
	_ = os.MkdirAll(uploads, 0o755)
	svc := attachmentsvc.New(sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
		UploadDir:    uploads,
		MaxSizeBytes: 1 << 20,
		AllowedMimes: []string{"image/*"},
	}, &memHub{})

	_, err := svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t",
		"img.png", "image/png", attachment.UploaderUser, "u",
		bytes.NewReader([]byte("x")),
	)
	require.NoError(t, err)

	_, err = svc.StoreFromBytes(
		context.Background(), attachment.TargetTask, "t",
		"doc.txt", "text/plain", attachment.UploaderUser, "u",
		bytes.NewReader([]byte("x")),
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, attachmentsvc.ErrMimeRejected)
}
