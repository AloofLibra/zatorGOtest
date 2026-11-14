import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Сборка обязана выдавать в ../webui ровно app.js + styles.css + index.html
// (файловый состав webui/ — контракт установки z2r.sh).
export default defineConfig({
  plugins: [vue()],
  base: './',
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8099',
      '/cgi-bin': 'http://127.0.0.1:8099',
      '/favicon.png': 'http://127.0.0.1:8099',
      '/icon.png': 'http://127.0.0.1:8099',
    },
  },
  build: {
    outDir: '../assets/webui',
    emptyOutDir: false,
    target: 'es2018',
    cssCodeSplit: false,
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: {
      output: {
        format: 'iife',
        inlineDynamicImports: true,
        entryFileNames: 'app.js',
        assetFileNames: 'styles.css',
      },
    },
  },
})
