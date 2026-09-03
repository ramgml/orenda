import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

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
    // jsdom defaults its document URL to http://localhost:3000, so any
    // unmocked axios/XHR call (React Query hooks in TaskCard/ColumnView)
    // dials a real TCP connection and floods the log with ECONNREFUSED
    // noise. An `about:blank` document URL makes the request fail fast
    // in the URL parser instead of opening sockets. The trade-off is an
    // opaque origin, where jsdom leaves `localStorage` undefined —
    // src/test-setup.ts installs an in-memory stand-in for that case.
    environmentOptions: {
      jsdom: { url: 'about:blank' },
    },
    setupFiles: ['./src/test-setup.ts'],
  },
});
