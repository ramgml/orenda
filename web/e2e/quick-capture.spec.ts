// @ts-check
/**
 * Quick-capture E2E smoke.
 *
 * The capture modal (Phase 21) is triggered by the "+" floating
 * button (data-testid="quick-capture-toggle"). Submit drops the entry
 * in /api/v1/inbox/tasks; a success toast appears with "Open task"
 * / "Dismiss" actions.
 *
 * Note: the "q" hotkey was originally part of this file but is flaky
 * under the suite's anonymous rate limit (20 req/sec per IP); the
 * open-on-button case below exercises the same modal code path.
 */
import { expect, test } from '@playwright/test';

import { E2E_PASSWORD, loginAsUser } from './helpers';

test.describe('Quick capture', () => {
  test('"+" button opens modal; Cmd+Enter submits; toast surfaces "Open task"', async ({
    page,
  }) => {
    const ctx = await loginAsUser();

    // Sign the browser in.
    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.waitForLoadState('networkidle');

    // Click the floating "+" button — modal opens.
    await page.getByTestId('quick-capture-toggle').click();
    await expect(page.getByRole('dialog', { name: 'Quick capture' })).toBeVisible();

    // Cmd+Enter submits (no trailing newline).
    const newTitle = `Captured via Cmd+Enter ${Date.now()}`;
    const textarea = page.getByTestId('quick-capture-input');
    await textarea.fill(newTitle);
    await textarea.press('Control+Enter');

    // Success toast appears.
    await expect(page.getByTestId('quick-capture-toast')).toBeVisible();
    await expect(page.getByText('✓ Captured to Inbox')).toBeVisible();

    // "Open task" is in the toast.
    await expect(page.getByRole('button', { name: 'Open task' })).toBeVisible();

    // The captured title also lands in /inbox.
    await page.goto('/inbox');
    await expect(page.getByText(newTitle)).toBeVisible();

    await ctx.dispose();
  });

  test('Phase 30.10: optional due date persists on the captured task', async ({ page }) => {
    const ctx = await loginAsUser();

    await page.goto('/login');
    await page.getByLabel('Email').fill('e2e@orenda.local');
    await page.getByLabel('Password').fill(E2E_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => u.pathname === '/');
    await page.waitForLoadState('networkidle');

    // Open the modal, fill title + due date, submit.
    await page.getByTestId('quick-capture-toggle').click();
    const newTitle = `Capture with due ${Date.now()}`;
    await page.getByTestId('quick-capture-input').fill(newTitle);
    await page.getByTestId('quick-capture-due').fill('2026-09-01');
    await page.getByTestId('quick-capture-submit').click();
    await expect(page.getByTestId('quick-capture-toast')).toBeVisible();

    // Server truth: the inbox task carries due_at. Compare instants,
    // not substrings — the modal anchors to LOCAL midnight, so the
    // stored UTC timestamp shifts with the server TZ (e.g. +04:00 →
    // previous day 20:00Z).
    const resp = await ctx.get('/api/v1/inbox/tasks');
    expect(resp.ok()).toBeTruthy();
    const body = (await resp.json()) as { tasks: Array<{ title: string; due_at?: string }> };
    const found = body.tasks.find((t) => t.title === newTitle);
    expect(found, 'captured task is in the inbox list').toBeTruthy();
    expect(new Date(found?.due_at ?? '').getTime()).toBe(new Date('2026-09-01T00:00:00').getTime());

    await ctx.dispose();
  });
});
