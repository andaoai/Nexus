import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物输出到 Go embed 目录；开发期 /api 代理到本机后端
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  build: {
    outDir: '../internal/api/webdist',
    emptyOutDir: true,
  },
})
