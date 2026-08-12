// @ts-check
/**
 * WebSocket live-update E2E smoke (Phase 27.2).
 *
 * Validates the FULL realtime pipeline:
 *
 *   1. Sign in via /login → cookie orenda_session is set.
 *   2. AppLayout mounts the WS connection hook; the browser opens
 *      /api/v1/ws with the cookie. Playwright's `page.on('websocket')`
 *      listener (registered BEFORE login) captures the upgrade.
 *   3. From the API: create + claim + submit a task — server publishes
 *      a task.submitted event on the "tasks" topic; the open WS pushes
 *      it down.
 *   4. /today's awaiting banner updates WITHOUT `page.reload()`. This
 *      is the assertion that proves WS works end-to-end through the
 *      cookie auth path, not via a manual refresh.
 *
 * If WS or cookie auth is broken, the banner never changes and the
 * test times out.
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

test.describe('WebSocket live update (cookie auth, Phase 27.2)', () => {
  test('after agent submit, /today awaiting banner updates via real WS push', async ({
    page,
  }) => {
    const userCtx = await loginAsUser()
    const project = await createProject(userCtx)

    // Listen for the WS frame BEFORE we navigate. Playwright captures
    // every WebSocket the page opens — even ones that open between
    // navigation and our subscription.
    const wsPromise = page.waitForEvent('websocket', { timeout: 15_000 })

    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.waitForLoadState('networkidle')

    const ws = await wsPromise
    expect(ws.url()).toContain('/api/v1/ws')

    // Hang the WS open until the agent submits: prove it actually
    // carries the "tasks" topic frame without a refresh.
    const wsFramePromise = ws.waitForEvent('framereceived', { timeout: 15_000 })

    // Snapshot the awaiting-banner text so we can wait for a change
    // rather than racing against previous-spec state.
    const bannerBefore =
      (await page
        .getByText(/awaiting your review/)
        .first()
        .textContent()
        .catch(() => '')) ?? ''

    // Trigger an event through the agent path: create + claim + submit.
    const task = await createTask(userCtx, project.id, {
      title: `Live update ${Date.now()}`,
    })
    const { plain_token } = await createAgent(userCtx)
    const agentCtx = await loginAsAgent(plain_token)
    await claimTask(agentCtx, task.id)
    await submitTask(agentCtx, task.id, 'live')

    // The WS frame MUST arrive within the timeout — no page.reload()
    // involved. This is the heartbeat of the realtime pipeline.
    const frame = await wsFramePromise
    // Frame is a { payload: string | Buffer, ... } shape; coerce to string.
    const frameText =
      typeof frame.payload === 'string'
        ? frame.payload
        : Buffer.isBuffer(frame.payload)
          ? frame.payload.toString('utf8')
          : String(frame.payload)
    expect(frameText).toContain('tasks')

    // /today's banner reflects the new task WITHOUT reload.
    await expect(page.getByText(/awaiting your review/)).not.toHaveText(
      bannerBefore,
      { timeout: 10_000 },
    )

    await userCtx.dispose()
    await agentCtx.dispose()
  })
})
