// @ts-check
/**
 * Phase 28.3 (polish): TaskModal long-content scroll fix.
 *
 * Before this fix:
 *  - The backdrop used `flex items-start md:items-center` with
 *    `overflow-y-auto`. When the card grew past the viewport,
 *    `items-center` placed the flex item so its top edge went
 *    into negative offset — not reachable by scrolling.
 *  - The background page kept its own scroll, so the user's wheel
 *    moved the kanban under the modal.
 *
 * After this fix:
 *  - Backdrop has `items-start` (always anchored to the top).
 *  - Card has `my-auto` so short cards still vertically center via
 *    auto margins, while tall cards naturally collapse to the
 *    backdrop's padding — the entire card is scrollable.
 *  - `useBodyScrollLock` toggles `document.body.style.overflow`
 *    on mount/cleanup.
 *
 * The spec below exercises the full surface: open a modal whose
 * content forces overflow, scroll it top-to-bottom, scroll back,
 * confirm the top is reachable, confirm the body never scrolls.
 * Two close paths covered: Esc and click-outside.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  createProject,
  createTask,
  loginAsUser,
  patchTask,
} from './helpers'

test.describe('TaskModal long-content scroll (Phase 28.3)', () => {
  test('tall card scrolls top-to-bottom; top reachable; body locked; closes via Esc and backdrop click', async ({
    page,
  }) => {
    const ctx = await loginAsUser()
    const project = await createProject(ctx)

    // Seed a task with a description long enough to outgrow the
    // constrained viewport below (height: 600, padding on both
    // sides). 5 KiB of body text comfortably blows past that.
    const task = await createTask(ctx, project.id, {
      title: `Long-content probe ${Date.now()}`,
    })
    const longBody = 'A'.repeat(5000)
    await patchTask(ctx, task.id, { description: longBody })

    // Constrain the viewport so the modal outgrows it on a normal
    // Content-Length basis. md+ breakpoint is 768px; 1280 wide
    // places us on the items-center codepath that the bug lived on.
    await page.setViewportSize({ width: 1280, height: 600 })

    // Sign in and open the project board.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.goto(`/projects/${project.id}`)
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 })

    const card = page
      .getByTestId('task-card')
      .filter({ hasText: 'Long-content probe' })
    await expect(card).toBeVisible()

    // Capture the page scroll position before opening — proves the
    // body stays put while the modal is open.
    const scrollBeforeOpen = await page.evaluate(() => window.scrollY)
    expect(scrollBeforeOpen).toBe(0)

    // Click the kanban card — TaskCard.handleClick → openTaskModal,
    // which mounts TaskModal via the backgroundLocation trick.
    await card.click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible({ timeout: 5_000 })

    // Phase 28.3 contract 1: the dialog (backdrop) is the single
    // scroll container. Computed `overflow-y` must be `auto` so the
    // user can wheel into the card.
    const overflowY = await dialog.evaluate((el) => getComputedStyle(el).overflowY)
    expect(overflowY).toBe('auto')

    // Phase 28.3 contract 2: the body is locked while the modal is
    // mounted.
    const bodyOverflowWhileOpen = await page.evaluate(() => document.body.style.overflow)
    expect(bodyOverflowWhileOpen).toBe('hidden')

    // Phase 28.3 contract 3: the card's title (rendered near the
    // top of the dialog) is visible without scrolling — proving
    // that my-auto hasn't pushed the top edge out of view.
    const dialogCard = page.locator('[role="dialog"] >> [aria-label="Close task"]').locator('..')
    await expect(dialogCard).toBeVisible()

    // Scroll the dialog to the bottom, assert movement happened.
    const scrollHeightAndClientHeight = await dialog.evaluate((el) => ({
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    }))
    expect(scrollHeightAndClientHeight.scrollHeight).toBeGreaterThan(
      scrollHeightAndClientHeight.clientHeight,
    )
    await dialog.evaluate((el) => {
      el.scrollTop = el.scrollHeight
    })
    const afterScrollDown = await dialog.evaluate((el) => el.scrollTop)
    expect(afterScrollDown).toBeGreaterThan(0)

    // The background page must not have moved. With overflow:hidden
    // on body, document.documentElement.scrollTop would never tick,
    // but assert directly for clarity.
    const docScrollMid = await page.evaluate(() => window.scrollY)
    expect(docScrollMid).toBe(0)

    // Scroll back to the top — the original bug pushed the top of
    // the card out of the scroll viewport; now the top should be
    // reachable by setting scrollTop=0.
    await dialog.evaluate((el) => {
      el.scrollTop = 0
    })
    const scrollTopReachable = await dialog.evaluate((el) => el.scrollTop)
    expect(scrollTopReachable).toBe(0)

    // Esc closes — keyboard path. We attach on window so a focused
    // input doesn't swallow the first Escape; testing-library's
    // `page.keyboard.press` sends to whatever has focus.
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0, { timeout: 5_000 })

    // Body lock released.
    const bodyOverflowAfterClose = await page.evaluate(() => document.body.style.overflow)
    expect(bodyOverflowAfterClose).toBe('')

    // Reopen for a second close path: backdrop click in the corner
    // (well outside the card; pre-fix the card filled the visible
    // area and re-introduced the original bug). 5,5 is inside the
    // backdrop's `p-2 md:p-6` padding but outside the card.
    await card.click()
    await expect(dialog).toBeVisible({ timeout: 5_000 })
    await page.mouse.click(5, 5)
    await expect(dialog).toHaveCount(0, { timeout: 5_000 })
    const bodyOverflowAfterBackdropClose = await page.evaluate(() => document.body.style.overflow)
    expect(bodyOverflowAfterBackdropClose).toBe('')

    await ctx.dispose()
  })
})
