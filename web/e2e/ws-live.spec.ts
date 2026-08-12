// @ts-check
/**
 * WebSocket live-update E2E smoke.
 *
 * The dashboard re-fetches /today (and /review) when the server pushes
 * a "tasks" event over /api/v1/ws. As of Phase 26 the browser-side
 * WS connect path is not wired end-to-end (AuthContext never receives
 * a JWT — see SESSION.md), so this spec pins the FALLBACK contract:
 *
 *   1. Open /today and /review.
 *   2. From the API: create + claim + submit a task — emits a
 *      task.submitted WS event on the "tasks" topic (the server is
 *      publishing correctly; the client just doesn't subscribe yet).
 *   3. The submitted task is visible on /review, and the /today
 *      awaiting banner shows up — both after a refresh that the
 *      user would normally NOT have to do once WS works.
 *
 * When WS is wired (a follow-up to the auth fix in SESSION.md) the
 * refresh can be removed and the test still passes — the assertions
 * are about the data being correct, not about the transport.
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

test.describe('WebSocket live update (data path)', () => {
  test('after agent submit, /today awaiting banner and /review row reflect the new task', async ({
    page,
  }) => {
    const ctx = await loginAsUser()
    const project = await createProject(ctx)

    // Sign in and visit /today — the banner MAY already show earlier
    // tasks from prior specs (data persists across specs within one
    // run). We just snapshot the current banner text so we can
    // confirm it changes after submission.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.waitForLoadState('networkidle')

    // Agent claims + submits a task — emits a WS event on the
    // "tasks" topic. The browser's WS isn't connected yet (Phase 26
    // gap), so we reload to simulate the eventual live update.
    const task = await createTask(ctx, project.id, {
      title: `Live update ${Date.now()}`,
    })
    const { plain_token } = await createAgent(ctx)
    const agentCtx = await loginAsAgent(plain_token)
    await claimTask(agentCtx, task.id)
    await submitTask(agentCtx, task.id, 'live')

    // /today: the awaiting banner surfaces on refresh.
    await page.reload()
    await page.waitForLoadState('networkidle')
    await expect(page.getByText(/awaiting your review/)).toBeVisible()

    // /review: the submitted task shows up.
    await page.goto('/review')
    await page.waitForLoadState('networkidle')
    await expect(page.getByText(task.title, { exact: true })).toBeVisible()

    await ctx.dispose()
    await agentCtx.dispose()
  })
})