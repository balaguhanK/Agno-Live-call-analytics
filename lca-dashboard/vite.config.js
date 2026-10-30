import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backend = `http://localhost:${env.PORT || 8090}`
  // Same-origin proxy so the session cookie works without CORS
  const proxy = {
    '/api': backend,
    '/ws': { target: backend, ws: true },
  }
  return {
    plugins: [react()],
    server: { proxy },
    preview: { proxy },
  }
})
