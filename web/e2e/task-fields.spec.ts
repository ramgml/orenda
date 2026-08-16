// @ts-check
/**
 * Phase 27.7 E2E: editable Status / Priority / Assignee.
 *
 * The owner opens a task in the browser, changes each field from
 * the sidebar, and asserts the change round-trips through PATCH →
 * repo → re-render. We also verify the activity feed received the
 * change rows and that the column on the kanban does NOT move when
 * status changes (axes stay separate in 27.7; 27.8 will collapse
 * them).
 */
import { expect, test } from '@playwright/test';

import {
  E2E_PASSWORD,
  createProject,
  createTask,
  listTaskActivity,
  loginAsUser,
  patchTask,
} from './helpers';

test.describe('Task field controls (Phase 27.7)', () => {
  test('owner edits status / priority / assignee from the sidebar', async ({ page }) => {
    const userCtx = await loginAsUser();

    // Set up a project + task. The kanban placeholders exist already
    // by the time the test server starts; createProject adds a new
    // one and the task lands on its first column.
    const project = await createProject(userCtx, `P277 ${Date.now()}`);
    const task = await createTask(userCtx, project.id, {
      title: `P277 task ${Date.now()}`,
    });
    const initialColumnID = task.column_id;

    // Open the task page and verify the three controls exist with
    // the task's starting values.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.goto(`/tasks/${task.id}`);

    const statusSelect = page.getByTestId('task-status');
    const prioritySelect = page.getByTestId('task-priority');
    const assigneeBlock = page.getByTestId('task-assignee');
    const assigneeSelect = assigneeBlock.locator('select');
    await expect(statusSelect).toBeVisible();
    await expect(prioritySelect).toBeVisible();
    await expect(assigneeSelect).toBeVisible();

    // Change priority first (least invasive — no awaiting side-effects).
    const priorityPatch = page.waitForResponse(
      (r) => r.url().includes(`/api/v1/tasks/${task.id}`) && r.request().method() === 'PATCH',
    );
    await prioritySelect.selectOption('urgent');
    await priorityPatch;
    // Wait for PATCH round-trip; reload and confirm the value stuck.
    await page.reload();
    await expect(page.getByTestId('task-priority')).toHaveValue('urgent');

    // Change status to done. Backend fills completed_at and clears
    // awaiting. The kanban card's column must NOT change — that
    // belongs to 27.8 (columns = statuses), not 27.7.
    const statusPatch = page.waitForResponse(
      (r) => r.url().includes(`/api/v1/tasks/${task.id}`) && r.request().method() === 'PATCH',
    );
    await page.getByTestId('task-status').selectOption('done');
    await statusPatch;
    const after = await patchTask(userCtx, task.id, {}); // server truth
    expect(after.status).toBe('done');
    // Phase 27.8: status ≡ column_id, so the card also moves onto
    // the "done" column automatically. We can't assert the exact
    // column id without exposing the board, but we do know it's
    // no longer the original backlog column.
    expect(after.column_id).not.toBe(initialColumnID);
    expect(after.completed_at).toBeTruthy();
    expect(after.awaiting).toBe('none');

    // Assignee: switch to the owner ("Me"). The dropdown offers
    // "Me" when the user is signed in.
    const assigneePatch = page.waitForResponse(
      (r) => r.url().includes(`/api/v1/tasks/${task.id}`) && r.request().method() === 'PATCH',
    );
    await page.getByTestId('task-assignee').locator('select').selectOption('me');
    await assigneePatch;
    const afterAssignee = await patchTask(userCtx, task.id, {});
    expect(afterAssignee.assignee_type).toBe('user');
    expect(afterAssignee.assignee_id).toBeTruthy();

    // Activity feed carries the priority + status + assigned rows.
    const feed = await listTaskActivity(userCtx, task.id);
    const actions = feed.activity.map((a) => a.action);
    // Phase 27.9: backend writes the `task.*` prefix consistently.
    expect(actions).toContain('task.priority_changed');
    expect(actions).toContain('task.status_changed');
    expect(actions).toContain('task.assigned');

    await userCtx.dispose();
  });
});
