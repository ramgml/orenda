// @ts-check
/**
 * Review-queue E2E smoke.
 *
 *   - The /review page shows one row per task awaiting human verdict
 *     (status=review or awaiting=human).
 *   - Inline Accept → POST /tasks/{id}/review with decision=approve
 *     and removes the row.
 *   - Return → window.prompt asks for feedback; if the user cancels,
 *     the task stays in the queue.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  claimTask,
  createAgent,
  createProject,
  createTask,
  loginAsAgent,
  loginAsUser,
  submitTask,
} from './helpers'

test.describe('Review queue', () => {
  test('submitted task lands in /review; Accept removes the row; Return without comment is a no-op', async ({
    page,
  }) => {
    const ctx = await loginAsUser()
    const project = await createProject(ctx)
    const task = await createTask(ctx, project.id, {
      title: `Review smoke ${Date.now()}`,
    })

    // Agent claims + submits — the task moves to status='review'.
    const { plain_token } = await createAgent(ctx)
    const agentCtx = await loginAsAgent(plain_token)
    await claimTask(agentCtx, task.id)
    await submitTask(agentCtx, task.id, 'ready for review')

    // Sign in and visit /review.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')

    await page.goto('/review')
    await page.waitForLoadState('networkidle')

    // The submitted task appears.
    await expect(page.getByText(task.title, { exact: true })).toBeVisible()

    // First click: Return → window.prompt is cancelled (dialog handler
    // dismisses) → the task stays in the queue.
    let promptCount = 0
    page.once('dialog', (d) => {
      promptCount++
      void d.dismiss()
    })

    const row = page.locator('[data-testid="review-row"]').filter({ hasText: task.title })
    await row.getByTestId('review-reject').click()
    await expect(row).toBeVisible()
    expect(promptCount).toBe(1)

    // Now Accept — the row leaves the queue (status=done in DB).
    await row.getByTestId('review-accept').click()

    await expect(page.getByText(task.title, { exact: true })).toBeHidden({ timeout: 5_000 })

    const final = await ctx.get(`/api/v1/tasks/${task.id}`)
    const finalBody = await final.json()
    expect(finalBody.status).toBe('done')

    await ctx.dispose()
    await agentCtx.dispose()
  })
})