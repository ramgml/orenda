// @ts-check
/**
 * Phase 28.2 (polish): Settings hub page.
 *
 * Pre-28.2 the /settings route rendered a `<Placeholder title="Settings" />`
 * — the sidebar ⚙ entry landed the user on an empty page, and the only
 * way to reach Backups / Bots was to type the URL. This spec pins the
 * new hub:
 *   - /settings renders four cards (Backups, Bots, Agents, Reports).
 *   - Clicking Backups navigates to /settings/backups (the original
 *     defect: buttons link to the right place).
 *   - The About block is populated from /api/v1/stats.
 *
 * The "Bot/notification Settings" card uses the existing
 * `/settings/bots` route — Phase 21.3 follow-up (Telegram bind) lives
 * on that page too.
 */
import { expect, test } from '@playwright/test'

import { E2E_PASSWORD, loginAsUser } from './helpers'

test.describe('Settings home (Phase 28.2)', () => {
  test('sidebar → /settings → cards → click Backups lands on /settings/backups', async ({
    page,
  }) => {
    const ctx = await loginAsUser()

    // Sign in and follow the sidebar ⚙ entry.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')

    // Navigate via the sidebar — the real user journey.
    await page.getByRole('link', { name: 'Settings' }).click()
    await page.waitForURL((u) => u.pathname === '/settings')

    // The hub renders all four cards. Clicking Backups is the
    // regression test for the original "placeholder" defect.
    await expect(page.getByTestId('settings-home')).toBeVisible()
    await expect(page.getByTestId('settings-card-backups')).toBeVisible()
    await expect(page.getByTestId('settings-card-bots')).toBeVisible()
    await expect(page.getByTestId('settings-card-agents')).toBeVisible()
    await expect(page.getByTestId('settings-card-reports')).toBeVisible()

    // The hub must NOT be the "Coming in a later phase" placeholder
    // — that's the message the old <Placeholder> rendered, and a
    // direct quote saves a separate DOM-text check that would
    // regress if the placeholder copy ever changed.
    await expect(page.getByText('Coming in a later phase.')).toHaveCount(0)

    // Click Backups — the operator's most common path.
    await page.getByTestId('settings-card-backups').click()
    await page.waitForURL((u) => u.pathname === '/settings/backups')
    await expect(page.getByText(/Backup enabled/)).toBeVisible({ timeout: 10_000 })

    // About block from /api/v1/stats. We don't pin the exact uptime
    // text (it ticks) — only that the four labels resolve.
    await page.goto('/settings')
    await expect(page.getByTestId('settings-about')).toBeVisible()
    await expect(page.getByText('Uptime')).toBeVisible()
    await expect(page.getByText('Database')).toBeVisible()
    await expect(page.getByText('Requests served')).toBeVisible()
    await expect(page.getByText('WS clients')).toBeVisible()

    // Wait for the stats endpoint to settle — the dashboard shows
    // "—" until the first /api/v1/stats response lands. We don't
    // assert a numeric value (it's process-dependent) just that
    // the about-* nodes are populated.
    await expect
      .poll(
        async () => {
          const text = await page.getByTestId('about-db').textContent()
          return text ?? ''
        },
        { timeout: 5_000 },
      )
      .not.toBe('—')

    await ctx.dispose()
  })
})
