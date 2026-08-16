import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

// Minimal Vitest config for the web/ workspace.
//
// Default environment is `node` so the pure-TS modules (api client
// shape, outbox helpers) keep running without a DOM. React-component
// tests opt into `jsdom` via a `// @vitest-environment jsdom` comment
// at the top of the file — this avoids paying the jsdom bootstrap
// cost for tests that don't need it.
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