import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `npm run dev` proxies the API to a panel running locally on :8090.
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:8090' } },
})
