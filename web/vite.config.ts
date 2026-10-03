import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"

const apiTarget = process.env.WISPJAR_API ?? "http://localhost:8080"
const proxy = {
  "/api": {
    target: apiTarget,
    changeOrigin: true,
    ws: true,
    // 后端对 /api/ws 做 Origin 校验（同源或白名单），代理时改写为 target 源
    headers: { Origin: apiTarget },
  },
}

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: { proxy },
  preview: { proxy },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
})
