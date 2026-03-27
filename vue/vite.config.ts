import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import vueDevTools from 'vite-plugin-vue-devtools'

// https://vite.dev/config/
export default defineConfig({
  base: '/app/',
  plugins: [
    vue(),
    vueDevTools(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    },
  },
  build: {
    outDir: '../web/views',
    emptyOutDir: true,
  },
  server: {
    allowedHosts: ['.altclaw.local'],
    proxy: {
      '/api': {
        target: 'http://localhost:9090',
        changeOrigin: false,
        // Bypass http-proxy's response buffering for SSE streams
        selfHandleResponse: true,
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes, _req, res) => {
            // Copy status and headers
            res.writeHead(proxyRes.statusCode!, proxyRes.headers)
            // Pipe directly — no buffering
            proxyRes.pipe(res)
          })
        }
      },
      '/auth': {
        target: 'http://localhost:9090',
        changeOrigin: false,
      },
    },
  },
})
