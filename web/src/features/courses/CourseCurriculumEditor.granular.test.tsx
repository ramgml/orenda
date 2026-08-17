// @vitest-environment jsdom
/**
 * CourseCurriculumEditor granular-mode tests (Phase 30.13).
 *
 * Active courses save through the granular endpoints (stable IDs,
 * progress preserved) instead of the destructive curriculum swap.
 * Pinned contracts:
 *
 *   1. Active course save calls granular endpoints, NOT
 *      api.submitCurriculum.
 *   2. Rename-only save → only updateModule, no structure call.
 *   3. New module+lesson+quiz → create calls; the structure payload
 *      resolves their server IDs from the create responses.
 *   4. Deleted lesson → deleteLesson called, structure omits it.
 *   5. Reordered modules → diffCurriculum flags structureNeeded with
 *      no other ops; applyGranularPlan then issues ONLY the
 *      structure call (jsdom pointer simulation for dnd-kit is
 *      unreliable, so the reorder path is pinned at the plan level —
 *      the same plan onSave produces).
 *   6. Markdown import replaces local state and a swap-mode save
 *      includes the imported modules.
 */
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  CourseCurriculumEditor,
  applyGranularPlan,
  diffCurriculum,
  type EditorModule,
} from '@/features/courses/CourseCurriculumEditor';
import {
  api,
  type Course,
  type CourseLesson,
  type CourseModule,
  type CourseQuiz,
  type CourseTree,
} from '@/shared/api/client';

function makeCourse(over: Partial<Course> = {}): Course {
  return {
    id: 'c-1',
    title: 'Learn Rust',
    intent_md: '',
    level: 'beginner',
    pace: 'casual',
    status: 'active',
    owner_id: 'u-1',
    created_at: '',
    updated_at: '',
    ...over,
  };
}

function makeModule(over: Partial<EditorModule> = {}): EditorModule {
  return {
    id: 'm-1',
    title: 'Basics',
    description: '',
    position: 0,
    lessons: [
      {
        id: 'l-1',
        title: 'Intro',
        position: 0,
        content_md: '',
        quizzes: [],
      },
    ],
    ...over,
  };
}

function makeServerModule(over: Partial<CourseModule> = {}): CourseModule {
  return { id: 'srv-m', course_id: 'c-1', title: 'x', position: 0, ...over };
}

function makeServerLesson(over: Partial<CourseLesson> = {}): CourseLesson {
  return { id: 'srv-l', module_id: 'srv-m', title: 'x', position: 0, status: 'locked', ...over };
}

function makeServerQuiz(over: Partial<CourseQuiz> = {}): CourseQuiz {
  return { id: 'srv-q', lesson_id: 'srv-l', position: 0, question_md: 'q', kind: 'exact', ...over };
}

function makeTree(): CourseTree {
  return {
    course: makeCourse(),
    modules: [],
    lessons: [],
    quizzes: [],
    progress: { lessons_total: 0, lessons_done: 0 },
  };
}

