/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    // Preserve Vite 5's syntax target instead of silently narrowing browser support.
    target: ['es2020', 'edge88', 'firefox78', 'chrome87', 'safari14'],
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
  },
})
