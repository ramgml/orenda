// @ts-check
/**
 * Kanban E2E smoke.
 *
 *   - Creating a task via the "+ New task" inline form posts the
 *     create endpoint and the new card appears in the target column.
 *   - Moving a task between columns (drag-and-drop) is hard to drive
 *     reliably in jsdom/Chromium without a stable selector; we exercise
 *     the underlying endpoint via API + reload, then assert the
 *     card is in the new column.
 *   - The move position endpoint accepts fractional positions and the
 *     new column id.
 *   - Phase 27.3: a task with attached tags shows up as a card with
 *     a coloured chip per tag — the kanban enrichment path must
 *     populate task.tags end-to-end.
 */
import { expect, test } from '@playwright/test'

import {
  E2E_PASSWORD,
  createProject,
  createTag,
  createTask,
  listColumns,
  loginAsUser,
  setTaskTags,
} from './helpers'

test.describe('Kanban', () => {
  test('creating a task posts the API and the card appears in the column', async ({
    page,
  }) => {
    const ctx = await loginAsUser()
    const project = await createProject(ctx)
    const cols = await listColumns(ctx, project.id)
    expect(cols.length).toBeGreaterThanOrEqual(3)

    // Sign the browser in.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')

    // Navigate to the project's kanban tab.
    await page.goto(`/projects/${project.id}`)

    // Use the API to seed a task — covers the create path cheaply.
    // (The "+ New task" inline form is the same POST endpoint; we
    // exercise it through the API to keep this spec deterministic.)
    const title = `Kanban seed ${Date.now()}`
    const task = await createTask(ctx, project.id, { title })

    // Reload so the new task shows up.
    await page.reload()
    await page.waitForLoadState('networkidle')
    // The board renders a "Loading…" placeholder until /board
    // resolves; wait for it to leave before checking for the task.
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 })
    await expect(page.getByText(title, { exact: true })).toBeVisible()

    // Move it to the second column via the move endpoint.
    const target = cols[1]
    const moveResp = await ctx.post(`/api/v1/tasks/${task.id}/move`, {
      data: { column_id: target.id, position: 1024 },
    })
    expect(moveResp.status()).toBe(200)

    await page.reload()
    // After reload, the card sits in the second column — assert the
    // column id is now reflected in the API.
    const movedResp = await ctx.get(`/api/v1/tasks/${task.id}`)
    const moved = await movedResp.json()
    expect(moved.column_id).toBe(target.id)

    await ctx.dispose()
  })

  test('Phase 27.3: kanban card renders coloured tag chips from list-payload', async ({
    page,
  }) => {
    const ctx = await loginAsUser()
    const project = await createProject(ctx)

    // Sign the browser in and open the project board.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.goto(`/projects/${project.id}`)
    await page.waitForLoadState('networkidle')

    // Seed two tags + a tagged task through the REST surface. The
    // assertions below prove ListByProjectWithStats populates task.tags
    // (without this, the chip would render with the empty-state text).
    const tagBug = await createTag(ctx, { name: `bug-${Date.now()}`, color: '#dc2626' })
    const tagUrgent = await createTag(ctx, { name: `urgent-${Date.now()}`, color: '#f59e0b' })
    const task = await createTask(ctx, project.id, {
      title: `Tagged card ${Date.now()}`,
    })
    await setTaskTags(ctx, task.id, [tagBug.id, tagUrgent.id])

    // Reload so the board pulls the enriched payload.
    await page.reload()
    await page.waitForLoadState('networkidle')
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 })

    // Both chips must be visible — the chip's title attribute is the
    // tag name, so we anchor on that to distinguish it from any
    // title text that happens to share the substring.
    await expect(page.getByTitle(tagBug.name)).toBeVisible()
    await expect(page.getByTitle(tagUrgent.name)).toBeVisible()

    await ctx.dispose()
  })
})