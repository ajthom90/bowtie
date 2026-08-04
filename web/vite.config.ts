/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://localhost:8400',
    },
  },
  build: {
    // Built assets land inside the Go embed package.
    outDir: '../server/internal/web/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    globals: false,
  },
})
