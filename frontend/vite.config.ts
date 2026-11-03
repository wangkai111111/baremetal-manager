import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

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
      '/api': { target: 'http://localhost:8000', changeOrigin: true, ws: true }
    }
  }
})
