// @ts-check
/**
 * Phase 28.1 polish.1 + Phase 28.9 hot-reload: editable backup
 * settings UI.
 *
 * Pre-28.1 the Settings → Backups panel was a `<dl>` — operators
 * had to ssh to the host and edit data/config.yaml to add a
 * remote. This spec pins the editable flow end-to-end:
 *   - GET /api/v1/backups/settings returns the merged state.
 *   - PUT 200 (no longer 501) with the form's body.
 *   - The page reload reads the persisted state back.
 *
 * Phase 28.9 changes the contract: PUT no longer requires a
 * process restart for the new remote to take effect — the test
 * asserts the absence of the historical "restart to apply" hint,
 * pinning the new hot-reload behaviour.
 */
import { expect, test } from '@playwright/test'

import { E2E_PASSWORD, loginAsUser } from './helpers'

test.describe('Backups settings (Phase 28.1 + 28.9 hot-reload)', () => {
  test('PUT saves settings; reload reflects them; no restart needed', async ({
    page,
    request,
  }) => {
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

    // Phase 28.9: there is no longer a restart banner — settings
    // are hot-reloaded. The legacy `settings-restart-hint`
    // element is removed from the page; asserting absence pins
    // the contract so a future regression that re-adds the
    // restart dependency is caught.
    await expect(page.getByTestId('settings-restart-hint')).toHaveCount(0)

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
    // Phase 28.9: source_hint is no longer emitted — the live
    // service mirrors the DB rows by the time GET returns.
    // Kept in the response shape for backwards compat but
    // empty (tested as undefined-equivalent).
    expect(got.source_hint ?? '').toBe('')
    // The auth field is never returned — only `has_auth`.
    expect(got.remote_auth).toBeUndefined()
    expect(got.has_auth).toBe(true)

    await ctx.dispose()
  })
})
