import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import { fileURLToPath, URL } from 'node:url'

// Vite + React + TS configuration for Orenda.
//
// Dev server proxies /api and /ws to the Go backend on :2137 so the SPA can
// call the REST API directly with no CORS gymnastics.
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.svg'],
      manifest: {
        name: 'Orenda',
        short_name: 'Orenda',
        description: 'Local-first productivity suite with first-class AI agents',
        theme_color: '#3b82f6',
        background_color: '#0f172a',
        display: 'standalone',
        start_url: '/',
        icons: [
          {
            src: '/favicon.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'any maskable',
          },
        ],
      },
      workbox: {
        navigateFallback: '/index.html',
        runtimeCaching: [
          {
            // API reads: network-first, fall back to cache when offline.
            urlPattern: /^\/api\/v1\/.*$/i,
            handler: 'NetworkFirst',
            options: {
              cacheName: 'orenda-api',
              expiration: { maxEntries: 200, maxAgeSeconds: 3600 },
              networkTimeoutSeconds: 3,
            },
          },
          {
            // Static assets: cache-first.
            urlPattern: /^\/assets\/.*$/i,
            handler: 'CacheFirst',
            options: {
              cacheName: 'orenda-assets',
              expiration: { maxEntries: 100, maxAgeSeconds: 86400 * 30 },
            },
          },
        ],
      },
    }),
  ],
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