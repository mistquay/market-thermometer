import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 构建产物输出到 Go 的 embed 目录；开发时把 /api 代理到本地 Go 服务。
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
