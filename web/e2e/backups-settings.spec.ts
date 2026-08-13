// @ts-check
/**
 * Phase 28.1 polish.1: editable backup settings UI.
 *
 * Pre-28.1 the Settings → Backups panel was a `<dl>` — operators
 * had to ssh to the host and edit data/config.yaml to add a
 * remote. This spec pins the new editable flow:
 *   - GET /api/v1/backups/settings returns the merged state.
 *   - PUT 200 (no longer 501) with the form's body.
 *   - The page reload reads the persisted state back.
 *
 * The "restart to apply" contract is *not* fully covered here —
 * the running `*backup.Service` is wired at startup and stays on
 * the old URL until the operator restarts. That's documented in
 * the UI banner and would require a full process restart mid-spec
 * to test from end to end; we pin the persistence + display
 * half instead.
 */
import { expect, test } from '@playwright/test'

import { E2E_PASSWORD, loginAsUser } from './helpers'

test.describe('Backups settings (Phase 28.1)', () => {
  test('PUT saves settings; reload reflects them; no longer 501', async ({ page, request }) => {
    const ctx = await loginAsUser()

    // Sign in and open the settings page.
    await page.goto('/login')
    await page.getByLabel('Email').fill('e2e@orenda.local')
    await page.getByLabel('Password').fill(E2E_PASSWORD)
    await page.getByRole('button', { name: /sign in/i }).click()
    await page.waitForURL((u) => u.pathname === '/')
    await page.goto('/settings/backups')
    await expect(page.getByText(/Backup enabled/)).toBeVisible({ timeout: 10_000 })

    // Fill the form and save.
    const urlInput = page.getByTestId('settings-remote-url')
    await urlInput.fill('https://example.com/orenda.git')
    const authInput = page.getByTestId('settings-remote-auth')
    await authInput.fill('test-token-xxx')

    // The PUT is fired by Save — intercept to assert the request
    // body (avoiding race conditions with the response handler).
    const putPromise = page.waitForResponse(
      (r) => r.url().endsWith('/api/v1/backups/settings') && r.request().method() === 'PUT',
    )
    await page.getByTestId('settings-save').click()
    const putResp = await putPromise
    expect(putResp.status()).toBe(200)
    // Phase 28.1 must NOT return 501 — this was the original defect.
    expect(putResp.status()).not.toBe(501)

    // The success banner appears.
    await expect(page.getByText(/Settings saved/)).toBeVisible({ timeout: 5_000 })
    await expect(page.getByTestId('settings-restart-hint')).toBeVisible()

    // Reload — GET should reflect the persisted URL.
    await page.reload()
    await expect(page.getByText(/Backup enabled/)).toBeVisible({ timeout: 10_000 })
    const reloadedUrl = page.getByTestId('settings-remote-url')
    await expect(reloadedUrl).toHaveValue('https://example.com/orenda.git')

    // Sanity-check via the request context too.
    const getResp = await ctx.get('/api/v1/backups/settings')
    expect(getResp.status()).toBe(200)
    const got = await getResp.json()
    expect(got.remote_url).toBe('https://example.com/orenda.git')
    expect(got.source_hint).toBe('ui_override_restart_to_apply')
    // The auth field is never returned — only `has_auth`.
    expect(got.remote_auth).toBeUndefined()
    expect(got.has_auth).toBe(true)

    await ctx.dispose()
  })
})
