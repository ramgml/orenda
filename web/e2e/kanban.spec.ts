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
import { expect, test } from '@playwright/test';

import {
  E2E_PASSWORD,
  createProject,
  createTag,
  createTask,
  listColumns,
  loginAsUser,
  setTaskTags,
} from './helpers';

test.describe('Kanban', () => {
  test('creating a task posts the API and the card appears in the column', async ({ page }) => {
    const ctx = await loginAsUser();
    const project = await createProject(ctx);
    const cols = await listColumns(ctx, project.id);
    expect(cols.length).toBeGreaterThanOrEqual(3);

    // Sign the browser in.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');

    // Navigate to the project's kanban tab.
    await page.goto(`/projects/${project.id}`);

    // Use the API to seed a task — covers the create path cheaply.
    // (The "+ New task" inline form is the same POST endpoint; we
    // exercise it through the API to keep this spec deterministic.)
    const title = `Kanban seed ${Date.now()}`;
    const task = await createTask(ctx, project.id, { title });

    // Reload so the new task shows up.
    await page.reload();
    await page.waitForLoadState('networkidle');
    // The board renders a "Loading…" placeholder until /board
    // resolves; wait for it to leave before checking for the task.
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });
    await expect(page.getByText(title, { exact: true })).toBeVisible();

    // Move it to the second column via the move endpoint.
    const target = cols[1];
    const moveResp = await ctx.post(`/api/v1/tasks/${task.id}/move`, {
      data: { column_id: target.id, position: 1024 },
    });
    expect(moveResp.status()).toBe(200);

    await page.reload();
    // After reload, the card sits in the second column — assert the
    // column id is now reflected in the API.
    const movedResp = await ctx.get(`/api/v1/tasks/${task.id}`);
    const moved = await movedResp.json();
    expect(moved.column_id).toBe(target.id);

    await ctx.dispose();
  });

  test('Phase 27.3: kanban card renders coloured tag chips from list-payload', async ({ page }) => {
    const ctx = await loginAsUser();
    const project = await createProject(ctx);

    // Sign the browser in and open the project board.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.goto(`/projects/${project.id}`);
    await page.waitForLoadState('networkidle');

    // Seed two tags + a tagged task through the REST surface. The
    // assertions below prove ListByProjectWithStats populates task.tags
    // (without this, the chip would render with the empty-state text).
    const tagBug = await createTag(ctx, { name: `bug-${Date.now()}`, color: '#dc2626' });
    const tagUrgent = await createTag(ctx, { name: `urgent-${Date.now()}`, color: '#f59e0b' });
    const task = await createTask(ctx, project.id, {
      title: `Tagged card ${Date.now()}`,
    });
    await setTaskTags(ctx, task.id, [tagBug.id, tagUrgent.id]);

    // Reload so the board pulls the enriched payload.
    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });

    // Both chips must be visible — the chip's title attribute is the
    // tag name, so we anchor on that to distinguish it from any
    // title text that happens to share the substring.
    await expect(page.getByTitle(tagBug.name)).toBeVisible();
    await expect(page.getByTitle(tagUrgent.name)).toBeVisible();

    await ctx.dispose();
  });

  // Phase 27.10: column colour survives a round-trip through
  // the EditColumnModal. Pre-27.10 the modal hardcoded '#94a3b8',
  // so any Save action (even just renaming the column) clobbered
  // the saved colour with the slate default. The dot in the
  // column header is the visible contract; the dot's
  // data-column-color attribute reflects the saved value.
  test('Phase 27.10: column colour persists across rename + reload', async ({ page }) => {
    const ctx = await loginAsUser();
    const project = await createProject(ctx);
    const cols = await listColumns(ctx, project.id);
    const target = cols[1]; // pick any non-default column

    // Sign in and open the project board.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.goto(`/projects/${project.id}`);
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });

    // Phase 27.10 contract 1: the dot in the column header
    // reflects whatever the server returned. The seeded columns
    // have no colour, so the dot is the slate fallback
    // (data-column-color="" → style rgb(148, 163, 184)).
    const dot = page.getByTestId('column-color-dot').nth(1);
    await expect(dot).toHaveAttribute('data-column-color', '');

    // Phase 27.10 contract 2: pick a colour through the EditColumnModal.
    // We can't drive the colour picker reliably in headless Chromium,
    // so we seed the colour via the API and assert the dot reflects it
    // after reload. (The modal pipeline is covered by the vitest
    // suite — ColumnView.test.tsx asserts on the React state.)
    const newColour = '#22c55e';
    const patchResp = await ctx.patch(`/api/v1/columns/${target.id}`, {
      data: { color: newColour },
    });
    expect(patchResp.status()).toBe(200);

    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });

    const recoloured = page.getByTestId('column-color-dot').nth(1);
    await expect(recoloured).toHaveAttribute('data-column-color', newColour);
    // The dot's background is set via inline style; verify the rgb
    // tuple matches the chosen hex.
    await expect(recoloured).toHaveCSS('background-color', 'rgb(34, 197, 94)');

    // Phase 27.10 contract 3: rename must NOT wipe the colour. The
    // bug was that the EditColumnModal posted `color` on every save
    // (because state initialised to '#94a3b8'), clobbering the
    // saved value. The fix posts `color` only when the picker was
    // actually touched.
    //
    // We simulate "user opened the modal, typed a new name, hit Save
    // without touching the colour picker" by PATCHing only the
    // name (no `color` field). The server's PATCH semantics ignore
    // missing fields, so the colour survives.
    const renameResp = await ctx.patch(`/api/v1/columns/${target.id}`, {
      data: { name: 'In progress renamed' },
    });
    expect(renameResp.status()).toBe(200);

    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });

    // The renamed header is visible…
    await expect(page.getByText('In progress renamed')).toBeVisible();
    // …and the dot still carries the colour.
    const stillRecoloured = page.getByTestId('column-color-dot').nth(1);
    await expect(stillRecoloured).toHaveAttribute('data-column-color', newColour);
    await expect(stillRecoloured).toHaveCSS('background-color', 'rgb(34, 197, 94)');

    await ctx.dispose();
  });

  // Phase 27.8.4: drag-and-drop changes the column, and the task's
  // status follows. Pre-27.8.4 the backend's Service.Move updated
  // `column_id` but left `status` on the old value — the invariant
  // `task.status ≡ column.status` was broken on every drag. This
  // spec exercises the move endpoint (dnd-kit pointer sequences are
  // unreliable in headless Chromium; the API path is the same one
  // the drag handler uses) and asserts both fields move together.
  test('Phase 27.8.4: moving a task to the done column flips status to done', async ({ page }) => {
    const ctx = await loginAsUser();
    const project = await createProject(ctx);
    const cols = await listColumns(ctx, project.id);
    // Find the todo and done columns by their canonical status keys
    // (the default columns are seeded with `name == status`).
    const todoCol = cols.find((c) => c.status === 'todo') ?? cols[0];
    const doneCol = cols.find((c) => c.status === 'done') ?? cols[cols.length - 1];
    expect(todoCol.id).not.toBe(doneCol.id);

    // Sign in and open the board.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.goto(`/projects/${project.id}`);
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Loading…')).toHaveCount(0, { timeout: 10_000 });

    // Seed a task in the todo column with status="todo".
    const task = await createTask(ctx, project.id, {
      title: `Drag me ${Date.now()}`,
    });
    // Re-seat the task on the todo column explicitly — createTask
    // may pick the first column by default. This is the same wire
    // shape the DnD handler uses.
    const seatResp = await ctx.post(`/api/v1/tasks/${task.id}/move`, {
      data: { column_id: todoCol.id, position: 1024 },
    });
    expect(seatResp.status()).toBe(200);

    // Open the task to drive the Status select path (and so reload
    // after the drag can re-fetch via the same fixture).
    await page.goto(`/tasks/${task.id}`);
    await page.waitForLoadState('networkidle');
    // The select must have loaded the board's columns (Phase 27.8.4).
    const status = page.getByTestId('task-status');
    await expect(status).toContainText('todo', { timeout: 10_000 });

    // Drag (simulated via API, since the dnd-kit pointer sequence
    // is unreliable in jsdom/Chromium — see top-of-file comment):
    // POST /tasks/:id/move with the done column id.
    const moveResp = await ctx.post(`/api/v1/tasks/${task.id}/move`, {
      data: { column_id: doneCol.id, position: 1024 },
    });
    expect(moveResp.status()).toBe(200);

    // The card moved AND the status flipped. Read both back through
    // the REST surface — the move handler updates the row in-place.
    const after = await (await ctx.get(`/api/v1/tasks/${task.id}`)).json();
    expect(after.column_id).toBe(doneCol.id);
    expect(after.status).toBe('done', 'Phase 27.8.4: status follows the column on Move');

    // Visible contract: the Status select in the sidebar reads "done"
    // after the drag (the sidebar re-fetches on focus / WS event).
    // A reload is the deterministic way to drive the read.
    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(status).toContainText('done', { timeout: 10_000 });

    await ctx.dispose();
  });
});
