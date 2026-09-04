// @vitest-environment jsdom
/**
 * LessonPage component tests (Phase 27.4.B).
 *
 * The component boots from a list-then-tree fetch — `api.listCourses`
 * then `api.getCourse(courseId)` for each owned course until the
 * lesson is found. The tests pin three contracts:
 *
 *   1. Locked lessons render the placeholder; quizzes are hidden
 *      and the "complete" button stays disabled.
 *   2. Open lessons render the markdown content, list quizzes,
 *      and disable the "complete" button until every quiz has an
 *      answer.
 *   3. Submitting an answer hits the backend and the verdict
 *      shows up inline.
 *
 * Network plumbing is mocked at the `api` boundary — we don't want
 * to spin up a runtime for what is a pure rendering test.
 */
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { LessonPage } from '@/features/courses/LessonPage';
import { api } from '@/shared/api/client';

function makeLesson(
  over: Partial<{
    id: string;
    title: string;
    status: string;
    content_md: string;
    task_id: string;
  }> = {},
) {
  return {
    id: 'lesson-1',
    title: 'Variables and types',
    status: 'open',
    content_md: '# Heading\n\nExplain **borrowing** in one sentence.',
    task_id: '',
    ...over,
  };
}

const tree = (lessons: any[], quizzes: any[] = []) => ({
  course: {
    id: 'c-1',
    title: 'Learn Rust',
    intent_md: '',
    level: 'beginner',
    pace: 'casual',
    status: 'active' as const,
    owner_id: 'u-1',
    generator_task_id: '',
    number: 1,
    created_at: '',
    updated_at: '',
  },
  modules: [{ id: 'm-1', course_id: 'c-1', title: 'Intro', description: '', position: 0 }],
  lessons,
  quizzes,
  progress: {
    lessons_total: lessons.length,
    lessons_done: lessons.filter((l) => l.status === 'done').length,
  },
});

/**
 * Install fresh api mocks. We re-spy on every test instead of
 * mutating a long-lived spy because vi's spy state carries over
 * if you only mockReset (the next mockResolvedValue chains on
 * the previous queue). Rebinding the variable avoids that.
 */
