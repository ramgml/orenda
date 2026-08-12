import { expect, test } from '@playwright/test'

/**
 * Auth flow smoke spec — validates the E2E harness end-to-end:
 *
 *   - Unauthenticated visits to protected routes redirect to /login
 *     (RequireAuth gate is honoured).
 *   - Submitting valid credentials lands the user on the authenticated
 *     home (Today) with the session cookie persisted.
 *   - Wrong password surfaces a visible error instead of silently failing.
 *
 * The seeded user (`e2e@orenda.local` / `testpass123`) comes from
 * `global-setup.ts`; tests assume the suite was started via the harness
 * and the binary is already serving on baseURL.
 */
test.describe('Auth flow', () => {
  test('protected route redirects anonymous user to /login', async ({ page }) => {
    await page.goto('/projects')
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByRole('heading', { name: 'Sign in to Orenda' })).toBeVisible()
  })

  test('login with valid credentials lands on authenticated home', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill('testpass123')
    await page.getByRole('button', { name: /sign in/i }).click()

    // LoginPage redirects to '/' when RequireAuth didn't pass `state.from`.
    await expect(page).toHaveURL(/\/$/)
    // TodayPage header — proof the SPA is authenticated and routing works.
    await expect(page.getByRole('heading', { name: 'Today', exact: true })).toBeVisible()
  })

  test('login with wrong password shows visible error', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill('wrong-password')
    await page.getByRole('button', { name: /sign in/i }).click()

    await expect(page.getByText('Invalid email or password.')).toBeVisible()
    // Stays on /login — no implicit redirect on failure.
    await expect(page).toHaveURL(/\/login$/)
  })
})
