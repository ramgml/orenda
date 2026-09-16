// Package tutor — the lesson-scoped dialog tutor (T16).
//
// A tutoring thread is (lesson_id, user_id): the student asks a
// question from the lesson page, an agent answers in a later turn.
// Messages persist in tutor_messages (migration 045); the agent
// pulls pending questions via GET /agent/tutor/pending (the payload
// embeds the lesson context so one call is enough to answer) and
// replies via POST /agent/tutor/{lesson_id}/reply.
//
// Pending is derived, never stored: a thread is pending iff its last
// message has role='user'. Ask flips the thread to pending; Reply
// resolves it. A reply with no pending question is a conflict.
package tutor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

// Sentinel errors the API layer translates into HTTP statuses:
// ErrNotFound → 404, ErrInvalidInput → 400, ErrConflict → 409.
var (
	ErrNotFound     = errors.New("tutor service: not found")
	ErrInvalidInput = errors.New("tutor service: invalid input")
	ErrConflict     = errors.New("tutor service: conflict")
)

// Role discriminates the two sides of the dialog.
type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

// IsValid for Role — closed set above.
func (r Role) IsValid() bool {
	return r == RoleUser || r == RoleAgent
}

// Message is one turn in a lesson tutoring thread.
type Message struct {
	ID        string    `json:"id"`
	LessonID  string    `json:"lesson_id"`
	UserID    string    `json:"user_id"`
	Role      Role      `json:"role"`
	BodyMD    string    `json:"body_md"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository persists tutor messages. Implemented by
// *sqlite.TutorMessageRepository; in-memory stubs keep service tests
// off SQLite.
type Repository interface {
	// Create inserts one message; fills ID (UUIDv7) when empty.
	Create(ctx context.Context, m *Message) error
	// ListThread returns the thread's messages in chronological
	// order (oldest first). The caller scopes by user.
	ListThread(ctx context.Context, lessonID, userID string) ([]*Message, error)
	// Last returns the newest message of the thread (nil, nil when
	// the thread has no messages yet).
	Last(ctx context.Context, lessonID, userID string) (*Message, error)
}

// PendingQuestion is one row of the agent's work queue: who asked,
// what they asked, and the lesson material to answer from (content +
// quizzes embedded so the agent answers in a single round-trip).
type PendingQuestion struct {
	Message
	CourseID    string         `json:"course_id"`
	CourseTitle string         `json:"course_title"`
	LessonTitle string         `json:"lesson_title"`
	ContentMD   string         `json:"content_md"`
	Quizzes     []*course.Quiz `json:"quizzes"`
}

// Service implements the tutor use cases on top of the message
// repository and the course domain (lesson → module → course walk +
// quiz listing). Both deps come from the sqlite layer in production;
// stubs keep service tests off the database.
type Service struct {
	Repo   Repository
	Course course.Repository
}

// New wires a Service.
func New(repo Repository, courses course.Repository) *Service {
	return &Service{Repo: repo, Course: courses}
}

// Ask records a student question on the lesson thread. The thread
// becomes pending (its last message is now role='user'); any
// previous unresolved question is a conflict — one open question per
// thread keeps the agent's queue unambiguous.
func (s *Service) Ask(ctx context.Context, lessonID, userID, bodyMD string) (*Message, error) {
	if lessonID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.ensureLesson(ctx, lessonID); err != nil {
		return nil, err
	}
	bodyMD = strings.TrimSpace(bodyMD)
	if bodyMD == "" {
		return nil, ErrInvalidInput
	}
	last, err := s.Repo.Last(ctx, lessonID, userID)
	if err != nil {
		return nil, err
	}
	if last != nil && last.Role == RoleUser {
		return nil, ErrConflict
	}
	m := &Message{
		LessonID: lessonID,
		UserID:   userID,
		Role:     RoleUser,
		BodyMD:   bodyMD,
	}
	if err := s.Repo.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("tutor.Ask: %w", err)
	}
	return m, nil
}

// History returns the student's thread in chronological order.
// Unknown lesson → ErrNotFound (the UI distinguishes "empty thread"
// from "no such lesson" by this).
func (s *Service) History(ctx context.Context, lessonID, userID string) ([]*Message, error) {
	if lessonID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.ensureLesson(ctx, lessonID); err != nil {
		return nil, err
	}
	msgs, err := s.Repo.ListThread(ctx, lessonID, userID)
	if err != nil {
		return nil, fmt.Errorf("tutor.History: %w", err)
	}
	return msgs, nil
}

// ListPending walks every lesson thread whose last message is a
// user question and embeds the lesson context (course, title,
// content_md, quizzes) so the agent can answer in one call.
//
// Single-owner install: the pending set is small; a cross-thread
// scan via Repo.Last per thread is fine. Threads are discovered by
// scanning lessons with student questions — the repo exposes
// PendingThreads for that.
type PendingLister interface {
	// PendingThreads returns distinct (lesson_id, user_id) thread
	// keys whose last message has role='user'.
	PendingThreads(ctx context.Context) ([]PendingThreadKey, error)
}

// PendingThreadKey identifies a thread.
type PendingThreadKey struct {
	LessonID string
	UserID   string
}

// ListPending builds the agent's queue. Requires the repo to
// implement PendingLister (the sqlite repo does).
func (s *Service) ListPending(ctx context.Context) ([]*PendingQuestion, error) {
	lister, ok := s.Repo.(PendingLister)
	if !ok {
		return nil, fmt.Errorf("tutor.ListPending: repo does not support PendingThreads")
	}
	keys, err := lister.PendingThreads(ctx)
	if err != nil {
		return nil, fmt.Errorf("tutor.ListPending: %w", err)
	}
	out := make([]*PendingQuestion, 0, len(keys))
	for _, k := range keys {
		last, err := s.Repo.Last(ctx, k.LessonID, k.UserID)
		if err != nil {
			return nil, fmt.Errorf("tutor.ListPending: %w", err)
		}
		if last == nil || last.Role != RoleUser {
			continue // raced with a reply; not pending anymore
		}
		pq, err := s.withLessonContext(ctx, last)
		if err != nil {
			return nil, err
		}
		out = append(out, pq)
	}
	return out, nil
}

// Reply records the agent's answer on the thread. Requires a pending
// question (ErrConflict otherwise); unknown lesson → ErrNotFound.
// On success the thread is resolved and the loop is closed: the
// student sees the answer live via the WS topic "tutor".
func (s *Service) Reply(ctx context.Context, lessonID, bodyMD string) (*Message, error) {
	if lessonID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.ensureLesson(ctx, lessonID); err != nil {
		return nil, err
	}
	bodyMD = strings.TrimSpace(bodyMD)
	if bodyMD == "" {
		return nil, ErrInvalidInput
	}
	// The reply lands on the pending thread of this lesson. A lesson
	// can carry several user threads (one per student); the agent
	// addresses the question it pulled, so the caller pins the user.
	// We derive it: the pending user of this lesson.
	lister, ok := s.Repo.(PendingLister)
	if !ok {
		return nil, fmt.Errorf("tutor.Reply: repo does not support PendingThreads")
	}
	keys, err := lister.PendingThreads(ctx)
	if err != nil {
		return nil, fmt.Errorf("tutor.Reply: %w", err)
	}
	userID := ""
	for _, k := range keys {
		if k.LessonID == lessonID {
			userID = k.UserID
			break
		}
	}
	if userID == "" {
		return nil, ErrConflict
	}
	m := &Message{
		LessonID: lessonID,
		UserID:   userID,
		Role:     RoleAgent,
		BodyMD:   bodyMD,
	}
	if err := s.Repo.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("tutor.Reply: %w", err)
	}
	return m, nil
}

// withLessonContext walks lesson → module → course and attaches the
// material the agent answers from. A dangling lesson (no module /
// course row) is a data bug → ErrNotFound.
func (s *Service) withLessonContext(ctx context.Context, m *Message) (*PendingQuestion, error) {
	lesson, err := s.Course.GetLesson(ctx, m.LessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	mod, err := s.Course.GetModule(ctx, lesson.ModuleID)
	if err != nil {
		return nil, ErrNotFound
	}
	c, err := s.Course.GetCourse(ctx, mod.CourseID)
	if err != nil {
		return nil, ErrNotFound
	}
	quizzes, err := s.Course.ListQuizzesInCourse(ctx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("tutor.ListPending: quizzes: %w", err)
	}
	// Scope the quiz dump to the lesson the student is reading —
	// the course-wide list would bury the relevant ones.
	lessonQuizzes := make([]*course.Quiz, 0, len(quizzes))
	for _, q := range quizzes {
		if q.LessonID == lesson.ID {
			lessonQuizzes = append(lessonQuizzes, q)
		}
	}
	return &PendingQuestion{
		Message:     *m,
		CourseID:    c.ID,
		CourseTitle: c.Title,
		LessonTitle: lesson.Title,
		ContentMD:   lesson.ContentMD,
		Quizzes:     lessonQuizzes,
	}, nil
}

// ensureLesson fails fast on an unknown lesson so a typo'd URL
// yields 404 instead of a ghost thread.
func (s *Service) ensureLesson(ctx context.Context, lessonID string) error {
	if _, err := s.Course.GetLesson(ctx, lessonID); err != nil {
		return ErrNotFound
	}
	return nil
}
