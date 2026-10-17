import { defineConfig } from 'vite'
import path from 'node:path'

// The 3D world alone, for the Android app's WebView (and any other native
// host): one page, its script and its two fonts, every path relative so it
// loads from wherever the host serves it. Nothing in it talks to a core; the
// Wails bindings the scene's helpers import are stand-ins (src/world-embed/stubs).
//
//   npm run build:world-embed   ->  dist-world-embed/
export default defineConfig({
  plugins: [{
    // The page is the folder's index, whatever its source is called.
    name: 'world-embed-index',
    enforce: 'post',
    generateBundle(_, bundle) {
      const page = bundle['world-embed.html']
      if (page) page.fileName = 'index.html'
    },
  }],
  base: './',
  publicDir: false,
  resolve: {
    alias: [
      { find: '@', replacement: path.resolve(__dirname, './src') },
      { find: /^(\.\.\/)+wailsjs\/go\/app\/App(\.js)?$/, replacement: path.resolve(__dirname, 'src/world-embed/stubs/app.ts') },
      { find: /^(\.\.\/)+wailsjs\/runtime(\/runtime)?(\.js)?$/, replacement: path.resolve(__dirname, 'src/world-embed/stubs/runtime.ts') },
    ],
  },
  build: {
    outDir: 'dist-world-embed',
    emptyOutDir: true,
    // Down to the WebView an Android 10 image ships with (Chrome 74): no
    // optional chaining or ?? in what it parses. WebGL2 is still needed.
    target: ['es2019', 'chrome74'],
    cssTarget: 'chrome74',
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 1200,
    modulePreload: false,
    rollupOptions: {
      input: { index: path.resolve(__dirname, 'world-embed.html') },
      // Steady names: the Android app keeps a built copy in git, and a
      // rebuild that changed nothing should change nothing there.
      output: {
        entryFileNames: 'assets/world.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
})
