import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

// Minimal Vitest config for the web/ workspace.
//
// We only run a handful of smoke tests over pure-TS modules (api
// client shape, outbox helpers) so the setup stays dependency-light —
// no jsdom, no @testing-library bootstrapping unless a test asks for
// it. Tests live next to source under `*.test.ts(x)` files.
export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'node',
    globals: false,
  },
})