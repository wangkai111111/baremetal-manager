import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'
import { forwardClientIP } from './dev/clientIp'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      // @ 路径别名指向 src/ 目录
      '@': resolve(__dirname, 'src')
    }
  },
  server: {
    proxy: {
      // Keep UI routes such as /api-keys and /api-docs inside the SPA.
      '^/api(?:/|$)': {
        target: process.env.BMM_API_TARGET || 'http://localhost:18080', changeOrigin: false, ws: true,
        configure(proxy) {
          proxy.on('proxyReq', forwardClientIP)
          proxy.on('proxyReqWs', forwardClientIP)
        }
      }
    }
  }
})
