import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // Dev-only: the real deployment is same-origin (ARCHITECTURE.md §4.11),
    // embedded into cmd/api via go:embed (task 7.4). This proxy exists only
    // so `npm run dev` can talk to a locally running `cmd/api` without a
    // second CORS story to build and then throw away.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
