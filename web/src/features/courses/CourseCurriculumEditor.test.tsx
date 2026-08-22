// @vitest-environment jsdom
/**
 * CourseCurriculumEditor tests (Phase 27.6).
 *
 * The editor is presentational — it owns its local modules/lessons/quizzes
 * tree and fires api.submitCurriculum on save. We pin four contracts:
 *
 *   1. Add module → +1 module.
 *   2. Add lesson within a module → nested lesson appears.
 *   3. Save → api.submitCurriculum called with the expected wire shape.
 *   4. Read-only when course.status is not draft/review.
 *   5. Save validation: missing titles / questions / expected answers
 *      block the save and surface an inline error.
 */
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CourseCurriculumEditor } from '@/features/courses/CourseCurriculumEditor';
import { api, type Course } from '@/shared/api/client';

function makeCourse(over: Partial<Course> = {}): Course {
  return {
    id: 'c-1',
    title: 'Learn Rust',
    intent_md: '',
    level: 'beginner',
    pace: 'casual',
    status: 'draft',
    owner_id: 'u-1',
    number: 1,
    created_at: '',
    updated_at: '',
    ...over,
  };
}

describe('CourseCurriculumEditor', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('renders an empty state when no modules are supplied', () => {
    const { getByText } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(getByText(/no modules yet/i)).toBeTruthy();
  });

  it('adds a module on demand', () => {
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-add-module'));
    expect(getByTestId('editor-module')).toBeTruthy();
  });

  it('adds a lesson within a module', () => {
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[
          {
            id: 'm-1',
            title: 'Basics',
            description: '',
            position: 0,
            lessons: [],
          },
        ]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-add-lesson'));
    expect(getByTestId('editor-lesson')).toBeTruthy();
  });

  it('saves a structured tree via api.submitCurriculum', async () => {
    const spy = vi.spyOn(api, 'submitCurriculum').mockResolvedValue({ status: 'review' });
    const onSaved = vi.fn();
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[
          {
            id: 'm-1',
            title: 'Basics',
            description: '',
            position: 0,
            lessons: [
              {
                id: 'l-1',
                title: 'Hello',
                position: 0,
                content_md: 'hi',
                quizzes: [
                  {
                    id: 'q-1',
                    position: 0,
                    question_md: '2+2?',
                    expected_md: '4',
                    kind: 'exact',
                  },
                ],
              },
            ],
          },
        ]}
        onCancel={() => {}}
        onSaved={onSaved}
      />,
    );
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1));
    expect(spy).toHaveBeenCalledWith(
      'c-1',
      expect.objectContaining({
        modules: [
          expect.objectContaining({
            id: 'm-1',
            title: 'Basics',
            lessons: [
              expect.objectContaining({
                id: 'l-1',
                title: 'Hello',
                quizzes: [expect.objectContaining({ kind: 'exact', question_md: '2+2?' })],
              }),
            ],
          }),
        ],
      }),
    );
    expect(onSaved).toHaveBeenCalled();
  });

  it('refuses save when a module is missing a title', async () => {
    const spy = vi.spyOn(api, 'submitCurriculum').mockResolvedValue({ status: 'review' });
    const { getByTestId, getByText } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[
          {
            id: 'm-1',
            title: '',
            description: '',
            position: 0,
            lessons: [],
          },
        ]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(getByText(/every module needs a title/i)).toBeTruthy());
    expect(spy).not.toHaveBeenCalled();
  });

  it('is read-only when the course is done/archived', () => {
    // Phase 30.13: active courses are editable granularly; only the
    // frozen terminal statuses keep the read-only gate.
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse({ status: 'done' })}
        initialModules={[]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(getByTestId('editor-readonly')).toBeTruthy();
  });
});
