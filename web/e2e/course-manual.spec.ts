// @ts-check
/**
 * Phase 27.6 E2E: course built entirely by the owner.
 *
 * Without an agent, the owner must be able to ship a fully-formed
 * program: modules + lessons + quizzes, all in one swap. This is
 * the close-out of the Phase 18 debt ("course_quizzes table doesn't
 * accept quizzes in its payload") and the manual-fill gap that
 * motivated Phase 27.6.
 *
 * The walk:
 *   1. Owner creates a course with skip_generator=true (no agent
 *      task is spawned).
 *   2. Owner submits a curriculum with two modules, each with one
 *      lesson; one lesson has an exact quiz.
 *   3. Owner approves → course is active, first lesson is open.
 *   4. Owner edits the open lesson's content via UI.
 *   5. Owner adds a quiz via the API helper (the Add-quiz UI is
 *      exercised through the editor in a separate vitest; here we
 *      prove the end-to-end data path).
 *   6. Owner opens /lessons/:id, answers both exact quizzes, marks
 *      the lesson done → progress increments.
 *
 * No agent context is created at all. The test is the proof that
 * the course is now usable without one.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  addQuizAsOwner,
  approveCourse,
  createCourse,
  getCourseTree,
  loginAsUser,
  submitCurriculumAsOwner,
} from './helpers'

test.describe('Course manual fill (Phase 27.6)', () => {
  test('owner builds and ships a curriculum without an agent', async ({ page }) => {
    const userCtx = await loginAsUser()

    // 1. Create with skip_generator=true. No agent task is spawned;
    //    a later tutor claim would find an empty inbox.
    const course = await createCourse(userCtx, {
      title: `E2E manual ${Date.now()}`,
      intent_md: 'Learn Vim in a weekend',
      skip_generator: true,
    })
    expect(course.status).toBe('draft')

    // 2. Owner-side curriculum swap: two modules, one lesson each.
    //    The first lesson carries an exact quiz — proves the quiz
    //    round-trip in the swap payload (Phase 18.6 close-out).
    const lessonTitle = 'Modes'
    await submitCurriculumAsOwner(userCtx, course.id, [
      {
        title: 'Movement',
        description: 'how to move around',
        position: 0,
        lessons: [
          {
            title: lessonTitle,
            position: 0,
            content_md: '# Modes\n\nNormal mode is for reading.',
            quizzes: [
              {
                position: 0,
                question_md: 'Which key enters Normal mode?',
                expected_md: 'esc',
                kind: 'exact',
              },
            ],
          },
        ],
      },
      {
        title: 'Editing',
        position: 1,
        lessons: [{ title: 'Insert', position: 0, content_md: '' }],
      },
    ])

    // Sanity: course is in review, both modules persisted, quiz
    // round-tripped through the swap.
    let tree = await getCourseTree(userCtx, course.id)
    expect(tree.course.status).toBe('review')
    expect(tree.modules.length).toBe(2)
    expect(tree.lessons.length).toBe(2)
    expect(tree.quizzes.length).toBe(1)
    expect(tree.quizzes[0].question_md).toBe('Which key enters Normal mode?')
    expect(tree.quizzes[0].expected_md).toBe('esc')

    // 3. Approve → active; first lesson (Movement / Modes) unlocks.
    await approveCourse(userCtx, course.id)
    tree = await getCourseTree(userCtx, course.id)
    expect(tree.course.status).toBe('active')
    const lesson = tree.lessons.find((l) => l.title === lessonTitle)!
    expect(lesson.status).toBe('open')

    // 4. Edit content via the UI to prove the owner-edit affordance
    //    works end-to-end.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.goto(`/lessons/${lesson.id}`)
    await expect(page.getByTestId('lesson-content')).toBeVisible()
    await page.getByTestId('lesson-edit-content').click()
    const newBody = '# Modes\n\nNormal mode is for *reading and navigating*.'
    const textarea = page.locator('[data-testid="lesson-edit-content"] textarea')
    await textarea.fill(newBody)
    await page.getByTestId('lesson-save-content').click()
    await expect(page.getByTestId('lesson-content')).toBeVisible()
    tree = await getCourseTree(userCtx, course.id)
    const refreshed = tree.lessons.find((l) => l.id === lesson.id)!
    expect(refreshed.content_md).toBe(newBody)

    // 5. Append an extra quiz via the targeted endpoint (Phase 18.6
    //    close-out — tutor alternative now also available to owner).
    //    Position 0 in the swap payload yielded position=1 server-side;
    //    the appended quiz lands at MAX+1 = 2. We just verify it
    //    appended and persisted; exact position is irrelevant.
    const added = await addQuizAsOwner(userCtx, lesson.id, {
      question_md: 'What does :q do?',
      expected_md: 'rewind',
      kind: 'exact',
    })
    expect(added.id).toBeTruthy()

    // 6. Open the lesson page again — both quizzes listed; answer
    //    each, then complete the lesson.
    await page.reload()
    await expect(page.getByTestId('lesson-content')).toBeVisible()
    const quizInputs = page.getByTestId('quiz-answer-input')
    await expect(quizInputs).toHaveCount(2)
    await quizInputs.nth(0).fill('esc')
    await page.getByTestId('quiz-submit').nth(0).click()
    await expect(page.getByTestId('quiz-result').nth(0)).toContainText('Correct')
    await quizInputs.nth(1).fill('rewind')
    await page.getByTestId('quiz-submit').nth(1).click()
    await expect(page.getByTestId('quiz-result').nth(1)).toContainText('Correct')

    await page.getByTestId('lesson-complete').click()
    await page.waitForURL((u) => u.pathname === `/courses/${course.id}`)

    tree = await getCourseTree(userCtx, course.id)
    expect(tree.progress.lessons_done).toBeGreaterThanOrEqual(1)
    expect(tree.progress.lessons_total).toBe(2)

    await userCtx.dispose()
  })
})