describe('CourseCurriculumEditor (granular mode)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('saves an active course via granular endpoints, not submitCurriculum', async () => {
    const swapSpy = vi.spyOn(api, 'submitCurriculum');
    const renameSpy = vi.spyOn(api, 'renameLesson').mockResolvedValue(makeServerLesson());
    const structureSpy = vi.spyOn(api, 'applyCourseStructure');
    const onSaved = vi.fn();
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[makeModule()]}
        onCancel={() => {}}
        onSaved={onSaved}
      />,
    );
    fireEvent.change(getByTestId('editor-lesson-title'), { target: { value: 'Intro 2' } });
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(renameSpy).toHaveBeenCalledWith('l-1', 'Intro 2');
    expect(swapSpy).not.toHaveBeenCalled();
    // Pure rename: no positions shifted, so no structure call.
    expect(structureSpy).not.toHaveBeenCalled();
  });

  it('rename module only → only updateModule, no structure call', async () => {
    const updateSpy = vi.spyOn(api, 'updateModule').mockResolvedValue(makeServerModule());
    const structureSpy = vi.spyOn(api, 'applyCourseStructure');
    const renameLessonSpy = vi.spyOn(api, 'renameLesson');
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[makeModule()]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.change(getByTestId('editor-module-title'), { target: { value: 'Basics 2' } });
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(updateSpy).toHaveBeenCalledTimes(1));
    expect(updateSpy).toHaveBeenCalledWith('m-1', { title: 'Basics 2', description: '' });
    expect(structureSpy).not.toHaveBeenCalled();
    expect(renameLessonSpy).not.toHaveBeenCalled();
  });

  it('creates resolve server ids and land in the structure payload', async () => {
    const createModuleSpy = vi
      .spyOn(api, 'createCourseModule')
      .mockResolvedValue(makeServerModule({ id: 'srv-m' }));
    const createLessonSpy = vi
      .spyOn(api, 'createModuleLesson')
      .mockResolvedValue(makeServerLesson({ id: 'srv-l', module_id: 'srv-m' }));
    const addQuizSpy = vi
      .spyOn(api, 'addQuiz')
      .mockResolvedValue(makeServerQuiz({ id: 'srv-q', lesson_id: 'srv-l' }));
    const structureSpy = vi.spyOn(api, 'applyCourseStructure').mockResolvedValue(makeTree());
    const onSaved = vi.fn();
    const { getByTestId, getAllByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[makeModule()]}
        onCancel={() => {}}
        onSaved={onSaved}
      />,
    );

    fireEvent.click(getByTestId('editor-add-module'));
    fireEvent.change(getAllByTestId('editor-module-title')[1], {
      target: { value: 'New Module' },
    });
    fireEvent.click(getAllByTestId('editor-add-lesson')[1]);
    fireEvent.change(getAllByTestId('editor-lesson-title')[1], {
      target: { value: 'New Lesson' },
    });
    fireEvent.click(getAllByTestId('editor-add-quiz')[1]);
    fireEvent.change(getByTestId('editor-quiz-question'), { target: { value: '2+2?' } });
    fireEvent.change(getByTestId('editor-quiz-expected'), { target: { value: '4' } });

    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());

    expect(createModuleSpy).toHaveBeenCalledWith('c-1', {
      title: 'New Module',
      description: '',
    });
    // The lesson was created against the NEW module's server id.
    expect(createLessonSpy).toHaveBeenCalledWith('srv-m', { title: 'New Lesson' });
    // And the quiz against the NEW lesson's server id.
    expect(addQuizSpy).toHaveBeenCalledWith(
      'srv-l',
      expect.objectContaining({ question_md: '2+2?', expected_md: '4', kind: 'exact' }),
    );
    // Structure covers every module/lesson exactly once, with
    // server ids for the new rows.
    expect(structureSpy).toHaveBeenCalledWith('c-1', [
      { module_id: 'm-1', lesson_ids: ['l-1'] },
      { module_id: 'srv-m', lesson_ids: ['srv-l'] },
    ]);
  });

  it('deleted lesson → deleteLesson called, structure payload omits it', async () => {
    const initial = makeModule({
      lessons: [
        { id: 'l-1', title: 'Intro', position: 0, content_md: '', quizzes: [] },
        { id: 'l-2', title: 'Second', position: 1, content_md: '', quizzes: [] },
      ],
    });
    const deleteSpy = vi.spyOn(api, 'deleteLesson').mockResolvedValue(undefined);
    const structureSpy = vi.spyOn(api, 'applyCourseStructure').mockResolvedValue(makeTree());
    const onSaved = vi.fn();
    const { getByTestId, getAllByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[initial]}
        onCancel={() => {}}
        onSaved={onSaved}
      />,
    );
    fireEvent.click(getAllByTestId('editor-lesson-remove')[1]);
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(deleteSpy).toHaveBeenCalledWith('l-2');
    expect(structureSpy).toHaveBeenCalledWith('c-1', [{ module_id: 'm-1', lesson_ids: ['l-1'] }]);
  });

  it('reordered modules → diff is structure-only, plan issues only the structure call', async () => {
    const m1 = makeModule();
    const m2 = makeModule({ id: 'm-2', title: 'Advanced', position: 1, lessons: [] });
    const initial = [m1, m2];
    const reordered = [
      { ...m2, position: 0 },
      { ...m1, position: 1 },
    ];

    const plan = diffCurriculum(initial, reordered);
    expect(plan.structureNeeded).toBe(true);
    expect(plan.moduleUpdates).toEqual([]);
    expect(plan.moduleCreates).toEqual([]);
    expect(plan.moduleDeletes).toEqual([]);
    expect(plan.lessonUpdates).toEqual([]);
    expect(plan.lessonCreates).toEqual([]);
    expect(plan.lessonDeletes).toEqual([]);

    const structureSpy = vi.spyOn(api, 'applyCourseStructure').mockResolvedValue(makeTree());
    const updateSpy = vi.spyOn(api, 'updateModule');
    const createSpy = vi.spyOn(api, 'createCourseModule');
    const deleteSpy = vi.spyOn(api, 'deleteModule');
    await applyGranularPlan('c-1', plan, reordered);
    expect(structureSpy).toHaveBeenCalledWith('c-1', [
      { module_id: 'm-2', lesson_ids: [] },
      { module_id: 'm-1', lesson_ids: ['l-1'] },
    ]);
    expect(updateSpy).not.toHaveBeenCalled();
    expect(createSpy).not.toHaveBeenCalled();
    expect(deleteSpy).not.toHaveBeenCalled();
  });

  it('lesson moved across modules → structureNeeded, no lesson update/delete', () => {
    const lesson = { id: 'l-1', title: 'Intro', position: 0, content_md: '', quizzes: [] };
    const m1 = makeModule({ lessons: [lesson] });
    const m2 = makeModule({ id: 'm-2', title: 'Advanced', position: 1, lessons: [] });
    const moved = [
      { ...m1, lessons: [] },
      { ...m2, lessons: [{ ...lesson }] },
    ];
    const plan = diffCurriculum([m1, m2], moved);
    expect(plan.structureNeeded).toBe(true);
    expect(plan.lessonUpdates).toEqual([]);
    expect(plan.lessonCreates).toEqual([]);
    expect(plan.lessonDeletes).toEqual([]);
  });

  it('quiz-only change → quizUpdates, structure not needed', () => {
    const quiz = {
      id: 'q-1',
      position: 0,
      question_md: '2+2?',
      expected_md: '4',
      kind: 'exact' as const,
    };
    const m = makeModule({
      lessons: [{ id: 'l-1', title: 'Intro', position: 0, content_md: '', quizzes: [quiz] }],
    });
    const edited = [
      {
        ...m,
        lessons: [{ ...m.lessons[0], quizzes: [{ ...quiz, question_md: '3+3?' }] }],
      },
    ];
    const plan = diffCurriculum([m], edited);
    expect(plan.quizUpdates).toEqual([
      { id: 'q-1', question_md: '3+3?', expected_md: '4', kind: 'exact' },
    ]);
    expect(plan.structureNeeded).toBe(false);
  });

  it('validation still blocks granular save before any network call', async () => {
    const updateSpy = vi.spyOn(api, 'updateModule');
    const structureSpy = vi.spyOn(api, 'applyCourseStructure');
    const { getByTestId, getByText } = render(
      <CourseCurriculumEditor
        course={makeCourse()}
        initialModules={[makeModule({ title: '' })]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(getByText(/every module needs a title/i)).toBeTruthy());
    expect(updateSpy).not.toHaveBeenCalled();
    expect(structureSpy).not.toHaveBeenCalled();
  });
});

