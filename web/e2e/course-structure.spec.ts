// @ts-check
/**
 * Phase 30.13 E2E: structural edits on an ACTIVE course preserve
 * student progress.
 *
 * The curriculum swap (Phase 27.6) is destructive — rows are deleted
 * and re-inserted, resetting lesson status. Once a student is moving
 * through a course, the owner needs surgical edits instead: rename a
 * module, add a lesson, without losing what was completed.
 *
 * The walk:
 *   1. Owner creates a course (skip_generator), submits a two-module
 *      curriculum, approves → active; first lesson opens.
 *   2. Owner completes lesson 1 → status done, lesson 2 opens.
 *   3. Owner opens the course page, enters the curriculum editor
 *      (available in active since 30.13), renames module 2, adds a
 *      third lesson to module 1, saves via the granular path
 *      ("Save changes" — NOT the swap).
 *   4. Server truth: module renamed, new lesson present and born
 *      locked, lesson 1 still `done`, lesson 2 still `open`.
 *
 * Reorder itself is covered by vitest (plan level) and the backend
 * structure tests; dnd-kit pointer sequences are unreliable in
 * headless Chromium (see kanban.spec.ts note).
 */
import { expect, test } from '@playwright/test';

import {
  E2E_PASSWORD,
  approveCourse,
  completeLesson,
  createCourse,
  getCourseTree,
  loginAsUser,
  submitCurriculumAsOwner,
} from './helpers';

test.describe('Course structure edits on active course (Phase 30.13)', () => {
  test('rename + add lesson granularly; completed lesson stays done', async ({ page }) => {
    const userCtx = await loginAsUser();

    // 1. Build and activate the course without an agent.
    const course = await createCourse(userCtx, {
      title: `E2E structure ${Date.now()}`,
      intent_md: 'Preserve progress through edits',
      skip_generator: true,
    });
    await submitCurriculumAsOwner(userCtx, course.id, [
      {
        title: 'Alpha',
        position: 0,
        lessons: [
          { title: 'A1', position: 0, content_md: '' },
          { title: 'A2', position: 1, content_md: '' },
        ],
      },
      {
        title: 'Beta',
        position: 1,
        lessons: [{ title: 'B1', position: 0, content_md: '' }],
      },
    ]);
    await approveCourse(userCtx, course.id);

    // 2. Student completes the first lesson.
    let tree = await getCourseTree(userCtx, course.id);
    const lessonA1 = tree.lessons.find((l) => l.title === 'A1');
    const lessonA2 = tree.lessons.find((l) => l.title === 'A2');
    if (!lessonA1 || !lessonA2) throw new Error('seed lessons missing');
    await completeLesson(userCtx, lessonA1.id);
    tree = await getCourseTree(userCtx, course.id);
    expect(tree.progress.lessons_done).toBe(1);
    expect(tree.progress.lessons_total).toBe(3);

    // 3. Owner edits the active course via the granular editor.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.goto(`/courses/${course.id}`);
    await page.getByRole('button', { name: 'Edit curriculum' }).click();

    // Rename module 2 (second module title input).
    await page.getByTestId('editor-module-title').nth(1).fill('Beta Renamed');

    // Add a lesson to module 1: its "+ Add lesson" is the first one.
    await page.getByTestId('editor-add-lesson').nth(0).click();
    await page.getByTestId('editor-lesson-title').nth(2).fill('A3');

    // Granular save (active mode label).
    await page.getByTestId('editor-save').click();
    await expect(page.getByTestId('editor-save')).toHaveCount(0); // editor closed on save

    // 4. Server truth after reload.
    await page.reload();
    await expect(page.getByText('Beta Renamed')).toBeVisible();

    tree = await getCourseTree(userCtx, course.id);
    const modBeta = tree.modules.find((m) => m.title === 'Beta Renamed');
    expect(modBeta, 'module rename persisted').toBeTruthy();

    const a1 = tree.lessons.find((l) => l.id === lessonA1.id);
    const a2 = tree.lessons.find((l) => l.id === lessonA2.id);
    const a3 = tree.lessons.find((l) => l.title === 'A3');
    expect(a1?.status, 'completed lesson must stay done').toBe('done');
    expect(a2?.status, 'open lesson must stay open').toBe('open');
    expect(a3, 'new lesson added').toBeTruthy();
    expect(a3?.status, 'new lesson is born locked').toBe('locked');
    expect(tree.progress.lessons_total).toBe(4);
    expect(tree.progress.lessons_done).toBe(1);
  });
});
