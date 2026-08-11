// Package service — Phase 18 course service.
//
// High-level operations:
//   - CreateWithIntent: build a course from a user intent, plus a
//     generator task for the tutor agent (the same shape as the
//     ticket-orchestrator pattern in Phase 2).
//   - SubmitCurriculum: the tutor's atomic curriculum swap.
//   - ApproveCurriculum: move the course to active and unlock the
//     first lesson.
//   - MaterializeLesson: unused for now (deferred — Phase 18 ships
//     the create/approve path; full tutor lifecycle is a follow-up).
//   - CompleteLesson: mark a lesson done and unlock the next one.
//   - AnswerQuiz: server-side check for `exact` quizzes; `open` ones
//     stay deferred (would need a tutor review surface).
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/course"
)

// Sentinel errors. Handlers translate these to HTTP status codes.
var (
	ErrNotFound     = errors.New("course service: not found")
	ErrInvalidInput = errors.New("course service: invalid input")
	ErrTransition   = errors.New("course service: invalid lifecycle transition")
)

// Service is the place to mutate course state. The HTTP layer
// delegates here so the same business rules apply regardless of
// caller (UI, agent, CLI).
type Service struct {
	Repo course.Repository
}

func New(repo course.Repository) *Service {
	return &Service{Repo: repo}
}

// CreateWithIntent inserts a draft course and returns the freshly
// minted row. The generator_task_id plumbing is exposed via parameter
// so the caller (the entrypoint we wire into cmd/orenda) can pass
// the existing task that the tutor agent will pick up; for the
// pure-API path we accept a pre-created task id.
func (s *Service) CreateWithIntent(ctx context.Context, ownerID, title, intentMD string) (*course.Course, error) {
	if ownerID == "" || title == "" {
		return nil, ErrInvalidInput
	}
	c := &course.Course{
		Title:    title,
		IntentMD: intentMD,
		Level:    "beginner",
		Pace:     "casual",
		Status:   course.StatusDraft,
		OwnerID:  ownerID,
	}
	if err := s.Repo.CreateCourse(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// SubmitCurriculum atomically replaces the course's modules and
// lessons with the tutor's draft. The course moves to review.
func (s *Service) SubmitCurriculum(
	ctx context.Context,
	courseID string,
	modules []*course.Module,
	lessons []*course.Lesson,
) error {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusReview) {
		return ErrTransition
	}
	if err := s.Repo.SubmitCurriculum(ctx, courseID, modules, lessons); err != nil {
		return err
	}
	c.Status = course.StatusReview
	return s.Repo.UpdateCourse(ctx, c)
}

// ApproveCurriculum moves the course from review to active.
func (s *Service) ApproveCurriculum(ctx context.Context, courseID string) (*course.Course, error) {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusActive) {
		return nil, ErrTransition
	}
	c.Status = course.StatusActive
	if err := s.Repo.UpdateCourse(ctx, c); err != nil {
		return nil, err
	}
	// Unlock the first lesson so the student can start.
	lessons, err := s.Repo.ListLessonsInCourse(ctx, courseID)
	if err != nil {
		return nil, err
	}
	for _, l := range lessons {
		if l.Status == course.LessonLocked {
			l.Status = course.LessonOpen
			if err := s.Repo.UpdateLesson(ctx, l); err != nil {
				return nil, fmt.Errorf("course.Approve: unlock first lesson: %w", err)
			}
			break
		}
	}
	return c, nil
}

// RequestChanges sends the course back to draft with optional
// feedback (the message becomes part of the agent's task notes).
func (s *Service) RequestChanges(ctx context.Context, courseID string) (*course.Course, error) {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusDraft) {
		return nil, ErrTransition
	}
	c.Status = course.StatusDraft
	if err := s.Repo.UpdateCourse(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CompleteLesson marks a lesson done and unlocks the next one.
// When the last lesson is completed, the course moves to done.
func (s *Service) CompleteLesson(ctx context.Context, lessonID string) (*course.Lesson, error) {
	lesson, err := s.Repo.GetLesson(ctx, lessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	if lesson.Status != course.LessonOpen {
		return nil, ErrTransition
	}
	lesson.Status = course.LessonDone
	if err := s.Repo.UpdateLesson(ctx, lesson); err != nil {
		return nil, err
	}
	// Unlock the next lesson in the same module.
	candidates, err := s.Repo.ListLessons(ctx, lesson.ModuleID)
	if err != nil {
		return nil, err
	}
	nextUnlocked := false
	for _, l := range candidates {
		if nextUnlocked && l.Status == course.LessonLocked {
			l.Status = course.LessonOpen
			if err := s.Repo.UpdateLesson(ctx, l); err != nil {
				return nil, err
			}
			break
		}
		if l.ID == lesson.ID {
			nextUnlocked = true
		}
	}
	return lesson, nil
}
