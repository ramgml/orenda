// @ts-check
/**
 * Course E2E happy path (Phase 27.4).
 *
 * Walks the full LMS lifecycle the user + tutor agent would:
 *
 *   1. Owner creates a course with intent → /draft.
 *   2. Tutor submits a curriculum (modules + lessons) via the
 *      agent endpoint → /review.
 *   3. Owner approves → /active, first lesson unlocks.
 *   4. Tutor materialises the lesson (writes content_md +
 *      status='open') → owner can read.
 *   5. Owner opens /lessons/:id, sees the markdown rendered,
 *      marks the lesson done.
 *   6. The course tree shows progress incremented.
 *
 * Where the backend capabilities are exercised directly via the
 * REST surface (agent tutors in production would be a separate
 * process), the owner-side UI is driven through the browser. This
 * shape keeps the test resilient to minor UI tweaks while still
 * verifying the end-to-end data path.
 *
 * Quiz answering is covered by the LessonPage vitest suite — the
 * course_quizzes table is owned by the agent curriculum endpoint,
 * which doesn't yet accept quizzes in its payload (Phase 27.4
 * forward-looking). Wiring a quiz-creation endpoint is the next
 * step for a full LMS E2E.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  approveCourse,
  createAgent,
  createCourse,
  getCourseTree,
  loginAsAgent,
  loginAsUser,
  materializeLesson,
  submitCurriculum,
} from './helpers'

test.describe('Course happy path (Phase 27.4)', () => {
  test('owner creates → tutor submits → owner approves → student learns', async ({
    page,
  }) => {
    const userCtx = await loginAsUser()

    // 1. Owner creates a course (user-side).
    const course = await createCourse(userCtx, {
      title: `E2E Rust ${Date.now()}`,
      intent_md: 'Learn Rust basics in 30 minutes',
    })
    expect(course.status).toBe('draft')

    // 2. Tutor submits a curriculum (agent-side). We mint an
    // agent + Bearer context to call the agent endpoints.
    const { plain_token } = await createAgent(userCtx)
    const agentCtx = await loginAsAgent(plain_token)
    const lessonTitle = 'Hello, Rust'
    await submitCurriculum(agentCtx, course.id, [
      {
        title: 'Module 1',
        position: 0,
        lessons: [{ title: lessonTitle, position: 0 }],
      },
    ])

    // Sanity-check: the lesson is `locked` after the tutor submits.
    let tree = await getCourseTree(userCtx, course.id)
    expect(tree.course.status).toBe('review')
    const lesson = tree.lessons.find((l) => l.title === lessonTitle)
    expect(lesson).toBeDefined()
    expect(lesson!.status).toBe('locked')

    // 3. Owner approves → course becomes active, first lesson
    // opens.
    await approveCourse(userCtx, course.id)
    tree = await getCourseTree(userCtx, course.id)
    expect(tree.course.status).toBe('active')
    const approvedLesson = tree.lessons.find((l) => l.id === lesson!.id)!
    expect(approvedLesson.status).toBe('open')

    // 4. Tutor materialises the lesson body (agent-side).
    const contentMd = '# Hello, Rust\n\nRust is a systems language.'
    await materializeLesson(agentCtx, approvedLesson.id, contentMd)
    tree = await getCourseTree(userCtx, course.id)
    const materialized = tree.lessons.find((l) => l.id === lesson!.id)!
    expect(materialized.content_md).toBe(contentMd)

    // 5. Owner opens the lesson page in the browser.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.goto(`/lessons/${lesson!.id}`)
    await expect(page.getByTestId('lesson-content')).toBeVisible()
    await expect(page.getByTestId('lesson-complete')).toBeVisible()
    // The H1 from the markdown survives the render. We anchor on
    // the article element because the page also has a `<h1>` for
    // the lesson title ("Hello, Rust") — react-markdown renders the
    // markdown `# Hello, Rust` as a second h1 inside the article.
    const article = page.getByTestId('lesson-content')
    await expect(article.locator('h1', { hasText: 'Hello, Rust' })).toBeVisible()

    // 6. Complete the lesson (no quizzes → button is enabled).
    await page.getByTestId('lesson-complete').click()
    // After clicking, the page navigates back to the course.
    await page.waitForURL((u) => u.pathname === `/courses/${course.id}`)
    tree = await getCourseTree(userCtx, course.id)
    expect(tree.progress.lessons_done).toBe(1)
    expect(tree.progress.lessons_total).toBe(1)

    await userCtx.dispose()
    await agentCtx.dispose()
  })
})
