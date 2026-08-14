import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config for the Orenda web SPA E2E suite.
 *
 * Scope (Phase 26.A — scaffold + first smoke spec):
 *   - Chromium only (local-first; browser matrix is overkill)
 *   - One worker so the embedded server runs once and the DB is fresh per run
 *   - `webServer.command` boots ./bin/orenda serve via a wrapper script that
 *     also performs one-time DB setup (clean tmp dir, migrate up, seed user).
 *     Playwright runs `webServer` BEFORE any globalSetup, so any DB prep
 *     that the server depends on MUST live in the same command line.
 *   - Reusing an already-running server is forbidden: the suite needs a
 *     clean DB and we don't want to clobber either the usage instance
 *     on :2137 or the dev instance on :2138 (Phase 28.20).
 *
 * The `webServer.url` readiness probe is /healthz (Phase 0 endpoint).
 *
 * All paths are deterministic under /tmp/orenda-e2e so the config string
 * stays the same on every run.
 */
const E2E_DIR = '/tmp/orenda-e2e';
const DATA_DIR = `${E2E_DIR}/data`;
const DB_PATH = `${DATA_DIR}/orenda.db`;
const PORT = 21371;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 1,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',

  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'on-first-retry',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  expect: {
    timeout: 5_000,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: {
    command: `./e2e-setup/run-server.sh`,
    url: `http://127.0.0.1:${PORT}/healthz`,
    timeout: 30_000,
    reuseExistingServer: false,
    env: {
      ORENDA_SERVER__PORT: String(PORT),
      ORENDA_STORAGE__DATA_DIR: DATA_DIR,
      ORENDA_STORAGE__DB_PATH: DB_PATH,
      ORENDA_AUTH__COOKIE_SECURE: 'false',
      ORENDA_LOGGING__LEVEL: 'warn',
      // Phase 26.E: bump rate limits so the suite doesn't flake on
      // auth'd GETs that fire on every page mount.
      ORENDA_RATELIMIT_AUTH_BURST: '5000',
      ORENDA_RATELIMIT_AUTH_PER_SEC: '1000',
    },
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
