package comment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/comment"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// fakeNotifier records events the comment service fans out. Mirrors
// the real `*notifier.Service.Notify` contract: returns the same
// error so the caller can react.
type fakeNotifier struct {
	events []notifierservice.Event
	err    error
}

func (f *fakeNotifier) Notify(ctx context.Context, e notifierservice.Event) error {
	f.events = append(f.events, e)
	return f.err
}

// fakeTaskOwner returns a canned (user_id, title) pair so the
// `task.commented` test doesn't need a real tasks table.
type fakeTaskOwner struct {
	userID, title string
	err           error
}

func (f fakeTaskOwner) OwnerForTask(ctx context.Context, taskID string) (string, string, error) {
	return f.userID, f.title, f.err
}

// stubRepo is a minimal in-memory Repository so the test doesn't
// need a real DB. Only Create and ListByTarget are exercised by
// Add; the rest is here to satisfy the interface.
type stubRepo struct {
	create   func(context.Context, *comment.Comment) (*comment.Comment, error)
	getByID  func(context.Context, string) (*comment.Comment, error)
	list     func(context.Context, comment.TargetType, string) ([]*comment.Comment, error)
	mentions func(context.Context, string) ([]*comment.Mention, error)
	update   func(context.Context, string, string) (*comment.Comment, error)
}

func (s stubRepo) Create(ctx context.Context, c *comment.Comment) (*comment.Comment, error) {
	if s.create != nil {
		return s.create(ctx, c)
	}
	return c, nil
}
func (s stubRepo) GetByID(ctx context.Context, id string) (*comment.Comment, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, comment.ErrNotFound
}
func (s stubRepo) ListByTarget(ctx context.Context, t comment.TargetType, id string) ([]*comment.Comment, error) {
	if s.list != nil {
		return s.list(ctx, t, id)
	}
	return nil, nil
}
func (s stubRepo) MentionsForComment(ctx context.Context, id string) ([]*comment.Mention, error) {
	if s.mentions != nil {
		return s.mentions(ctx, id)
	}
	return nil, nil
}
func (s stubRepo) Update(ctx context.Context, id string, bodyMd string) (*comment.Comment, error) {
	if s.update != nil {
		return s.update(ctx, id, bodyMd)
	}
	return nil, comment.ErrNotFound
}

func TestAdd_TaskCommentEmitsNotifiedEvent(t *testing.T) {
	repo := stubRepo{
		create: func(_ context.Context, c *comment.Comment) (*comment.Comment, error) {
			c.ID = "c-1"
			c.CreatedAt = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			return c, nil
		},
	}
	notif := &fakeNotifier{}
	svc := New(repo, nil, nil)
	svc.Notifier = notif
	svc.TaskOwnerResolver = fakeTaskOwner{userID: "u-owner", title: "Build the compiler"}

	c := &comment.Comment{
		TargetType: comment.TargetTask,
		TargetID:   "task-1",
		AuthorType: "user",
		AuthorID:   "u-author",
		BodyMD:     "First!",
	}
	_, err := svc.Add(context.Background(), c)
	require.NoError(t, err)

	require.Len(t, notif.events, 1, "Add should fan out exactly one notifier event for a task comment")
	got := notif.events[0]
	assert.Equal(t, "task.commented", got.Type)
	assert.Equal(t, "u-owner", got.UserID, "owner_id is the lookup result, not the author")
	assert.Equal(t, "task", got.TargetType)
	assert.Equal(t, "task-1", got.TargetID)
	assert.Equal(t, "New comment on: Build the compiler", got.Title)
	assert.Equal(t, "/tasks/task-1", got.Link)
	assert.NotEmpty(t, got.DedupKey, "dedup key prevents the next sweep from re-emitting")
}

func TestAdd_TaskCommentSkipsSelfNotify(t *testing.T) {
	repo := stubRepo{
		create: func(_ context.Context, c *comment.Comment) (*comment.Comment, error) {
			c.ID = "c-1"
			c.CreatedAt = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			return c, nil
		},
	}
	notif := &fakeNotifier{}
	svc := New(repo, nil, nil)
	svc.Notifier = notif
	// Owner == author → no event. We only get the user_id back
	// from the resolver, so the "skip self" check uses the user_id
	// form of the author (author_type + ":" + author_id).
	svc.TaskOwnerResolver = fakeTaskOwner{userID: "user:u-self", title: "X"}

	_, err := svc.Add(context.Background(), &comment.Comment{
		TargetType: comment.TargetTask,
		TargetID:   "task-1",
		AuthorType: "user",
		AuthorID:   "u-self",
		BodyMD:     "talking to myself",
	})
	require.NoError(t, err)
	assert.Empty(t, notif.events, "no notification when the author is the owner")
}

func TestAdd_PageCommentDoesNotEmit(t *testing.T) {
	// Comments on wiki pages have their own notification shape; the
	// audit's three missing events are task-specific. Verify the
	// page-target path doesn't accidentally fire task.commented.
	repo := stubRepo{
		create: func(_ context.Context, c *comment.Comment) (*comment.Comment, error) {
			c.ID = "c-1"
			c.CreatedAt = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			return c, nil
		},
	}
	notif := &fakeNotifier{}
	svc := New(repo, nil, nil)
	svc.Notifier = notif
	svc.TaskOwnerResolver = fakeTaskOwner{userID: "u-owner", title: "X"}

	_, err := svc.Add(context.Background(), &comment.Comment{
		TargetType: comment.TargetPage,
		TargetID:   "page-1",
		AuthorType: "user",
		AuthorID:   "u-author",
		BodyMD:     "wiki note",
	})
	require.NoError(t, err)
	assert.Empty(t, notif.events, "page-targeted comments are out of scope for `task.commented`")
}

func TestAdd_InboxTaskSkips(t *testing.T) {
	// Inbox tasks (project_id empty) have no recipient. The
	// resolver returns empty user_id; the service should silently
	// skip the notifier.
	repo := stubRepo{
		create: func(_ context.Context, c *comment.Comment) (*comment.Comment, error) {
			c.ID = "c-1"
			c.CreatedAt = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			return c, nil
		},
	}
	notif := &fakeNotifier{}
	svc := New(repo, nil, nil)
	svc.Notifier = notif
	svc.TaskOwnerResolver = fakeTaskOwner{userID: "", title: "X"}

	_, err := svc.Add(context.Background(), &comment.Comment{
		TargetType: comment.TargetTask,
		TargetID:   "inbox-1",
		AuthorType: "user",
		AuthorID:   "u-author",
		BodyMD:     "inbox",
	})
	require.NoError(t, err)
	assert.Empty(t, notif.events, "no recipient → no event")
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello…", truncate("hello world", 5))
	// Multi-byte rune boundary: don't cut mid-codepoint.
	assert.Equal(t, "café…", truncate("café latte", 4))
	// zero max returns the input unchanged.
	assert.Equal(t, "hello", truncate("hello", 0))
}

// Compile-time check: ErrNotFound is the right sentinel.
var _ = errors.Is(comment.ErrNotFound, comment.ErrNotFound)