describe('CourseCurriculumEditor (markdown import)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('apply valid markdown replaces state; swap-mode save includes imported modules', async () => {
    const swapSpy = vi.spyOn(api, 'submitCurriculum').mockResolvedValue({ status: 'review' });
    const onSaved = vi.fn();
    const { getByTestId, queryByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse({ status: 'draft' })}
        initialModules={[]}
        onCancel={() => {}}
        onSaved={onSaved}
      />,
    );
    fireEvent.click(getByTestId('editor-import-toggle'));
    fireEvent.change(getByTestId('editor-import-textarea'), {
      target: { value: '## Imported\nAbout the module.\n### Lesson One\n- [exact] Q? | A' },
    });
    fireEvent.click(getByTestId('editor-import-apply'));

    // Editor now renders the imported tree.
    const titleInput = getByTestId('editor-module-title') as HTMLInputElement;
    expect(titleInput.value).toBe('Imported');
    expect(queryByTestId('editor-quiz-question')).toBeTruthy();

    fireEvent.click(getByTestId('editor-save'));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(swapSpy).toHaveBeenCalledWith(
      'c-1',
      expect.objectContaining({
        modules: [
          expect.objectContaining({
            title: 'Imported',
            description: 'About the module.',
            lessons: [
              expect.objectContaining({
                title: 'Lesson One',
                quizzes: [
                  expect.objectContaining({ kind: 'exact', question_md: 'Q?', expected_md: 'A' }),
                ],
              }),
            ],
          }),
        ],
      }),
    );
  });

  it('import over a non-empty tree asks for confirmation', () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const { getByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse({ status: 'draft' })}
        initialModules={[makeModule()]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-import-toggle'));
    fireEvent.change(getByTestId('editor-import-textarea'), {
      target: { value: '## Replacement' },
    });
    fireEvent.click(getByTestId('editor-import-apply'));
    expect(confirmSpy).toHaveBeenCalled();
    // Declined → existing module untouched.
    const titleInput = getByTestId('editor-module-title') as HTMLInputElement;
    expect(titleInput.value).toBe('Basics');
  });

  it('import with no parseable modules shows an inline error', () => {
    const { getByTestId, getByText, queryByTestId } = render(
      <CourseCurriculumEditor
        course={makeCourse({ status: 'draft' })}
        initialModules={[]}
        onCancel={() => {}}
        onSaved={() => {}}
      />,
    );
    fireEvent.click(getByTestId('editor-import-toggle'));
    fireEvent.change(getByTestId('editor-import-textarea'), {
      target: { value: 'just some prose, no modules' },
    });
    fireEvent.click(getByTestId('editor-import-apply'));
    expect(getByText(/nothing to import/i)).toBeTruthy();
    expect(queryByTestId('editor-module')).toBeNull();
  });
});
