/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In development the Go server runs on :8080 (`papers serve --no-authn
// --ws-origin localhost:5173`) and Vite proxies API, WebSocket and MCP calls.
export default defineConfig(({ mode }) => ({
  plugins: [react()],
  // The demo build runs from any path (it is hosted as a static page).
  base: mode === 'demo' ? './' : '/',
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', ws: true },
      '/mcp': { target: 'http://localhost:8080' },
    },
  },
  build: {
    outDir: mode === 'demo' ? 'dist-demo' : 'dist',
    emptyOutDir: mode === 'demo',
    chunkSizeWarningLimit: 2000,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    // e2e/ holds the Playwright tests, run by `pnpm test:e2e`.
    include: ['src/**/*.test.{ts,tsx}'],
  },
}))
