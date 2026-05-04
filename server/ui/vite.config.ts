import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../static',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: (chunkInfo) => {
          const id = chunkInfo.facadeModuleId || ''
          if (/monaco-editor\/esm\/vs\/(basic-languages|language)\//.test(id)) {
            return 'assets/lang/[name].js'
          }
          return 'assets/[name].js'
        },
        assetFileNames: 'assets/[name].[ext]',
      },
    },
  },
  worker: {
    rollupOptions: {
      output: {
        entryFileNames: 'assets/workers/[name].js',
        chunkFileNames: 'assets/workers/[name].js',
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:4242',
      '/ws': {
        target: 'ws://localhost:4242',
        ws: true,
      },
    },
  },
})
