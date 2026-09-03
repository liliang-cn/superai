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
  build: {
    rollupOptions: {
      // Two pages out of one build. The avatar page is served by a different
      // server on a different port (backend/avatar.go) to whatever external
      // renderer is pointed at it, but it draws replies with the same AIGUI
      // plugins the chat window does — so it is built here, beside the app,
      // and shares the vendor chunks rather than duplicating React and every
      // plugin into a bundle of its own.
      input: {
        main: path.resolve(__dirname, 'index.html'),
        avatar: path.resolve(__dirname, 'avatar.html'),
      },
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
