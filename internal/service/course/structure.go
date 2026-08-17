// Package service — Phase 30.13: granular curriculum CRUD with stable IDs.
//
// The curriculum swap (SubmitCurriculum) is the right tool for a
// wholesale rewrite of a draft/review course, but it is destructive:
// rows are deleted and re-inserted, so lesson status (student
// progress) and task links are lost. Once a course is active the
// owner (or the tutor agent) needs surgical edits instead — rename
// a module, add a lesson, fix a quiz, drag lessons around — without
// touching the rest of the tree. This file is that surface.
//
// Rules:
//   - Structural edits are allowed in draft, review, AND active.
//     done/archived courses are frozen (ErrTransition).
//   - No row is ever re-inserted here: IDs are stable by
//     construction, progress survives by construction.
//   - Deleting a module/lesson deletes its content (cascade) —
//     that is an explicit act, not a side effect of an edit.
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/ramgml/orenda/internal/domain/course"
)

// editableCourse loads the course and gates structural mutations on
// its status: draft/review/active are editable, done/archived are
// frozen. All granular ops below go through this gate so the rule
// lives in exactly one place.
func (s *Service) editableCourse(ctx context.Context, courseID string) (*course.Course, error) {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if c.Status == course.StatusDone || c.Status == course.StatusArchived {
		return nil, ErrTransition
	}
	return c, nil
}

// courseOfModule walks module → course and applies the editability
// gate. Returns the module for the caller's convenience.
func (s *Service) courseOfModule(ctx context.Context, moduleID string) (*course.Module, error) {
	m, err := s.Repo.GetModule(ctx, moduleID)
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.editableCourse(ctx, m.CourseID); err != nil {
		return nil, err
	}
	return m, nil
}

// courseOfLesson walks lesson → module → course with the gate.
func (s *Service) courseOfLesson(ctx context.Context, lessonID string) (*course.Lesson, error) {
	l, err := s.Repo.GetLesson(ctx, lessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.courseOfModule(ctx, l.ModuleID); err != nil {
		return nil, err
	}
	return l, nil
}

// courseOfQuiz walks quiz → lesson → module → course with the gate.
func (s *Service) courseOfQuiz(ctx context.Context, quizID string) (*course.Quiz, error) {
	q, err := s.Repo.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.courseOfLesson(ctx, q.LessonID); err != nil {
		return nil, err
	}
	return q, nil
}

// AddModule appends a module at the end of the course.
func (s *Service) AddModule(ctx context.Context, courseID, title, description string) (*course.Module, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.editableCourse(ctx, courseID); err != nil {
		return nil, err
	}
	existing, err := s.Repo.ListModules(ctx, courseID)
	if err != nil {
		return nil, err
	}
	m := &course.Module{
		CourseID:    courseID,
		Title:       title,
		Description: description,
		Position:    len(existing) + 1,
	}
	if err := s.Repo.CreateModule(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// UpdateModule renames a module / edits its description in place.
func (s *Service) UpdateModule(ctx context.Context, moduleID, title, description string) (*course.Module, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	m, err := s.courseOfModule(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	m.Title = title
	m.Description = description
	if err := s.Repo.UpdateModule(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteModule removes the module and (via cascade) its lessons and
// quizzes. Deleting content intentionally drops the progress those
// lessons carried — that is the owner's explicit choice.
func (s *Service) DeleteModule(ctx context.Context, moduleID string) error {
	if _, err := s.courseOfModule(ctx, moduleID); err != nil {
		return err
	}
	return s.Repo.DeleteModule(ctx, moduleID)
}

// AddLesson appends a locked lesson at the end of the module. A new
// lesson is always born locked even in an active course — the tutor
// (or owner) materializes it before the student sees it, exactly
// like lessons from a fresh curriculum.
func (s *Service) AddLesson(ctx context.Context, moduleID, title string) (*course.Lesson, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	if _, err := s.courseOfModule(ctx, moduleID); err != nil {
		return nil, err
	}
	existing, err := s.Repo.ListLessons(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	l := &course.Lesson{
		ModuleID: moduleID,
		Title:    title,
		Status:   course.LessonLocked,
		Position: len(existing) + 1,
	}
	if err := s.Repo.CreateLesson(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// RenameLesson edits the lesson title in place, preserving status,
// content, position and task link.
func (s *Service) RenameLesson(ctx context.Context, lessonID, title string) (*course.Lesson, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	l, err := s.courseOfLesson(ctx, lessonID)
	if err != nil {
		return nil, err
	}
	l.Title = title
	if err := s.Repo.UpdateLesson(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// DeleteLesson removes the lesson and its quizzes (cascade).
func (s *Service) DeleteLesson(ctx context.Context, lessonID string) error {
	if _, err := s.courseOfLesson(ctx, lessonID); err != nil {
		return err
	}
	return s.Repo.DeleteLesson(ctx, lessonID)
}

// UpdateQuiz edits a quiz's question/expected answer/kind in place.
func (s *Service) UpdateQuiz(ctx context.Context, quizID, questionMD, expectedMD string, kind course.QuizKind) (*course.Quiz, error) {
	if strings.TrimSpace(questionMD) == "" {
		return nil, ErrInvalidInput
	}
	switch kind {
	case course.QuizOpen, course.QuizExact:
	default:
		return nil, ErrInvalidInput
	}
	q, err := s.courseOfQuiz(ctx, quizID)
	if err != nil {
		return nil, err
	}
	q.QuestionMD = questionMD
	q.ExpectedMD = expectedMD
	q.Kind = kind
	if err := s.Repo.UpdateQuiz(ctx, q); err != nil {
		return nil, err
	}
	return q, nil
}

// DeleteQuiz removes a single quiz from its lesson.
func (s *Service) DeleteQuiz(ctx context.Context, quizID string) error {
	if _, err := s.courseOfQuiz(ctx, quizID); err != nil {
		return err
	}
	return s.Repo.DeleteQuiz(ctx, quizID)
}

// ApplyStructure applies a drag-and-drop reorder: module order plus
// each module's lesson order, with lessons allowed to move across
// modules. The repo enforces exact coverage (every module and lesson
// of the course named exactly once) inside the same transaction, so
// a malformed payload leaves the tree untouched.
func (s *Service) ApplyStructure(ctx context.Context, courseID string, modules []course.ModuleOrder) error {
	if _, err := s.editableCourse(ctx, courseID); err != nil {
		return err
	}
	if err := s.Repo.ApplyStructure(ctx, courseID, modules); err != nil {
		if errors.Is(err, course.ErrInvalidInput) {
			return ErrInvalidInput
		}
		return err
	}
	return nil
}
