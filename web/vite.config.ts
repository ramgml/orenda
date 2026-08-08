import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

// Vite + React + TS configuration for Orenda.
//
// Dev server proxies /api and /ws to the Go backend on :2137 so the SPA can
// call the REST API directly with no CORS gymnastics.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    strictPort: false,
    // Proxy backend endpoints before the SPA history fallback can swallow them.
    // Anything not matched here falls through to Vite's dev middleware
    // (which serves the React shell).
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:2137',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://127.0.0.1:2137',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://127.0.0.1:2137',
        ws: true,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    target: 'es2022',
  },
})