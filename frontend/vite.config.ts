import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    // `npm run dev` against a running `superai-desktop serve -port 43117`:
    // the shim's /api calls land on the Go server, hot reload stays on vite.
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:43117',
        changeOrigin: false,
      },
    },
  },
})