function setupApi(opts: { treeResp: ReturnType<typeof tree> }) {
  vi.spyOn(api, 'listCourses').mockResolvedValue({
    courses: [
      {
        id: 'c-1',
        title: 'Learn Rust',
        intent_md: '',
        level: 'beginner',
        pace: 'casual',
        status: 'active',
        owner_id: 'u-1',
        number: 1,
        created_at: '',
        updated_at: '',
      },
    ],
    count: 1,
  });
  vi.spyOn(api, 'getCourse').mockResolvedValue(opts.treeResp);
  vi.spyOn(api, 'answerQuiz').mockResolvedValue({ correct: true });
  vi.spyOn(api, 'completeLesson').mockResolvedValue({});
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/lessons/lesson-1']}>
      <Routes>
        <Route path="/lessons/:id" element={<LessonPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('LessonPage', () => {
  beforeEach(() => {
    // Always start with a clean slate. The previous test's
    // mockResolvedValue queues would otherwise chain on top.
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('shows a locked placeholder when the lesson is locked', async () => {
    setupApi({
      treeResp: tree([
        { id: 'lesson-1', module_id: 'm-1', title: 'Locked', position: 0, status: 'locked' },
      ]),
    });
    const { findByTestId, queryByTestId } = renderPage();
    expect(await findByTestId('lesson-locked')).toBeTruthy();
    expect(queryByTestId('lesson-content')).toBeNull();
    // The complete button is always rendered but disabled for
    // locked lessons — the lock is the source of truth.
    const complete = await findByTestId('lesson-complete');
    expect(complete.hasAttribute('disabled')).toBe(true);
  });

  it('renders the markdown content for an open lesson', async () => {
    setupApi({ treeResp: tree([makeLesson()]) });
    const { findByTestId } = renderPage();
    const content = await findByTestId('lesson-content');
    expect(content.querySelector('h1')?.textContent).toBe('Heading');
    expect(content.querySelector('strong')?.textContent).toBe('borrowing');
  });

  it('disables the complete button until every quiz has been answered', async () => {
    setupApi({
      treeResp: tree(
        [makeLesson()],
        [
          { id: 'q1', lesson_id: 'lesson-1', position: 0, question_md: '?', kind: 'exact' },
          { id: 'q2', lesson_id: 'lesson-1', position: 1, question_md: '?', kind: 'exact' },
        ],
      ),
    });
    const answerSpy = vi.spyOn(api, 'answerQuiz').mockResolvedValue({ correct: true });
    const { findByTestId, findAllByTestId } = renderPage();
    const complete = await findByTestId('lesson-complete');
    expect(complete.hasAttribute('disabled')).toBe(true);

    const inputs = await findAllByTestId('quiz-answer-input');
    expect(inputs.length).toBe(2);
    const submits = await findAllByTestId('quiz-submit');
    fireEvent.change(inputs[0], { target: { value: 'answer-1' } });
    fireEvent.click(submits[0]);
    await waitFor(() => expect(answerSpy).toHaveBeenCalledTimes(1));
    fireEvent.change(inputs[1], { target: { value: 'answer-2' } });
    fireEvent.click(submits[1]);
    await waitFor(() => expect(answerSpy).toHaveBeenCalledTimes(2));
    const enabled = await findByTestId('lesson-complete');
    expect(enabled.hasAttribute('disabled')).toBe(false);
  });

  it('shows the backend verdict after submit', async () => {
    setupApi({
      treeResp: tree(
        [makeLesson()],
        [{ id: 'q1', lesson_id: 'lesson-1', position: 0, question_md: '?', kind: 'exact' }],
      ),
    });
    vi.spyOn(api, 'answerQuiz').mockResolvedValue({ correct: true, feedback_md: '' });
    const { findByTestId, findAllByTestId } = renderPage();
    const rows = await findAllByTestId('quiz-row');
    expect(rows.length).toBe(1);
    const input = (await findAllByTestId('quiz-answer-input'))[0];
    fireEvent.change(input, { target: { value: 'hello' } });
    fireEvent.click((await findAllByTestId('quiz-submit'))[0]);
    const result = await findByTestId('quiz-result');
    expect(result.textContent).toContain('Correct');
  });

  // ---- Phase 27.6: owner-edit affordance for active-course lessons ----

  it('shows the Edit content button when the course is active', async () => {
    setupApi({ treeResp: tree([makeLesson()]) });
    const { findByTestId, queryByTestId } = renderPage();
    // tree default has course.status='active' — the button is rendered
    // as a sibling of lesson-content.
    expect(await findByTestId('lesson-content')).toBeTruthy();
    expect(await findByTestId('lesson-edit-content')).toBeTruthy();
    // No textarea before clicking.
    expect(queryByTestId('lesson-edit-content-textarea')).toBeNull();
  });

  it('hides the Edit content button when the course is not active', async () => {
    vi.restoreAllMocks();
    vi.spyOn(api, 'listCourses').mockResolvedValue({
      courses: [
        {
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
        },
      ],
      count: 1,
    });
    vi.spyOn(api, 'getCourse').mockResolvedValue({
      course: {
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
      },
      modules: [{ id: 'm-1', course_id: 'c-1', title: 'Intro', description: '', position: 0 }],
      lessons: [
        {
          id: 'lesson-1',
          module_id: 'm-1',
          title: 'L',
          position: 0,
          status: 'open',
          number: 1,
          content_md: 'x',
        },
      ],
      quizzes: [],
      progress: { lessons_total: 1, lessons_done: 0 },
    });
    vi.spyOn(api, 'answerQuiz').mockResolvedValue({ correct: true });
    vi.spyOn(api, 'completeLesson').mockResolvedValue({});

    const { findByTestId, queryByTestId } = renderPage();
    await findByTestId('lesson-content');
    expect(queryByTestId('lesson-edit-content')).toBeNull();
  });

  it('saves content via api.updateLessonContent and reloads', async () => {
    setupApi({ treeResp: tree([makeLesson()]) });
    const updateSpy = vi
      .spyOn(api, 'updateLessonContent')
      .mockResolvedValue({} as unknown as Awaited<ReturnType<typeof api.updateLessonContent>>);
    const { findByTestId } = renderPage();
    await findByTestId('lesson-content');
    const editBtn = await findByTestId('lesson-edit-content');
    fireEvent.click(editBtn);
    const saveBtn = await findByTestId('lesson-save-content');
    fireEvent.click(saveBtn);
    await waitFor(() => expect(updateSpy).toHaveBeenCalledTimes(1));
    expect(updateSpy).toHaveBeenCalledWith(
      'lesson-1',
      expect.objectContaining({ content_md: expect.any(String) }),
    );
  });
});
