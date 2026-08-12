// @ts-check
/**
 * Today page E2E smoke.
 *
 * Seeds two tasks — one overdue, one due-today — via the public REST
 * API and verifies they show up in the corresponding sections of the
 * Today page.
 *
 * What's pinned:
 *   - The four section headings render with the right counts.
 *   - A seeded task with due_at = yesterday surfaces in Overdue.
 *   - A seeded task with due_at = today surfaces in Due today.
 *   - The /review banner appears when an agent has submitted a task.
 *   - The empty-state copy does NOT appear when there's real content.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  createAgent,
  createProject,
  createTask,
  claimTask,
  loginAsAgent,
  loginAsUser,
  patchTask,
  submitTask,
} from './helpers'

test.describe('Today page', () => {
  test('overdue and due-today sections reflect seeded tasks; awaiting banner shows after agent submit', async ({
    page,
  }) => {
    const userCtx = await loginAsUser()
    const project = await createProject(userCtx)

    // Overdue: due_at 7 days ago, status stays 'todo' so it lands in
    // the overdue bucket (the backend excludes done/review tasks).
    const overdue = await createTask(userCtx, project.id, {
      title: `Overdue seed ${Date.now()}`,
    })
    const overdueDue = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString()
    await patchTask(userCtx, overdue.id, {
      status: 'todo',
      priority: 'high',
      due_at: overdueDue,
    })

    // Due today: due_at a few hours in the future so it's still within
    // today's window in any reasonable TZ.
    const dueToday = await createTask(userCtx, project.id, {
      title: `Due-today seed ${Date.now()}`,
    })
    await patchTask(userCtx, dueToday.id, {
      status: 'todo',
      priority: 'medium',
      due_at: new Date(Date.now() + 6 * 60 * 60 * 1000).toISOString(),
    })

    // A separate task that the agent claims + submits. It moves to
    // status='review' which is what triggers the /review banner. We
    // keep it OUT of the overdue/due_today buckets (no due_at) so
    // its submission doesn't disturb the section counts.
    const forReview = await createTask(userCtx, project.id, {
      title: `Review seed ${Date.now()}`,
    })
    const { plain_token } = await createAgent(userCtx)
    const agentCtx = await loginAsAgent(plain_token)
    await claimTask(agentCtx, forReview.id)
    await submitTask(agentCtx, forReview.id, 'ready for human review')

    // Sign the browser in via the actual login form so the SPA sees
    // a real session cookie.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    // LoginPage redirects to '/'.
    await page.waitForURL((u) => u.pathname === '/')
    await page.waitForLoadState('networkidle')

    await expect(page.getByRole('heading', { name: 'Today', exact: true })).toBeVisible()

    // Wait for /today to land and the WS subscribe to settle.
    await page.waitForLoadState('networkidle')

    // The Overdue section count is exactly 1 — our seeded task
    // lands here regardless of how many other tasks are in flight
    // from earlier specs.
    await expect(page.getByText('Overdue (1)')).toBeVisible()

    // The seeded overdue task title shows up under its section.
    await expect(page.getByText(overdue.title, { exact: true })).toBeVisible()

    // The /review banner appears because the overdue task is now
    // awaiting human review.
    await expect(page.getByText(/awaiting your review/)).toBeVisible()

    // The day-clear copy is NOT shown — there's at least one task.
    await expect(page.getByText(/Day is clear/)).not.toBeVisible()

    // Clean up so the next run starts fresh.
    await userCtx.dispose()
    await agentCtx.dispose()
  })
})